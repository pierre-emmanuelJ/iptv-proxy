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
	"net/url"
	"path"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/pierre-emmanuelJ/iptv-proxy/pkg/m3u"
)

// With --proxy-logos, the logos and covers players load are served by the
// proxy too, as "/logo/<token>/<name>": a player behind the proxy reaches no
// other host, which matters behind a VPN. The token is the image's address,
// encrypted, as for HLS: the proxy only fetches addresses it gave out.

// logoAddress is the proxy's address of an image, or "" for what is not an
// http(s) address (a data: image is kept as it is).
func (c *Config) logoAddress(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return ""
	}
	name := path.Base(u.Path)
	if name == "" || name == "." || name == "/" {
		name = "logo"
	}
	return c.proxyBaseURL() + "/logo/" + c.tokens.seal(u.String()) + "/" + url.PathEscape(name)
}

// proxyLogo serves an image named by a token.
func (c *Config) proxyLogo(ctx *gin.Context) {
	address, err := c.tokens.open(ctx.Param("token"))
	if err != nil {
		ctx.AbortWithStatus(http.StatusNotFound)
		return
	}
	resp, err := c.upstream(ctx, c.apiClient, address)
	if err != nil {
		c.upstreamError(ctx, err)
		return
	}
	defer resp.Body.Close() // nolint: errcheck
	c.passOn(ctx, resp)
}

// logosInLine points the tvg-logo of a playlist line to the proxy.
func (c *Config) logosInLine(line string) string {
	if !c.ProxyLogos {
		return line
	}
	return m3u.EditAttributes(line, func(name, value string) (string, bool) {
		if !strings.EqualFold(name, "tvg-logo") {
			return value, true
		}
		if proxied := c.logoAddress(value); proxied != "" {
			return proxied, true
		}
		return value, true
	})
}
