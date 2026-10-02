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
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/pierre-emmanuelJ/iptv-proxy/pkg/m3u"
	"github.com/pierre-emmanuelJ/iptv-proxy/pkg/xmltv"
	"github.com/pierre-emmanuelJ/iptv-proxy/pkg/xtream"
)

// Filtering keeps the channels a client sees: in playlists, in the live
// lists of the Xtream API and in the guide. Movies and series are not
// filtered. Without filters, the provider's answers are passed on as they
// are.

// liveGroups are the names of the provider's live categories, by id. The
// stream list only gives a category's id, and the filters are on its name.
type liveGroups struct {
	mu    sync.Mutex
	names map[string]string
	at    time.Time
}

// liveGroupNames returns the provider's live categories, asked again once
// they are older than the playlist cache.
func (c *Config) liveGroupNames(ctx *gin.Context) (map[string]string, error) {
	c.groups.mu.Lock()
	defer c.groups.mu.Unlock()

	if c.groups.names != nil && time.Since(c.groups.at) < time.Duration(c.M3UCacheExpiration)*time.Hour {
		return c.groups.names, nil
	}
	categories, err := c.providerList(ctx, "get_live_categories")
	if err != nil {
		return nil, err
	}
	names := make(map[string]string, len(categories))
	for _, category := range categories {
		names[xtream.Text(category["category_id"])] = xtream.Text(category["category_name"])
	}
	c.groups.names, c.groups.at = names, time.Now()
	return names, nil
}

// keepStream tells whether a live stream of the API is kept. A stream may
// name several categories; it is kept when one of them is.
func (c *Config) keepStream(stream map[string]any, groups map[string]string) bool {
	name := xtream.Text(stream["name"])
	ids := []string{xtream.Text(stream["category_id"])}
	if more, ok := stream["category_ids"].([]any); ok {
		for _, id := range more {
			ids = append(ids, xtream.Text(id))
		}
	}
	for _, id := range ids {
		if c.rules.Keep(groups[id], name) {
			return true
		}
	}
	return false
}

// filterAPIList drops, from an answer of the API, the live categories and
// streams the filters leave out. Other answers are returned as they are.
func (c *Config) filterAPIList(ctx *gin.Context, action string, body []byte) ([]byte, error) {
	if !c.rules.Active() {
		return body, nil
	}
	switch action {
	case "get_live_categories":
		return xtream.FilterList(body, func(category map[string]any) bool {
			return c.rules.Group(xtream.Text(category["category_name"]))
		})
	case "get_live_streams":
		var groups map[string]string
		if c.rules.FiltersGroups() {
			var err error
			if groups, err = c.liveGroupNames(ctx); err != nil {
				return nil, err
			}
		}
		return xtream.FilterList(body, func(stream map[string]any) bool {
			return c.keepStream(stream, groups)
		})
	}
	return body, nil
}

// guideChannels returns the guide ids of the live streams the filters keep.
func (c *Config) guideChannels(ctx *gin.Context) (map[string]bool, error) {
	var groups map[string]string
	if c.rules.FiltersGroups() {
		var err error
		if groups, err = c.liveGroupNames(ctx); err != nil {
			return nil, err
		}
	}
	streams, err := c.providerList(ctx, "get_live_streams")
	if err != nil {
		return nil, err
	}
	kept := map[string]bool{}
	for _, stream := range streams {
		if id := xtream.Text(stream["epg_channel_id"]); id != "" && c.keepStream(stream, groups) {
			kept[id] = true
		}
	}
	return kept, nil
}

// passOnGuide sends the provider's guide with only the channels kept.
func (c *Config) passOnGuide(ctx *gin.Context, resp *http.Response, kept map[string]bool) {
	c.dropLeakingHeaders(resp.Header)
	if resp.StatusCode >= http.StatusBadRequest {
		c.passOn(ctx, resp)
		return
	}

	// The guide is filtered as it streams: it is sent uncompressed, with a
	// length nobody knows yet.
	body := bufio.NewReader(resp.Body)
	if start, _ := body.Peek(2); bytes.Equal(start, []byte{0x1f, 0x8b}) {
		unzipped, err := gzip.NewReader(body)
		if err != nil {
			c.upstreamError(ctx, err)
			return
		}
		defer unzipped.Close() // nolint: errcheck
		body = bufio.NewReader(unzipped)
		resp.Header.Set("Content-Type", "application/xml")
	}
	for _, name := range []string{"Content-Length", "Content-Encoding", "Content-Range", "Accept-Ranges", "Etag", "Content-Md5"} {
		resp.Header.Del(name)
	}

	mergeHttpHeader(ctx.Writer.Header(), resp.Header)
	ctx.Status(resp.StatusCode)
	ctx.Writer.WriteHeaderNow()
	// The copy ends when the provider or the client stops: there is no one
	// left to tell.
	_ = xmltv.Filter(flushWriter{ctx.Writer}, body, func(channel string) bool { return kept[channel] })
}

// keepTrack tells whether a playlist track is kept.
func (c *Config) keepTrack(track m3u.Track) bool {
	return c.rules.Keep(track.Group(), track.Name())
}

// providerSecret is the account whose password must not reach a client: the
// Xtream account, else the one in the playlist's address.
func (c *Config) providerSecret() xtream.Account {
	if c.XtreamPassword != "" {
		return c.providerAccount()
	}
	if c.RemoteURL != nil {
		q := c.RemoteURL.Query()
		return xtream.Account{User: q.Get("username"), Password: q.Get("password")}
	}
	return xtream.Account{}
}

// hideProvider rewrites a playlist line that is not an address (the header,
// a track's #EXTINF and other directives) so that it does not give the
// provider away. In an Xtream playlist, the provider's API and streams named
// there (a guide in url-tvg, catch-up in catchup-source) become the proxy's.
// An address that would still give the provider's password away is removed:
// the attribute, or the whole directive. It returns "" for a line to drop.
func (c *Config) hideProvider(line string, fromXtream bool) string {
	if fromXtream {
		line = string(xtream.Sanitize([]byte(line), c.providerAccount(), c.proxyAccount()))
	}
	secret := c.providerSecret()
	if !xtream.Leaks(line, secret) {
		return line
	}
	if strings.HasPrefix(line, "#EXTM3U") || strings.HasPrefix(line, "#EXTINF") {
		return m3u.EditAttributes(line, func(_, value string) (string, bool) {
			return value, !strings.Contains(value, "://") || !xtream.Leaks(value, secret)
		})
	}
	if strings.Contains(line, "://") {
		return ""
	}
	return line
}

// hideProviderInTrack applies hideProvider to the lines of a track.
func (c *Config) hideProviderInTrack(track m3u.Track, fromXtream bool) m3u.Track {
	track.ExtInf = c.hideProvider(track.ExtInf, fromXtream)
	extra := make([]string, 0, len(track.Extra))
	for _, line := range track.Extra {
		if line = c.hideProvider(line, fromXtream); line != "" {
			extra = append(extra, line)
		}
	}
	track.Extra = extra
	return track
}
