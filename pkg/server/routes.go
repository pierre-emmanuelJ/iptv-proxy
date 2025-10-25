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
	"path"
	"strings"

	"github.com/gin-gonic/gin"
)

func (c *Config) routes(r *gin.RouterGroup) {
	r = r.Group(c.CustomEndpoint)

	//Xtream service endopoints
	if c.ProxyConfig.XtreamBaseURL != "" {
		c.xtreamRoutes(r)
		if strings.Contains(c.XtreamBaseURL, c.RemoteURL.Host) &&
			c.XtreamUser.String() == c.RemoteURL.Query().Get("username") &&
			c.XtreamPassword.String() == c.RemoteURL.Query().Get("password") {

			r.GET("/"+c.M3UFileName, c.authenticate, c.xtreamGetAuto)
			// XXX Private need: for external Android app
			r.POST("/"+c.M3UFileName, c.authenticate, c.xtreamGetAuto)

			return
		}
	}

	c.m3uRoutes(r)
}

func (c *Config) xtreamRoutes(r *gin.RouterGroup) {
	// Always use xtreamApiGet to include series and VOD
	getphp := gin.HandlerFunc(c.xtreamApiGet)
	r.GET("/get.php", c.authenticate, getphp)
	r.POST("/get.php", c.authenticate, getphp)
	r.GET("/apiget", c.authenticate, c.xtreamApiGet)
	r.GET("/player_api.php", c.authenticate, c.xtreamPlayerAPIGET)
	r.POST("/player_api.php", c.appAuthenticate, c.xtreamPlayerAPIPOST)
	r.GET("/xmltv.php", c.authenticate, c.xtreamXMLTV)
	r.GET(fmt.Sprintf("/%s/%s/:id", c.User, c.Password), c.xtreamStreamHandler)
	r.GET(fmt.Sprintf("/live/%s/%s/:id", c.User, c.Password), c.xtreamStreamLive)
	r.GET(fmt.Sprintf("/timeshift/%s/%s/:duration/:start/:id", c.User, c.Password), c.xtreamStreamTimeshift)
	r.GET(fmt.Sprintf("/movie/%s/%s/:id", c.User, c.Password), c.xtreamStreamMovie)
	r.GET(fmt.Sprintf("/series/%s/%s/:id", c.User, c.Password), c.xtreamStreamSeries)
	r.GET(fmt.Sprintf("/hlsr/:token/%s/%s/:channel/:hash/:chunk", c.User, c.Password), c.xtreamHlsrStream)
	r.GET("/hls/:token/:chunk", c.xtreamHlsStream)
	r.GET("/play/:token/:type", c.xtreamStreamPlay)
}

func (c *Config) m3uRoutes(r *gin.RouterGroup) {
	r.GET("/"+c.M3UFileName, c.authenticate, c.getM3U)
	// XXX Private need: for external Android app
	r.POST("/"+c.M3UFileName, c.authenticate, c.getM3U)

	for i, track := range c.playlist.Tracks {
		trackConfig := &Config{
			ProxyConfig: c.ProxyConfig,
			track:       &c.playlist.Tracks[i],
		}

		if strings.HasSuffix(track.URI, ".m3u8") {
			r.GET(fmt.Sprintf("/%s/%s/%s/%d/:id", c.endpointAntiColision, c.User, c.Password, i), trackConfig.m3u8ReverseProxy)
		} else {
			r.GET(fmt.Sprintf("/%s/%s/%s/%d/%s", c.endpointAntiColision, c.User, c.Password, i, path.Base(track.URI)), trackConfig.reverseProxy)
		}
	}
}
