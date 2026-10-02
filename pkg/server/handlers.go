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
	"crypto/subtle"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/pierre-emmanuelJ/iptv-proxy/pkg/hls"
)

const (
	// maxFormBytes bounds the body of the form requests the proxy reads
	// (credentials and API parameters).
	maxFormBytes = 1 << 20
	// sniffBytes is how much of a response is looked at to tell a playlist
	// from media.
	sniffBytes = 512
	// maxPlaylistBytes bounds an HLS playlist: a day of DVR window is a few
	// hundred kilobytes.
	maxPlaylistBytes = 16 << 20
)

func (c *Config) getM3U(ctx *gin.Context) {
	c.serveM3U(ctx, c.proxyfiedM3U)
}

func (c *Config) serveM3U(ctx *gin.Context, playlist []byte) {
	ctx.Header("Content-Disposition", fmt.Sprintf(`attachment; filename=%q`, c.M3UFileName))
	ctx.Data(http.StatusOK, "application/octet-stream", playlist)
}

func (c *Config) reverseProxy(track *url.URL) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		c.stream(ctx, track)
	}
}

// stream passes a provider resource on to the client, for as long as the
// client stays. An HLS playlist is rewritten on the way, so that everything
// it names is asked to the proxy.
func (c *Config) stream(ctx *gin.Context, oriURL *url.URL) {
	resp, err := c.upstream(ctx, c.client, oriURL.String(), true)
	if err != nil {
		c.upstreamError(ctx, err)
		return
	}
	defer resp.Body.Close() // nolint: errcheck

	// Media or playlist: the first bytes tell, whatever the address and
	// the content type say.
	body := bufio.NewReaderSize(resp.Body, sniffBytes)
	start, _ := body.Peek(sniffBytes) // a short or failing body is passed on as it is
	resp.Body = struct {
		io.Reader
		io.Closer
	}{body, resp.Body}

	if !hls.IsPlaylist(start) {
		c.passOn(ctx, resp)
		return
	}

	playlist, err := io.ReadAll(io.LimitReader(resp.Body, maxPlaylistBytes+1))
	if err != nil {
		c.upstreamError(ctx, withoutURL(err))
		return
	}
	if len(playlist) > maxPlaylistBytes {
		ctx.AbortWithError(http.StatusBadGateway, errors.New("the provider's playlist is too large")) // nolint: errcheck
		return
	}

	// Addresses are relative to where the playlist really came from, after
	// the provider's redirects.
	rewritten := hls.Rewrite(playlist, resp.Request.URL, c.hlsAddress)

	// The provider's headers describe its own bytes, not the rewritten
	// ones: a player trusting the original size would cut the playlist.
	for _, name := range []string{"Content-Length", "Content-Range", "Accept-Ranges", "Etag", "Content-Md5"} {
		resp.Header.Del(name)
	}
	status := resp.StatusCode
	if status == http.StatusPartialContent {
		// The whole playlist is sent, whatever range was asked ("bytes=0-"
		// is what players send).
		status = http.StatusOK
	}
	mergeHttpHeader(ctx.Writer.Header(), resp.Header)
	ctx.Data(status, resp.Header.Get("Content-Type"), rewritten)
}

// hlsAddress is where the proxy serves an address found in an HLS playlist:
// "<endpoint>/hls/<token>/<name>". The name is only there for players that
// look at the extension. The address has no host: the player asks the host
// it got the playlist from.
func (c *Config) hlsAddress(address *url.URL) string {
	name := path.Base(address.Path)
	if name == "" || name == "." || name == "/" {
		name = "stream"
	}

	prefix := strings.Trim(c.CustomEndpoint, "/")
	if prefix != "" {
		prefix = "/" + prefix
	}
	return prefix + "/hls/" + c.tokens.seal(address.String()) + "/" + url.PathEscape(name)
}

// hlsStream serves an address named by a playlist the proxy rewrote. The
// token is the right to fetch it: only the proxy can have issued it.
func (c *Config) hlsStream(ctx *gin.Context) {
	address, err := c.tokens.open(ctx.Param("token"))
	if err != nil {
		ctx.AbortWithStatus(http.StatusNotFound)
		return
	}
	target, err := url.Parse(address)
	if err != nil {
		ctx.AbortWithStatus(http.StatusNotFound)
		return
	}

	c.stream(ctx, target)
}

// passOn copies a provider response to the client.
func (c *Config) passOn(ctx *gin.Context, resp *http.Response) {
	mergeHttpHeader(ctx.Writer.Header(), resp.Header)
	ctx.Status(resp.StatusCode)
	ctx.Writer.WriteHeaderNow()
	// The copy ends when the provider or the client stops: neither is an
	// error worth reporting.
	_, _ = io.Copy(flushWriter{ctx.Writer}, resp.Body)
}

// flushWriter sends each chunk as it comes: a live stream is not held back
// in the server's buffer.
type flushWriter struct {
	gin.ResponseWriter
}

func (w flushWriter) Write(p []byte) (int, error) {
	n, err := w.ResponseWriter.Write(p)
	w.Flush()
	return n, err
}

// upstream sends a GET to the provider on behalf of the client: it is
// cancelled when the client leaves. With forwardHeaders the client's headers
// go along (Range, for seeking in a movie); otherwise only a User-Agent.
func (c *Config) upstream(ctx *gin.Context, client *http.Client, rawURL string, forwardHeaders bool) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx.Request.Context(), http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, errors.New("invalid provider address")
	}

	if forwardHeaders {
		mergeHttpHeader(req.Header, ctx.Request.Header)
		// Media is not compressed, and a playlist must be readable.
		req.Header.Del("Accept-Encoding")
	}
	req.Header.Set("User-Agent", c.upstreamUserAgent(ctx.Request.UserAgent()))

	resp, err := client.Do(req)
	if err != nil {
		return nil, withoutURL(err)
	}
	return resp, nil
}

// upstreamError answers for a provider that could not be reached.
func (c *Config) upstreamError(ctx *gin.Context, err error) {
	if ctx.Request.Context().Err() != nil {
		ctx.Abort() // the client left: nobody to answer
		return
	}
	ctx.AbortWithError(http.StatusBadGateway, err) // nolint: errcheck
}

type values []string

func (vs values) contains(s string) bool {
	for _, v := range vs {
		if v == s {
			return true
		}
	}

	return false
}

// hopByHop headers describe one connection, not the resource: they are not
// passed from one side of the proxy to the other.
var hopByHop = map[string]bool{
	"Connection":          true,
	"Keep-Alive":          true,
	"Proxy-Authenticate":  true,
	"Proxy-Authorization": true,
	"Proxy-Connection":    true,
	"Te":                  true,
	"Trailer":             true,
	"Transfer-Encoding":   true,
	"Upgrade":             true,
}

func mergeHttpHeader(dst, src http.Header) {
	// Headers named by "Connection" are hop-by-hop too.
	named := map[string]bool{}
	for _, v := range src.Values("Connection") {
		for _, name := range strings.Split(v, ",") {
			named[http.CanonicalHeaderKey(strings.TrimSpace(name))] = true
		}
	}

	for k, vv := range src {
		if key := http.CanonicalHeaderKey(k); hopByHop[key] || named[key] {
			continue
		}
		for _, v := range vv {
			if values(dst.Values(k)).contains(v) {
				continue
			}
			dst.Add(k, v)
		}
	}
}

// authenticate checks the "username" and "password" of a request, given in
// its query or in its form body.
func (c *Config) authenticate(ctx *gin.Context) {
	ctx.Request.Body = http.MaxBytesReader(ctx.Writer, ctx.Request.Body, maxFormBytes)
	if err := ctx.Request.ParseMultipartForm(maxFormBytes); err != nil && !errors.Is(err, http.ErrNotMultipart) {
		ctx.AbortWithError(http.StatusBadRequest, err) // nolint: errcheck
		return
	}

	username, password := ctx.Request.Form.Get("username"), ctx.Request.Form.Get("password")
	if username == "" || password == "" {
		ctx.AbortWithError(http.StatusBadRequest, errors.New("missing username or password")) // nolint: errcheck
		return
	}

	userOK := subtle.ConstantTimeCompare([]byte(username), []byte(c.User.String())) == 1
	passwordOK := subtle.ConstantTimeCompare([]byte(password), []byte(c.Password.String())) == 1
	if !userOK || !passwordOK {
		ctx.AbortWithStatus(http.StatusUnauthorized)
	}
}
