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
	"net/http"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/pierre-emmanuelJ/iptv-proxy/pkg/filter"
	"github.com/pierre-emmanuelJ/iptv-proxy/pkg/m3u"
	"github.com/pierre-emmanuelJ/iptv-proxy/pkg/xtream"
)

// Filtering keeps the channels a client sees: in playlists, in the live
// lists of the Xtream API and in the guide. Movies and series are not
// filtered. Without filters, the provider's answers are passed on as they
// are.

// liveGroups are the names of the provider's live categories, by id, for
// each provider account. The stream list only gives a category's id, and the
// filters are on its name.
type liveGroups struct {
	mu       sync.Mutex
	accounts map[string]groupNames
}

type groupNames struct {
	names map[string]string
	at    time.Time
}

// liveGroupNames returns the live categories of the request's provider
// account, asked again once they are older than the playlist cache.
func (c *Config) liveGroupNames(ctx *gin.Context) (map[string]string, error) {
	c.groups.mu.Lock()
	defer c.groups.mu.Unlock()

	account := c.accountOf(ctx).User
	if known := c.groups.accounts[account]; known.names != nil && time.Since(known.at) < time.Duration(c.M3UCacheExpiration)*time.Hour {
		return known.names, nil
	}
	categories, err := c.providerList(ctx, "get_live_categories")
	if err != nil {
		return nil, err
	}
	names := make(map[string]string, len(categories))
	for _, category := range categories {
		names[xtream.Text(category["category_id"])] = xtream.Text(category["category_name"])
	}
	if c.groups.accounts == nil {
		c.groups.accounts = map[string]groupNames{}
	}
	c.groups.accounts[account] = groupNames{names: names, at: time.Now()}
	return names, nil
}

// keepStream tells whether rules keep a live stream of the API. A stream may
// name several categories; it is kept when one of them is.
func keepStream(stream map[string]any, groups map[string]string, rules *filter.Rules) bool {
	name := xtream.Text(stream["name"])
	ids := []string{xtream.Text(stream["category_id"])}
	if more, ok := stream["category_ids"].([]any); ok {
		for _, id := range more {
			ids = append(ids, xtream.Text(id))
		}
	}
	for _, id := range ids {
		if rules.Keep(groups[id], name) {
			return true
		}
	}
	return false
}

// filterAPIList drops, from an answer of the API, the live categories and
// streams the filters leave out. Other answers are returned as they are.
func (c *Config) filterAPIList(ctx *gin.Context, action string, body []byte) ([]byte, error) {
	rules := userOf(ctx).rules
	if !rules.Active() {
		return body, nil
	}
	switch action {
	case "get_live_categories":
		return xtream.FilterList(body, func(category map[string]any) bool {
			return rules.Group(xtream.Text(category["category_name"]))
		})
	case "get_live_streams":
		var groups map[string]string
		if rules.FiltersGroups() {
			var err error
			if groups, err = c.liveGroupNames(ctx); err != nil {
				return nil, err
			}
		}
		return xtream.FilterList(body, func(stream map[string]any) bool {
			return keepStream(stream, groups, rules)
		})
	}
	return body, nil
}

// guideChannels returns the guide ids of the live streams rules keep.
func (c *Config) guideChannels(ctx *gin.Context, rules *filter.Rules) (map[string]bool, error) {
	var groups map[string]string
	if rules.FiltersGroups() {
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
		if id := xtream.Text(stream["epg_channel_id"]); id != "" && keepStream(stream, groups, rules) {
			kept[id] = true
		}
	}
	return kept, nil
}

// keepTrack tells whether rules keep a playlist track.
func keepTrack(track m3u.Track, rules *filter.Rules) bool {
	return rules.Keep(track.Group(), track.Name())
}

// providerSecret is the account whose password must not reach a client: the
// Xtream account (see hiddenAccount), else the one in the playlist's address.
func (c *Config) providerSecret() xtream.Account {
	if c.XtreamPassword != "" || c.XtreamPassthrough {
		return c.hiddenAccount()
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
func (c *Config) hideProvider(line string, fromXtream bool, u *proxyUser) string {
	if fromXtream {
		line = string(xtream.Sanitize([]byte(line), u.provider, c.proxyAccount(u)))
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
func (c *Config) hideProviderInTrack(track m3u.Track, fromXtream bool, u *proxyUser) m3u.Track {
	track.ExtInf = c.logosInLine(c.hideProvider(track.ExtInf, fromXtream, u))
	extra := make([]string, 0, len(track.Extra))
	for _, line := range track.Extra {
		if line = c.hideProvider(line, fromXtream, u); line != "" {
			extra = append(extra, line)
		}
	}
	track.Extra = extra
	return track
}

// liveAccess are, for each set of filters, the live streams they keep: a
// user with filters only watches those, whatever the address asked.
type liveAccess struct {
	mu   sync.Mutex
	sets map[string]accessSet
}

type accessSet struct {
	ids map[string]bool
	at  time.Time
}

// mayWatch tells whether a user may watch a live stream of the provider.
func (c *Config) mayWatch(ctx *gin.Context, u *proxyUser, id string) (bool, error) {
	if !u.rules.Active() {
		return true, nil
	}
	id = strings.TrimSuffix(id, path.Ext(id))
	key := u.rules.String()
	if c.XtreamPassthrough {
		key = u.name.String() + "\x00" + key // accounts may have different channels
	}

	c.liveAccess.mu.Lock()
	defer c.liveAccess.mu.Unlock()
	set := c.liveAccess.sets[key]
	age := time.Since(set.at)
	fresh := set.ids != nil && age < time.Duration(c.M3UCacheExpiration)*time.Hour
	// A stream not in a recent list stays refused; one not in an older
	// list may be new: the list is read again.
	if fresh && (set.ids[id] || age < lineupStale) {
		return set.ids[id], nil
	}

	var groups map[string]string
	if u.rules.FiltersGroups() {
		var err error
		if groups, err = c.liveGroupNames(ctx); err != nil {
			return set.ids[id], keepOld(set, err)
		}
	}
	streams, err := c.providerList(ctx, "get_live_streams")
	if err != nil {
		return set.ids[id], keepOld(set, err)
	}
	ids := map[string]bool{}
	for _, stream := range streams {
		if keepStream(stream, groups, u.rules) {
			ids[xtream.Text(stream["stream_id"])] = true
		}
	}
	if c.liveAccess.sets == nil {
		c.liveAccess.sets = map[string]accessSet{}
	}
	c.liveAccess.sets[key] = accessSet{ids: ids, at: time.Now()}
	return ids[id], nil
}

// keepOld says whether a list that could not be read again is an error: not
// when there is a previous one to go by.
func keepOld(set accessSet, err error) error {
	if set.ids != nil {
		return nil
	}
	return err
}

// allowLive answers the request itself and returns false when its user may
// not watch the live stream id.
func (c *Config) allowLive(ctx *gin.Context, id string) bool {
	ok, err := c.mayWatch(ctx, userOf(ctx), id)
	switch {
	case err != nil:
		c.upstreamError(ctx, err)
		return false
	case !ok:
		ctx.AbortWithStatus(http.StatusNotFound)
		return false
	}
	return true
}
