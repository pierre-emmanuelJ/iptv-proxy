/*
 * Iptv-Proxy is a project to proxyfie an m3u file and to proxyfie an Xtream iptv service (client API).
 * Copyright (C) 2020  Pierre-Emmanuel Jacquier
 *
 * This program is free software: you can redistribute it and/or modify
 * it under the terms of the GNU General Public License as published by
 * the Free Software Foundation, either version 3 of the License, or
 * (at your option) any later version.
 *
 * This program is distributed in the hope that it will be useful,
 * but WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
 * GNU General Public License for more details.
 *
 * You should have received a copy of the GNU General Public License
 * along with this program.  If not, see <https://www.gnu.org/licenses/>.
 */

package server

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// maxLastGoodBytes bounds the memory the last good answers take, compressed.
// A catalogue of 150,000 movies and a guide of 3,500 channels take about
// 15 MB.
var maxLastGoodBytes = 64 << 20

// maxAnswerBytes bounds an answer once uncompressed: a guide of a large
// catalogue, with room to spare.
const maxAnswerBytes = 4 << 30

// lastGood keeps, for each request that lists something (the login, a
// catalogue, a playlist, the guide), the last answer the provider gave in
// full. When the provider fails, clients get that answer instead of an
// error: yesterday's channel list plays, an error page does not.
//
// Answers are kept compressed: lists and guides shrink tenfold.
type lastGood struct {
	mu      sync.Mutex
	answers map[string]goodAnswer
	size    int
}

type goodAnswer struct {
	compressed  []byte
	contentType string
	at          time.Time
}

func newLastGood() *lastGood {
	return &lastGood{answers: map[string]goodAnswer{}}
}

// put keeps an answer, dropping the oldest ones when there is no room.
func (l *lastGood) put(key, contentType string, compressed []byte) {
	if len(compressed) > maxLastGoodBytes {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	if old, ok := l.answers[key]; ok {
		l.size -= len(old.compressed)
	}
	l.answers[key] = goodAnswer{compressed: compressed, contentType: contentType, at: time.Now()}
	l.size += len(compressed)

	for l.size > maxLastGoodBytes {
		oldest := ""
		for k, a := range l.answers {
			if oldest == "" || a.at.Before(l.answers[oldest].at) {
				oldest = k
			}
		}
		l.size -= len(l.answers[oldest].compressed)
		delete(l.answers, oldest)
	}
}

func (l *lastGood) get(key string) (goodAnswer, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	a, ok := l.answers[key]
	return a, ok
}

// keep compresses and keeps a whole answer.
func (l *lastGood) keep(key, contentType string, body []byte) {
	var buf bytes.Buffer
	// BestSpeed is a valid level, and a bytes.Buffer does not fail.
	zw, _ := gzip.NewWriterLevel(&buf, gzip.BestSpeed)
	_, _ = zw.Write(body)
	_ = zw.Close()
	l.put(key, contentType, buf.Bytes())
}

// serve sends the kept answer for key, if there is one, and tells whether it
// did.
func (l *lastGood) serve(ctx *gin.Context, key string, why string) bool {
	a, ok := l.get(key)
	if !ok {
		return false
	}
	zr, err := gzip.NewReader(bytes.NewReader(a.compressed))
	if err != nil {
		return false
	}
	defer zr.Close() // nolint: errcheck

	age := time.Since(a.at).Round(time.Second)
	log.Printf("[iptv-proxy] %s: the provider failed (%s), serving its last good answer, %s old", requestName(ctx), why, age)

	ctx.Header("Content-Type", a.contentType)
	ctx.Header("Age", age.String())
	ctx.Status(http.StatusOK)
	// The answer was compressed by the proxy itself; still, nothing larger
	// than a guide comes out of it.
	_, _ = io.Copy(ctx.Writer, io.LimitReader(zr, maxAnswerBytes))
	return true
}

// recorder keeps a copy of an answer as it is sent, compressed, up to the
// size lastGood takes.
type recorder struct {
	buf      bytes.Buffer
	zw       *gzip.Writer
	overflow bool
}

func newRecorder() *recorder {
	r := &recorder{}
	r.zw, _ = gzip.NewWriterLevel(&r.buf, gzip.BestSpeed) // a valid level
	return r
}

func (r *recorder) Write(p []byte) (int, error) {
	if !r.overflow {
		_, _ = r.zw.Write(p) // a bytes.Buffer does not fail
		if r.buf.Len() > maxLastGoodBytes {
			r.overflow = true
			r.buf = bytes.Buffer{}
		}
	}
	return len(p), nil
}

// finish returns the compressed answer, or false when it was too large.
func (r *recorder) finish() ([]byte, bool) {
	if r.overflow || r.zw.Close() != nil || r.buf.Len() > maxLastGoodBytes {
		return nil, false
	}
	return r.buf.Bytes(), true
}

// providerFailed tells whether a provider answer is a failure worth hiding
// behind the last good answer: no answer, a server error, or a "not found"
// some providers answer for a moment. A refusal (401, 403) is not hidden: it
// says something about the account.
func providerFailed(status int, err error) bool {
	return err != nil || status >= http.StatusInternalServerError || status == http.StatusNotFound
}

// failure says how the provider failed, for the logs.
func failure(status int, err error, invalid bool) string {
	switch {
	case err != nil:
		return err.Error()
	case invalid:
		return "an answer that is not what was asked"
	default:
		return fmt.Sprintf("HTTP %d", status)
	}
}

// answerKey names a request by its endpoint and parameters, without the
// credentials.
func answerKey(endpoint string, params url.Values) string {
	keys := make([]string, 0, len(params))
	for k := range params {
		if k != "username" && k != "password" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString(endpoint)
	for _, k := range keys {
		b.WriteString("&" + url.QueryEscape(k) + "=" + url.QueryEscape(strings.Join(params[k], ",")))
	}
	return b.String()
}

// userAnswerKey names a user's request: answers hold the user's
// credentials, and filters may differ from one user to the next.
func userAnswerKey(u *proxyUser, endpoint string, params url.Values) string {
	return u.name.String() + "\x00" + answerKey(endpoint, params)
}

// requestName is how a request is named in a log line: its path and action,
// without credentials.
func requestName(ctx *gin.Context) string {
	name := ctx.Request.URL.Path
	if action := ctx.Request.Form.Get("action"); action != "" {
		name += " " + action
	}
	return name
}
