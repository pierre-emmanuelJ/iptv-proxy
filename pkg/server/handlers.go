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
	"context"
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
	"github.com/pierre-emmanuelJ/iptv-proxy/pkg/xtream"
)

const (
	// maxFormBytes bounds the body of the form requests the proxy reads
	// (credentials and API parameters).
	maxFormBytes = 1 << 20
	// sniffBytes is how much of a response is looked at to tell a playlist
	// from media: "#EXTM3U", after a byte order mark and a few blanks. Kept
	// small, as a slow stream is not sent on before they are read.
	sniffBytes = 16
	// maxErrorPageBytes bounds a provider's error page.
	maxErrorPageBytes = 1 << 20
	// maxPlaylistBytes bounds an HLS playlist: a day of DVR window is a few
	// hundred kilobytes.
	maxPlaylistBytes = 16 << 20
)

func (c *Config) serveM3U(ctx *gin.Context, playlist []byte) {
	ctx.Header("Content-Disposition", fmt.Sprintf(`attachment; filename=%q`, c.M3UFileName))
	ctx.Data(http.StatusOK, "application/octet-stream", playlist)
}

// fileExtensions are those of media with a beginning and an end: each client
// reads its own copy, from where it wants.
var fileExtensions = map[string]bool{
	".mp4": true, ".mkv": true, ".avi": true, ".mov": true, ".m4v": true, ".wmv": true,
	".flv": true, ".webm": true, ".mpg": true, ".mpeg": true, ".vob": true, ".3gp": true,
	".mp3": true, ".m4a": true, ".flac": true, ".wav": true, ".ogg": true,
	".srt": true, ".vtt": true, ".jpg": true, ".jpeg": true, ".png": true,
}

// streamLive serves a live stream: every client watching it shares one
// connection to the provider, opened again if the provider drops it.
func (c *Config) streamLive(ctx *gin.Context, oriURL *url.URL) {
	// A range is a request for a part of a file: not live television.
	if r := ctx.Request.Header.Get("Range"); c.NoStreamSharing || (r != "" && r != "bytes=0-") {
		c.stream(ctx, oriURL)
		return
	}

	// The provider is asked with this client's headers; the stream then
	// lives as long as anyone watches it, not as long as this request.
	header := c.upstreamHeader(ctx, true)
	sub, resp, err := c.hub.Join(oriURL.String(), func(streamCtx context.Context) (*http.Response, error) {
		return c.upstreamDo(streamCtx, c.client, oriURL.String(), header)
	})
	if err != nil {
		c.upstreamError(ctx, err)
		return
	}
	if resp != nil {
		// Not a live stream after all (a playlist, a file, an error).
		defer resp.Body.Close() // nolint: errcheck
		c.deliver(ctx, resp)
		return
	}
	defer sub.Close()

	c.dropLeakingHeaders(sub.Header)
	mergeHttpHeader(ctx.Writer.Header(), sub.Header)
	ctx.Status(http.StatusOK)
	ctx.Writer.WriteHeaderNow()
	ctx.Writer.Flush()

	gone := ctx.Request.Context().Done()
	for {
		select {
		case chunk, ok := <-sub.C:
			if !ok {
				return // the provider is gone for good, or this client is too slow
			}
			if _, err := ctx.Writer.Write(chunk); err != nil {
				return
			}
			ctx.Writer.Flush()
		case <-gone:
			return
		}
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

	c.deliver(ctx, resp)
}

// deliver sends a provider answer to the client: a playlist rewritten,
// anything else as it comes.
func (c *Config) deliver(ctx *gin.Context, resp *http.Response) {
	// Media or playlist: the first bytes tell, whatever the address and
	// the content type say.
	body := bufio.NewReader(resp.Body)
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
	c.dropLeakingHeaders(resp.Header)

	// An error page is not media: it may repeat the address it was asked,
	// which holds the provider's credentials.
	if resp.StatusCode >= http.StatusBadRequest {
		page, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorPageBytes))
		c.errorPage(ctx, resp.StatusCode, resp.Header, page)
		return
	}

	mergeHttpHeader(ctx.Writer.Header(), resp.Header)
	ctx.Status(resp.StatusCode)
	ctx.Writer.WriteHeaderNow()
	// The copy ends when the provider or the client stops: neither is an
	// error worth reporting.
	_, _ = io.Copy(flushWriter{ctx.Writer}, resp.Body)
}

// errorPage sends a provider's error answer without its credentials.
func (c *Config) errorPage(ctx *gin.Context, status int, header http.Header, page []byte) {
	page = xtream.Scrub(page, c.providerAccount(), c.proxyAccount())

	header.Del("Content-Length") // the page changed
	mergeHttpHeader(ctx.Writer.Header(), header)
	ctx.Data(status, header.Get("Content-Type"), page)
}

// dropLeakingHeaders removes the provider's headers that hold its password
// (a Location, a cookie, a link).
func (c *Config) dropLeakingHeaders(header http.Header) {
	for name, values := range header {
		for _, value := range values {
			if xtream.Leaks(value, c.providerAccount()) {
				header.Del(name)
				break
			}
		}
	}
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
	return c.upstreamDo(ctx.Request.Context(), client, rawURL, c.upstreamHeader(ctx, forwardHeaders))
}

// upstreamHeader builds the headers of a request to the provider made for a
// client.
func (c *Config) upstreamHeader(ctx *gin.Context, forwardHeaders bool) http.Header {
	header := http.Header{}
	if forwardHeaders {
		mergeHttpHeader(header, ctx.Request.Header)
		// Media is not compressed, and a playlist must be readable.
		header.Del("Accept-Encoding")
	}
	header.Set("User-Agent", c.upstreamUserAgent(ctx.Request.UserAgent()))
	return header
}

func (c *Config) upstreamDo(ctx context.Context, client *http.Client, rawURL string, header http.Header) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, errors.New("invalid provider address")
	}
	req.Header = header.Clone()

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
