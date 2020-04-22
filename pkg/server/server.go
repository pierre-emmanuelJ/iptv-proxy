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
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/jamesnetherton/m3u"
	"github.com/pierre-emmanuelJ/iptv-proxy/pkg/config"
	uuid "github.com/satori/go.uuid"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
)

var defaultProxyfiedM3UPath = "/tmp/" + uuid.NewV4().String() + ".iptv-proxy.m3u"

// Config represent the server configuration
type Config struct {
	*config.ProxyConfig

	// M3U service part
	playlist *m3u.Playlist
	// this variable is set only for m3u proxy endpoints
	track *m3u.Track
	// path to the proxyfied m3u file
	proxyfiedM3UPath string
}

// NewServer initialize a new server configuration
func NewServer(config *config.ProxyConfig) (*Config, error) {
	var p m3u.Playlist
	if config.RemoteURL.String() != "" {
		var err error
		p, err = m3u.Parse(config.RemoteURL.String())
		if err != nil {
			return nil, err
		}
	}

	return &Config{
		config,
		&p,
		nil,
		defaultProxyfiedM3UPath,
	}, nil
}

// Serve the iptv-proxy api
func (c *Config) Serve() error {
	c.playlistInitialization()

	router := gin.Default()
	router.Use(cors.Default())
	group := router.Group("/")
	c.routes(group)

	return router.Run(fmt.Sprintf(":%d", c.HostConfig.Port))
}

func (c *Config) playlistInitialization() error {
	if len(c.playlist.Tracks) == 0 {
		return nil
	}

	f, err := os.Create(c.proxyfiedM3UPath)
	if err != nil {
		return err
	}
	defer f.Close()

	return c.marshallInto(f, false)
}

// MarshallInto a *bufio.Writer a Playlist.
func (c *Config) marshallInto(into *os.File, xtream bool) error {
	into.WriteString("#EXTM3U\n")
	for _, track := range c.playlist.Tracks {
		into.WriteString("#EXTINF:")
		into.WriteString(fmt.Sprintf("%d ", track.Length))
		for i := range track.Tags {
			if i == len(track.Tags)-1 {
				into.WriteString(fmt.Sprintf("%s=%q", track.Tags[i].Name, track.Tags[i].Value))
				continue
			}
			into.WriteString(fmt.Sprintf("%s=%q ", track.Tags[i].Name, track.Tags[i].Value))
		}
		into.WriteString(", ")

		uri, err := c.replaceURL(track.URI, xtream)
		if err != nil {
			return err
		}

		into.WriteString(fmt.Sprintf("%s\n%s\n", track.Name, uri))
	}

	return into.Sync()
}

// ReplaceURL replace original playlist url by proxy url
func (c *Config) replaceURL(uri string, xtream bool) (string, error) {
	oriURL, err := url.Parse(uri)
	if err != nil {
		return "", err
	}

	protocol := "http"
	if c.HTTPS {
		protocol = "https"
	}

	customEnd := c.CustomEndpoint
	if customEnd != "" {
		customEnd = fmt.Sprintf("/%s", customEnd)
	} else {
		// use prefix path before user/pass
		if strings.Index(oriURL.EscapedPath(), c.XtreamUser) > 1 {
			customEnd = "/" + oriURL.EscapedPath()[1:strings.Index(oriURL.EscapedPath(), c.XtreamUser)-1]
		}
	}

	path := oriURL.EscapedPath()
	if xtream {
		path = fmt.Sprintf("/%s", filepath.Base(path))
	}

	newURI := fmt.Sprintf(
		"%s://%s:%d%s/%s/%s%s",
		protocol,
		c.HostConfig.Hostname,
		c.HostConfig.Port,
		customEnd,
		url.QueryEscape(c.User),
		url.QueryEscape(c.Password),
		path,
	)

	newURL, err := url.Parse(newURI)
	if err != nil {
		return "", err
	}

	return newURL.String(), nil
}
