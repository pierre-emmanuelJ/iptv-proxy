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
	"log"
	"net/http"
	"net/url"
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
// map it returns says, with their programmes. The channels of the guides at
// others that address does not have are added to it (several sources). A
// guide sent in full is kept under key, compressed, for when the provider
// fails.
func (c *Config) guide(ctx *gin.Context, address, key string, mapping func() (channelMap, error), others []string) {
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

	if len(others) > 0 && channels == nil {
		channels = func(channel string) []string { return []string{channel} }
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
		var unzip bool
		if src, unzip, err = unzipped(body, resp.Header); err != nil {
			c.upstreamError(ctx, err)
			return
		}
		if unzip {
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
	switch {
	case len(others) > 0:
		// A channel is the first source's when it has it, as in the lists.
		seen := map[string]bool{}
		first := func(channel string) []string {
			seen[channel] = true
			return channels(channel)
		}
		err = xmltv.Merge(out, src, first, func(w io.Writer) error {
			for _, other := range others {
				if !c.addGuide(ctx, w, other, channels, seen) {
					keep = false // incomplete
				}
			}
			return nil
		})
	case channels != nil:
		err = xmltv.Map(out, src, channels)
	default:
		_, err = io.Copy(out, src)
	}
	// Only a guide read to its end and sent in full is kept.
	if err == nil && keep && resp.StatusCode == http.StatusOK {
		if compressed, ok := record.finish(); ok {
			c.lastGood.put(key, resp.Header.Get("Content-Type"), compressed)
		}
	}
}

// unzipped is a guide decompressed, when it is compressed (some providers
// send it so unasked).
func unzipped(body *bufio.Reader, header http.Header) (io.Reader, bool, error) {
	if start, _ := body.Peek(2); !bytes.Equal(start, []byte{0x1f, 0x8b}) && !strings.EqualFold(header.Get("Content-Encoding"), "gzip") {
		return body, false, nil
	}
	zr, err := gzip.NewReader(body)
	if err != nil {
		return nil, false, err
	}
	return zr, true, nil
}

// addGuide writes the channels of the guide at address that are not seen
// yet, as channels maps them, with their programmes. It tells whether the
// guide was read in full.
func (c *Config) addGuide(ctx *gin.Context, w io.Writer, address string, channels channelMap, seen map[string]bool) bool {
	resp, err := c.openGuide(ctx, address)
	if err != nil {
		log.Printf("[iptv-proxy] guide of another source: %v", err)
		return false
	}
	defer resp.Body.Close() // nolint: errcheck
	if resp.StatusCode != http.StatusOK {
		log.Printf("[iptv-proxy] guide of another source: HTTP %d", resp.StatusCode)
		return false
	}
	src, _, err := unzipped(bufio.NewReader(resp.Body), resp.Header)
	if err != nil {
		log.Printf("[iptv-proxy] guide of another source: %v", err)
		return false
	}
	added := map[string]bool{}
	err = xmltv.Elements(w, src, func(channel string) []string {
		if seen[channel] {
			return nil
		}
		added[channel] = true
		return channels(channel)
	})
	for channel := range added {
		seen[channel] = true
	}
	if err != nil {
		log.Printf("[iptv-proxy] guide of another source: %v", err)
		return false
	}
	return true
}

// otherGuides are the guides of the sources after the first, when there are
// several.
func (c *Config) otherGuides(params url.Values) []string {
	if !c.merged() {
		return nil
	}
	others := make([]string, 0, len(c.sources)-1)
	for _, src := range c.sources[1:] {
		others = append(others, src.account.APIURL("xmltv.php", params))
	}
	return others
}

// openGuide opens a guide: an http(s) address, asked for the client, or a
// local file.
func (c *Config) openGuide(ctx *gin.Context, address string) (*http.Response, error) {
	if strings.HasPrefix(address, "http://") || strings.HasPrefix(address, "https://") {
		return c.upstream(ctx, c.apiClient, address)
	}
	f, err := os.Open(address)
	if err != nil {
		return nil, err
	}
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: f, ContentLength: -1}, nil
}
