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
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/pierre-emmanuelJ/iptv-proxy/pkg/m3u"
	"github.com/pierre-emmanuelJ/iptv-proxy/pkg/xtream"
)

// errNotAPlaylist is what a client gets when the provider answered something
// else than a playlist. The provider's own answer may name its credentials:
// it is not passed on.
var errNotAPlaylist = errors.New("the provider did not answer with a playlist")

// cachedPlaylist returns the playlist kept under key, building it again once
// it is older than the configured expiration.
func (c *Config) cachedPlaylist(key string, build func() (*m3u.Playlist, error)) ([]byte, error) {
	c.m3uCacheLock.Lock()
	defer c.m3uCacheLock.Unlock()

	if cached, ok := c.m3uCache[key]; ok && time.Since(cached.at) < time.Duration(c.M3UCacheExpiration)*time.Hour {
		return cached.body, nil
	}

	playlist, err := build()
	if err != nil {
		if cached, ok := c.m3uCache[key]; ok {
			// The last playlist plays; an error does not.
			log.Printf("[iptv-proxy] playlist: the provider failed (%v), serving the one from %s ago", err, time.Since(cached.at).Round(time.Second))
			return cached.body, nil
		}
		return nil, err
	}
	_, body := c.proxify(playlist, true)
	c.m3uCache[key] = cachedM3U{body: body, at: time.Now()}

	return body, nil
}

// providerGet asks the provider's API and returns its whole answer.
func (c *Config) providerGet(ctx *gin.Context, endpoint string, params url.Values) (int, http.Header, []byte, error) {
	resp, err := c.upstream(ctx, c.apiClient, c.providerAccount().APIURL(endpoint, params), false)
	if err != nil {
		return 0, nil, nil, err
	}
	defer resp.Body.Close() // nolint: errcheck

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, nil, nil, withoutURL(err)
	}
	return resp.StatusCode, resp.Header, body, nil
}

// providerList asks the provider's API for a list (categories, streams).
func (c *Config) providerList(ctx *gin.Context, action string) ([]map[string]any, error) {
	status, _, body, err := c.providerGet(ctx, "player_api.php", url.Values{"action": {action}})
	if err != nil {
		return nil, err
	}
	if status < 200 || status > 299 {
		return nil, fmt.Errorf("%s: the provider answered HTTP %d", action, status)
	}
	return xtream.DecodeList(body)
}

// xtreamGenerateM3u builds the live playlist from the provider's API, for
// providers whose get.php is disabled.
func (c *Config) xtreamGenerateM3u(ctx *gin.Context, extension string) (*m3u.Playlist, error) {
	// this is specific to xtream API,
	// prefix with "live" if there is an extension.
	var prefix string
	if extension != "" {
		extension = "." + extension
		prefix = "live/"
	}

	playlist := &m3u.Playlist{}
	err := c.appendCatalogue(ctx, playlist, "get_live_categories", "get_live_streams", func(stream map[string]any, group string) m3u.Track {
		name := xtream.Text(stream["name"])
		return m3u.Track{
			ExtInf: m3u.ExtInfLine(
				name,
				[2]string{"tvg-id", xtream.Text(stream["epg_channel_id"])},
				[2]string{"tvg-name", name},
				[2]string{"tvg-logo", xtream.Text(stream["stream_icon"])},
				[2]string{"group-title", group},
			),
			URI: c.providerAccount().StreamURL(prefix, xtream.Text(stream["stream_id"])+extension),
		}
	})
	if err != nil {
		return nil, err
	}

	if c.XtreamApiGetMovies {
		err := c.appendCatalogue(ctx, playlist, "get_vod_categories", "get_vod_streams", func(movie map[string]any, group string) m3u.Track {
			name := xtream.Text(movie["name"])
			// A movie is a file: it keeps its own format, whatever format
			// the live streams are asked in.
			format := xtream.Text(movie["container_extension"])
			if format == "" {
				format = "mp4"
			}
			return m3u.Track{
				ExtInf: m3u.ExtInfLine(
					name,
					[2]string{"tvg-name", name},
					[2]string{"tvg-logo", xtream.Text(movie["stream_icon"])},
					[2]string{"group-title", group},
				),
				URI: c.providerAccount().StreamURL("movie/", xtream.Text(movie["stream_id"])+"."+format),
			}
		})
		if err != nil {
			return nil, err
		}
	}

	return playlist, nil
}

// appendCatalogue adds to a playlist the entries of one of the provider's
// catalogues (live streams, movies), grouped by category in the provider's
// order.
func (c *Config) appendCatalogue(ctx *gin.Context, playlist *m3u.Playlist, categoriesAction, entriesAction string, track func(entry map[string]any, group string) m3u.Track) error {
	categories, err := c.providerList(ctx, categoriesAction)
	if err != nil {
		return err
	}
	entries, err := c.providerList(ctx, entriesAction)
	if err != nil {
		return err
	}

	byCategory := map[string][]map[string]any{}
	for _, entry := range entries {
		id := xtream.Text(entry["category_id"])
		byCategory[id] = append(byCategory[id], entry)
	}

	add := func(group string, entries []map[string]any) {
		for _, entry := range entries {
			playlist.Tracks = append(playlist.Tracks, track(entry, group))
		}
	}
	for _, category := range categories {
		id := xtream.Text(category["category_id"])
		add(xtream.Text(category["category_name"]), byCategory[id])
		delete(byCategory, id)
	}
	// Entries of a category the provider does not list are not lost.
	for _, entry := range entries {
		id := xtream.Text(entry["category_id"])
		if rest, ok := byCategory[id]; ok {
			add("", rest)
			delete(byCategory, id)
		}
	}

	return nil
}

// xtreamGetAuto serves the playlist the proxy was started with: the
// provider's get.php with the parameters of that address.
func (c *Config) xtreamGetAuto(ctx *gin.Context) {
	params := url.Values{}
	for k, v := range ctx.Request.Form {
		params[k] = v
	}
	for k, v := range c.RemoteURL.Query() {
		params.Add(k, strings.Join(v, ","))
	}

	c.xtreamServeGet(ctx, params)
}

func (c *Config) xtreamGet(ctx *gin.Context) {
	c.xtreamServeGet(ctx, ctx.Request.Form)
}

func (c *Config) xtreamServeGet(ctx *gin.Context, params url.Values) {
	// The same parameters give the same playlist: the cache key is the
	// provider address they build.
	key := c.providerAccount().APIURL("get.php", params)

	body, err := c.cachedPlaylist(key, func() (*m3u.Playlist, error) {
		log.Printf("[iptv-proxy] %v | %s | xtream cache m3u file\n", time.Now().Format("2006/01/02 - 15:04:05"), ctx.ClientIP())
		status, _, answer, err := c.providerGet(ctx, "get.php", params)
		if err != nil {
			return nil, err
		}
		if status < 200 || status > 299 {
			return nil, fmt.Errorf("get.php: the provider answered HTTP %d", status)
		}
		playlist, err := m3u.Parse(strings.NewReader(string(answer)))
		if err != nil {
			return nil, errNotAPlaylist
		}
		return playlist, nil
	})
	if err != nil {
		c.upstreamError(ctx, err)
		return
	}

	c.serveM3U(ctx, body)
}

func (c *Config) xtreamApiGet(ctx *gin.Context) {
	extension := ctx.Query("output")

	body, err := c.cachedPlaylist("apiget"+extension, func() (*m3u.Playlist, error) {
		log.Printf("[iptv-proxy] %v | %s | xtream cache API m3u file\n", time.Now().Format("2006/01/02 - 15:04:05"), ctx.ClientIP())
		return c.xtreamGenerateM3u(ctx, extension)
	})
	if err != nil {
		c.upstreamError(ctx, err)
		return
	}

	c.serveM3U(ctx, body)
}

// xtreamPlayerAPI answers the Xtream client API with the provider's own
// answer. Only what names the provider is rewritten: the account and server
// of the login answer, and stream addresses holding its credentials.
func (c *Config) xtreamPlayerAPI(ctx *gin.Context) {
	params := ctx.Request.Form
	action := params.Get("action")

	key := answerKey("player_api.php", params)
	status, header, body, err := c.providerGet(ctx, "player_api.php", params)
	// A maintenance page or a cut list is no answer either.
	invalid := err == nil && status == http.StatusOK && !json.Valid(body)
	if (invalid || providerFailed(status, err)) && c.lastGood.serve(ctx, key, failure(status, err, invalid)) {
		return
	}
	if err != nil {
		c.upstreamError(ctx, err)
		return
	}

	log.Printf("[iptv-proxy] %v | %s |Action\t%s\n", time.Now().Format("2006/01/02 - 15:04:05"), ctx.ClientIP(), action)

	if action == "" {
		protocol := "http"
		if c.HTTPS {
			protocol = "https"
		}
		body, _ = xtream.RewriteLogin(body, xtream.ProxyInfo{
			Account:  c.proxyAccount(),
			Hostname: c.HostConfig.Hostname,
			Port:     c.AdvertisedPort,
			Protocol: protocol,
		})
	}
	c.dropLeakingHeaders(header)
	if status >= http.StatusBadRequest {
		// Not data: an error page, which may repeat the address asked.
		c.errorPage(ctx, status, header, body)
		return
	}
	body = xtream.Sanitize(body, c.providerAccount(), c.proxyAccount())
	if body, err = c.filterAPIList(ctx, action, body); err != nil {
		c.upstreamError(ctx, err)
		return
	}

	contentType := header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/json"
	}
	if status == http.StatusOK && !invalid {
		c.lastGood.keep(key, contentType, body)
	}
	ctx.Data(status, contentType, body)
}

// xtreamXMLTV passes the provider's guide on as it comes: it can weigh
// hundreds of megabytes. With filters, the channels left out are taken out
// on the way.
func (c *Config) xtreamXMLTV(ctx *gin.Context) {
	var channels func() (map[string]bool, error)
	if c.rules.Active() {
		channels = func() (map[string]bool, error) { return c.guideChannels(ctx) }
	}
	c.guide(ctx, c.providerAccount().APIURL("xmltv.php", ctx.Request.Form), channels)
}

// xtreamProviderStream serves "<prefix><user>/<password>/<rest>" of the
// provider. Live television is shared between its clients; a movie or an
// episode is a file each client reads on its own.
func (c *Config) xtreamProviderStream(ctx *gin.Context, prefix, rest string, live bool) {
	rpURL, err := url.Parse(c.providerAccount().StreamURL(prefix, rest))
	if err != nil {
		ctx.AbortWithError(http.StatusInternalServerError, errors.New("invalid stream address")) // nolint: errcheck
		return
	}
	rpURL.RawQuery = ctx.Request.URL.RawQuery

	if live {
		c.streamLive(ctx, rpURL)
		return
	}
	c.stream(ctx, rpURL)
}

func (c *Config) xtreamStreamHandler(ctx *gin.Context) {
	c.xtreamProviderStream(ctx, "", url.PathEscape(ctx.Param("id")), true)
}

func (c *Config) xtreamStreamLive(ctx *gin.Context) {
	c.xtreamProviderStream(ctx, "live/", url.PathEscape(ctx.Param("id")), true)
}

func (c *Config) xtreamStreamMovie(ctx *gin.Context) {
	c.xtreamProviderStream(ctx, "movie/", url.PathEscape(ctx.Param("id")), false)
}

func (c *Config) xtreamStreamSeries(ctx *gin.Context) {
	c.xtreamProviderStream(ctx, "series/", url.PathEscape(ctx.Param("id")), false)
}

func (c *Config) xtreamStreamTimeshift(ctx *gin.Context) {
	rest := strings.Join([]string{
		url.PathEscape(ctx.Param("duration")),
		url.PathEscape(ctx.Param("start")),
		url.PathEscape(ctx.Param("id")),
	}, "/")
	rpURL, err := url.Parse(c.providerAccount().StreamURL("timeshift/", rest))
	if err != nil {
		ctx.AbortWithError(http.StatusInternalServerError, errors.New("invalid stream address")) // nolint: errcheck
		return
	}

	c.stream(ctx, rpURL)
}

func (c *Config) xtreamStreamPlay(ctx *gin.Context) {
	rpURL, err := url.Parse(fmt.Sprintf(
		"%s/play/%s/%s",
		strings.TrimRight(c.XtreamBaseURL, "/"),
		url.PathEscape(ctx.Param("token")),
		url.PathEscape(ctx.Param("type")),
	))
	if err != nil {
		ctx.AbortWithError(http.StatusInternalServerError, errors.New("invalid stream address")) // nolint: errcheck
		return
	}

	c.stream(ctx, rpURL)
}
