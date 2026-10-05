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
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log"
	"net/http"
	"net/url"
	"path"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/pierre-emmanuelJ/iptv-proxy/pkg/m3u"
)

// retryFailedReload is how long a playlist that could not be read again is
// served before trying again.
var retryFailedReload = 5 * time.Minute

// m3uPlaylist is the provider's M3U playlist as the proxy serves it. It is
// read at startup, then again when a client asks for it and it is older than
// the playlist cache (--m3u-cache-expiration). A playlist that cannot be read
// again is kept.
type m3uPlaylist struct {
	current atomic.Pointer[m3uState]
	reload  sync.Mutex
}

type m3uState struct {
	// audiences are what the users with the same filters may play, by the
	// description of their filters.
	audiences map[string]*m3uAudience
	// bodies are the playlists users get, by name: each holds its user's
	// credentials.
	bodies map[string][]byte
	// guide is the address of the guide, if any.
	guide string
	// live are the channels of the HDHomeRun tuner, when there is one.
	live []tunerChannel
	// next is when the playlist is read again.
	next time.Time
}

// m3uAudience is what users with the same filters may play.
type m3uAudience struct {
	// tracks are the provider's addresses, by track key. They are kept as
	// text: a playlist may hold a million tracks.
	tracks map[string]string
	// guideIDs are the guide ids of the tracks, when filters apply.
	guideIDs map[string]bool
}

// trackKey names a track in the proxy's addresses. It is derived from the
// provider's address, so that a track keeps its address on the proxy when
// the playlist is read again or the proxy restarts, wherever it moves in the
// playlist.
func trackKey(address string) string {
	sum := sha256.Sum256([]byte(address))
	return hex.EncodeToString(sum[:8])
}

// loadM3U reads the provider's playlist and makes it the one served.
func (c *Config) loadM3U(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, playlistFetchTimeout)
	defer cancel()
	playlist, err := c.loadPlaylist(ctx, c.RemoteURL.String(), c.upstreamUserAgent(""))
	if err != nil {
		return err
	}

	state := &m3uState{
		audiences: map[string]*m3uAudience{},
		bodies:    map[string][]byte{},
		guide:     c.m3uGuide(playlist.Header),
		next:      time.Now().Add(time.Duration(c.M3UCacheExpiration) * time.Hour),
	}
	for _, u := range c.users {
		kept, body := c.proxify(playlist, false, u)
		state.bodies[u.name.String()] = body

		audience := u.rules.String()
		if state.audiences[audience] != nil {
			continue
		}
		a := &m3uAudience{tracks: make(map[string]string, len(kept.Tracks)), guideIDs: map[string]bool{}}
		for _, track := range kept.Tracks {
			a.tracks[trackKey(track.URI)] = track.URI
			if !u.rules.Active() {
				continue // the guide is only filtered with the playlist
			}
			if id, ok := m3u.Attribute(track.ExtInf, "tvg-id"); ok && id != "" {
				a.guideIDs[id] = true
			}
		}
		state.audiences[audience] = a
	}
	if c.HDHomeRunPort != 0 {
		// the tuner has no user: the proxy's filters and its own apply
		var live []m3u.Track
		for _, track := range playlist.Tracks {
			if !c.tunerRules.Active() || keepTrack(track, c.tunerRules) {
				live = append(live, track)
			}
		}
		state.live = m3uTunerChannels(live)
	}
	c.m3u.current.Store(state)

	// What reading took (the provider's playlist, the copies made on the
	// way) goes back to the system now, not at some later collection.
	debug.FreeOSMemory()
	return nil
}

// m3uCurrent returns the playlist to serve, read again first when it is due.
func (c *Config) m3uCurrent(ctx context.Context) *m3uState {
	state := c.m3u.current.Load()
	if time.Now().Before(state.next) {
		return state
	}

	c.m3u.reload.Lock()
	defer c.m3u.reload.Unlock()
	if state = c.m3u.current.Load(); time.Now().Before(state.next) {
		return state // read again by another client meanwhile
	}
	if err := c.loadM3U(context.WithoutCancel(ctx)); err != nil {
		log.Printf("[iptv-proxy] playlist: reading it again failed (%v), serving the previous one", err)
		retry := *state
		retry.next = time.Now().Add(retryFailedReload)
		c.m3u.current.Store(&retry)
		return &retry
	}
	return c.m3u.current.Load()
}

func (c *Config) getM3U(ctx *gin.Context) {
	c.serveM3U(ctx, c.m3uCurrent(ctx.Request.Context()).bodies[userOf(ctx).name.String()])
}

// audience is what the user of a request may play.
func (c *Config) audience(ctx *gin.Context) (*m3uState, *m3uAudience) {
	state := c.m3uCurrent(ctx.Request.Context())
	return state, state.audiences[userOf(ctx).rules.String()]
}

// m3uTrack serves a track of the playlist.
func (c *Config) m3uTrack(ctx *gin.Context) {
	_, audience := c.audience(ctx)
	raw, ok := audience.tracks[ctx.Param("track")]
	if !ok {
		ctx.AbortWithStatus(http.StatusNotFound)
		return
	}
	address, err := url.Parse(raw)
	if err != nil {
		ctx.AbortWithStatus(http.StatusNotFound) // proxify() only keeps valid addresses
		return
	}
	// A playlist does not say what a track is. A file is recognized by its
	// extension; anything else may be live television.
	if fileExtensions[strings.ToLower(path.Ext(address.Path))] {
		c.stream(ctx, address)
		return
	}
	c.streamLive(ctx, address)
}

// m3uGuide is the address of the guide of an M3U playlist: --xmltv-url, else
// the one its header names.
func (c *Config) m3uGuide(header string) string {
	if c.XMLTVURL != "" {
		return c.XMLTVURL
	}
	for _, name := range []string{"url-tvg", "x-tvg-url"} {
		if value, ok := m3u.Attribute(header, name); ok {
			// several guides may be listed: the first one is served
			if first := strings.TrimSpace(strings.Split(value, ",")[0]); first != "" {
				return first
			}
		}
	}
	return ""
}

// m3uHeader is the header of the playlist clients get: when there is a
// guide, it names the proxy's.
func (c *Config) m3uHeader(original, cleaned string, u *proxyUser) string {
	if c.m3uGuide(original) == "" {
		return cleaned
	}
	guide := c.proxyBaseURL() + "/xmltv.php?username=" + url.QueryEscape(u.name.String()) + "&password=" + url.QueryEscape(u.password.String())

	named := false
	header := m3u.EditAttributes(cleaned, func(name, value string) (string, bool) {
		if strings.EqualFold(name, "url-tvg") || strings.EqualFold(name, "x-tvg-url") {
			named = true
			return guide, true
		}
		return value, true
	})
	if !named {
		header += ` url-tvg="` + guide + `"`
	}
	return header
}

// m3uXMLTV serves the guide of the M3U playlist.
func (c *Config) m3uXMLTV(ctx *gin.Context) {
	state, audience := c.audience(ctx)
	if state.guide == "" {
		ctx.AbortWithStatus(http.StatusNotFound)
		return
	}
	u := userOf(ctx)
	var mapping func() (channelMap, error)
	if u.rules.Active() {
		mapping = func() (channelMap, error) { return keeping(audience.guideIDs), nil }
	}
	c.guide(ctx, state.guide, userAnswerKey(u, "guide", ctx.Request.Form), mapping, nil, c.guideIcons(false))
}
