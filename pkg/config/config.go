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

	"github.com/pierre-emmanuelJ/iptv-proxy/pkg/filter"
)

// CredentialString represents an iptv-proxy credential.
type CredentialString string

// PathEscape escapes the credential for an url path.
func (c CredentialString) PathEscape() string {
	return url.PathEscape(string(c))
}

// String returns the credential string.
func (c CredentialString) String() string {
	return string(c)
}

// HostConfiguration containt host infos
type HostConfiguration struct {
	Hostname string
	Port     int
}

// User is one of the users of the configuration file.
type User struct {
	Name, Password string
	// MaxConnections is how many streams the user may watch at once; 0 is
	// no limit.
	MaxConnections int
	// Filter keeps the channels the user sees, after the proxy's filters.
	Filter filter.Patterns
}

// Source is an Xtream provider account the proxy serves besides the one of
// the Xtream options.
type Source struct {
	// Name names the source in logs.
	Name                                      string
	XtreamBaseURL, XtreamUser, XtreamPassword string
	// MaxConnections is how many streams the proxy opens at once on the
	// account; 0 asks the provider.
	MaxConnections int
}

// ProxyConfig Contain original m3u playlist and HostConfiguration
type ProxyConfig struct {
	HostConfig           *HostConfiguration
	XtreamUser           CredentialString
	XtreamPassword       CredentialString
	XtreamBaseURL        string
	XtreamGenerateApiGet bool
	// XtreamApiGetMovies adds the provider's movies to the playlist generated
	// from its API. Off by default: a catalogue of tens of thousands of
	// movies makes a playlist some players cannot load.
	XtreamApiGetMovies bool
	M3UCacheExpiration int
	M3UFileName        string
	CustomEndpoint     string
	CustomId           string
	RemoteURL          *url.URL
	AdvertisedPort     int
	HTTPS              bool
	User, Password     CredentialString
	// UserAgent, when set, replaces the client's on every request to the
	// provider.
	UserAgent string
	// NoStreamSharing gives every client its own connection to the provider
	// for a live stream, instead of one connection per stream.
	NoStreamSharing bool
	// Filter keeps the channels clients see, by group and by name.
	Filter filter.Patterns
	// ListenAddress is the address the proxy listens on; empty means every
	// interface.
	ListenAddress string
	// XMLTVURL is the guide of an M3U playlist, when its header does not
	// name one or names another.
	XMLTVURL string
	// ProxyLogos serves the logos and covers of playlists and of the Xtream
	// API through the proxy, like the streams.
	ProxyLogos bool
	// HDHomeRunPort, when set, is the port of an HDHomeRun tuner serving the
	// live channels to media servers (Plex...). HDHomeRunTuners is the
	// number of tuners it announces; 0 asks the provider.
	HDHomeRunPort   int
	HDHomeRunTuners int
	// HDHomeRunFilter keeps the tuner's channels, after the proxy's filters.
	HDHomeRunFilter filter.Patterns
	// Users, when given, are the users of the proxy instead of the one of
	// User and Password.
	Users []User
	// MaxConnections is the limit of streams at once of the one user of User
	// and Password; 0 is no limit.
	MaxConnections int
	// XtreamPassthrough lets each client log in with its own account of the
	// Xtream provider, instead of the proxy's users and account.
	XtreamPassthrough bool
	// Sources are more Xtream accounts, served with the one of the Xtream
	// options as one catalogue.
	Sources []Source
}
