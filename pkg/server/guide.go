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
	"bufio"
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/pierre-emmanuelJ/iptv-proxy/pkg/xmltv"
)

// channelMap gives the ids a guide channel is sent under (see xmltv.Map).
type channelMap func(channel string) []string

// keeping is the channelMap of a set of channels kept as they are.
func keeping(kept map[string]bool) channelMap {
	return func(channel string) []string {
		if kept[channel] {
			return []string{channel}
		}
		return nil
	}
}

// guide sends a guide (XMLTV) as it comes from address: it can weigh
// hundreds of megabytes. When mapping is given, the channels are sent as the
// map it returns says, with their programmes. A guide sent in full is kept
// under key, compressed, for when the provider fails.
func (c *Config) guide(ctx *gin.Context, address, key string, mapping func() (channelMap, error)) {
	var channels channelMap
	if mapping != nil {
		var err error
		if channels, err = mapping(); err != nil {
			if !c.lastGood.serve(ctx, key, err.Error()) {
				c.upstreamError(ctx, err)
			}
			return
		}
	}

	resp, err := c.openGuide(ctx, address)
	status := 0
	if err == nil {
		defer resp.Body.Close() // nolint: errcheck
		status = resp.StatusCode
	}
	if providerFailed(status, err) && c.lastGood.serve(ctx, key, failure(status, err, false)) {
		return
	}
	if err != nil {
		c.upstreamError(ctx, err)
		return
	}
	c.dropLeakingHeaders(resp.Header)
	if resp.StatusCode >= http.StatusBadRequest {
		c.passOn(ctx, resp) // an error page, without the provider's credentials
		return
	}
	if resp.Header.Get("Content-Type") == "" {
		resp.Header.Set("Content-Type", "application/xml")
	}

	body := bufio.NewReader(resp.Body)
	// A guide sent with a Content-Encoding is passed on as it is, and not
	// kept: the client decodes it, the proxy does not.
	keep := resp.Header.Get("Content-Encoding") == ""
	var src io.Reader = body
	if channels != nil {
		// Filtered as it streams: it is sent uncompressed, with a length
		// nobody knows yet.
		if start, _ := body.Peek(2); bytes.Equal(start, []byte{0x1f, 0x8b}) || strings.EqualFold(resp.Header.Get("Content-Encoding"), "gzip") {
			unzipped, err := gzip.NewReader(body)
			if err != nil {
				c.upstreamError(ctx, err)
				return
			}
			defer unzipped.Close() // nolint: errcheck
			src = unzipped
			resp.Header.Set("Content-Type", "application/xml")
			keep = true
		}
		for _, name := range []string{"Content-Length", "Content-Encoding", "Content-Range", "Accept-Ranges", "Etag", "Content-Md5"} {
			resp.Header.Del(name)
		}
	}

	mergeHttpHeader(ctx.Writer.Header(), resp.Header)
	ctx.Status(resp.StatusCode)
	ctx.Writer.WriteHeaderNow()

	record := newRecorder()
	out := io.MultiWriter(flushWriter{ctx.Writer}, record)
	if channels != nil {
		err = xmltv.Map(out, src, channels)
	} else {
		_, err = io.Copy(out, src)
	}
	// Only a guide read to its end and sent in full is kept.
	if err == nil && keep && resp.StatusCode == http.StatusOK {
		if compressed, ok := record.finish(); ok {
			c.lastGood.put(key, resp.Header.Get("Content-Type"), compressed)
		}
	}
}

// openGuide opens a guide: an http(s) address, asked for the client, or a
// local file.
func (c *Config) openGuide(ctx *gin.Context, address string) (*http.Response, error) {
	if strings.HasPrefix(address, "http://") || strings.HasPrefix(address, "https://") {
		return c.upstream(ctx, c.apiClient, address, false)
	}
	f, err := os.Open(address)
	if err != nil {
		return nil, err
	}
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: f, ContentLength: -1}, nil
}
