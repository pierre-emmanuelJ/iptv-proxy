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
	"fmt"

	"github.com/gin-gonic/gin"
)

func (c *Config) routes(r *gin.RouterGroup) {
	r = r.Group(c.CustomEndpoint)

	// What HLS playlists name, whatever the provider (see hlsAddress).
	r.GET("/hls/:token/:name", c.hlsStream)
	// Logos and covers, with --proxy-logos (see logoAddress).
	r.GET("/logo/:token/:name", c.proxyLogo)

	// Xtream service endpoints
	if c.XtreamBaseURL != "" {
		c.xtreamRoutes(r)
		if c.xtreamServesPlaylist() && !c.merged() {
			r.GET("/"+c.M3UFileName, c.authenticate, c.xtreamGetAuto)
			// XXX Private need: for external Android app
			r.POST("/"+c.M3UFileName, c.authenticate, c.xtreamGetAuto)

			return
		}
		if c.RemoteURL == nil || c.RemoteURL.String() == "" || c.merged() {
			// An Xtream account and no playlist of its own: the playlist is
			// the account's, under the usual name too.
			r.GET("/"+c.M3UFileName, c.authenticate, c.xtreamPlaylist())
			r.POST("/"+c.M3UFileName, c.authenticate, c.xtreamPlaylist())

			return
		}
	}

	c.m3uRoutes(r)
}

// xtreamPlaylist serves the account's playlist: the provider's get.php, or
// one generated from its API when asked so.
func (c *Config) xtreamPlaylist() gin.HandlerFunc {
	// Several sources have one playlist: the merged catalogue's.
	if c.XtreamGenerateApiGet || c.merged() {
		return c.xtreamApiGet
	}
	return c.xtreamGet
}

func (c *Config) xtreamRoutes(r *gin.RouterGroup) {
	r.GET("/get.php", c.authenticate, c.xtreamPlaylist())
	r.POST("/get.php", c.authenticate, c.xtreamPlaylist())
	r.GET("/apiget", c.authenticate, c.xtreamApiGet)
	r.GET("/player_api.php", c.authenticate, c.xtreamPlayerAPI)
	r.POST("/player_api.php", c.authenticate, c.xtreamPlayerAPI)
	r.GET("/xmltv.php", c.authenticate, c.xtreamXMLTV)
	r.GET("/play/:token/:type", c.xtreamStreamPlay)

	if c.XtreamPassthrough {
		// Streams name the provider account they are watched with.
		r.GET("/:username/:password/:id", c.passthroughStream, c.limit, c.xtreamStreamHandler)
		r.GET("/live/:username/:password/:id", c.passthroughStream, c.limit, c.xtreamStreamLive)
		r.GET("/timeshift/:username/:password/:duration/:start/:id", c.passthroughStream, c.limit, c.xtreamStreamTimeshift)
		r.GET("/movie/:username/:password/:id", c.passthroughStream, c.limit, c.xtreamStreamMovie)
		r.GET("/series/:username/:password/:id", c.passthroughStream, c.limit, c.xtreamStreamSeries)
		return
	}
	// Streams name their user in their path: each user has their own.
	for _, u := range c.users {
		r.GET(fmt.Sprintf("/%s/%s/:id", u.name, u.password), as(u), c.limit, c.xtreamStreamHandler)
		r.GET(fmt.Sprintf("/live/%s/%s/:id", u.name, u.password), as(u), c.limit, c.xtreamStreamLive)
		r.GET(fmt.Sprintf("/timeshift/%s/%s/:duration/:start/:id", u.name, u.password), as(u), c.limit, c.xtreamStreamTimeshift)
		r.GET(fmt.Sprintf("/movie/%s/%s/:id", u.name, u.password), as(u), c.limit, c.xtreamStreamMovie)
		r.GET(fmt.Sprintf("/series/%s/%s/:id", u.name, u.password), as(u), c.limit, c.xtreamStreamSeries)
	}
}

func (c *Config) m3uRoutes(r *gin.RouterGroup) {
	r.GET("/"+c.M3UFileName, c.authenticate, c.getM3U)
	// XXX Private need: for external Android app
	r.POST("/"+c.M3UFileName, c.authenticate, c.getM3U)
	r.GET("/xmltv.php", c.authenticate, c.m3uXMLTV)

	// The last element is the track's file name, there for players that
	// look at the extension.
	for _, u := range c.users {
		r.GET(fmt.Sprintf("/%s/%s/%s/:track/:name", c.endpointAntiColision, u.name, u.password), as(u), c.limit, c.m3uTrack)
	}
}
