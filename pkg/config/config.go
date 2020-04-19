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

package config

import (
	"net/url"
)

// HostConfiguration containt host infos
type HostConfiguration struct {
	Hostname string
	Port     int64
}

// ProxyConfig Contain original m3u playlist and HostConfiguration
type ProxyConfig struct {
	HostConfig         *HostConfiguration
	XtreamUser         string
	XtreamPassword     string
	XtreamBaseURL      string
	M3UCacheExpiration int
	M3UFileName        string
	CustomEndpoint     string
	RemoteURL          *url.URL
	HTTPS              bool
	User, Password     string
}
