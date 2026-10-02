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
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"

	"github.com/pierre-emmanuelJ/iptv-proxy/pkg/config"
	"github.com/pierre-emmanuelJ/iptv-proxy/pkg/m3u"
	"github.com/pierre-emmanuelJ/iptv-proxy/pkg/xtream"
)

const (
	// defaultUserAgent is used when the proxy talks to a provider on its own
	// (no client request to take one from). Providers commonly refuse Go's.
	defaultUserAgent = "VLC/3.0.20 LibVLC/3.0.20"

	playlistFetchTimeout = 2 * time.Minute
	connectTimeout       = 15 * time.Second
)

// How long a provider has to start answering. A stream starts at once or
// not at all; a whole playlist or guide is generated on request, which can
// take minutes with a large catalogue. Once it answers, a response lasts as
// long as it has to.
var (
	streamHeaderTimeout = 30 * time.Second
	apiHeaderTimeout    = 5 * time.Minute
)

// Config represent the server configuration
type Config struct {
	*config.ProxyConfig

	// M3U service part: the provider's playlist (tracks the proxy can
	// serve), and the same playlist pointing at the proxy.
	playlist     *m3u.Playlist
	proxyfiedM3U []byte

	endpointAntiColision string

	// client is for streams, apiClient for the provider's API, playlists
	// and guide. Both follow redirects.
	client    *http.Client
	apiClient *http.Client

	// tokens name the addresses found in HLS playlists.
	tokens *addressTokens

	m3uCacheLock sync.Mutex
	m3uCache     map[string]cachedM3U
}

type cachedM3U struct {
	body []byte
	at   time.Time
}

// NewServer initialize a new server configuration
func NewServer(config *config.ProxyConfig) (*Config, error) {
	newTransport := func(headerTimeout time.Duration) *http.Transport {
		return &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			DialContext:           (&net.Dialer{Timeout: connectTimeout, KeepAlive: 30 * time.Second}).DialContext,
			TLSHandshakeTimeout:   connectTimeout,
			ResponseHeaderTimeout: headerTimeout,
			MaxIdleConnsPerHost:   16,
			IdleConnTimeout:       90 * time.Second,
			// Media is passed on as the provider sends it.
			DisableCompression: true,
		}
	}
	transport := newTransport(streamHeaderTimeout)

	c := &Config{
		ProxyConfig:          config,
		playlist:             &m3u.Playlist{},
		endpointAntiColision: strings.Trim(config.CustomId, "/"),
		client:               &http.Client{Transport: transport},
		apiClient:            &http.Client{Transport: newTransport(apiHeaderTimeout)},
		m3uCache:             map[string]cachedM3U{},
	}
	remote := ""
	if config.RemoteURL != nil {
		remote = config.RemoteURL.String()
	}
	tokens, err := newAddressTokens(config.User.String(), config.Password.String(), config.XtreamUser.String(), config.XtreamPassword.String(), config.XtreamBaseURL, remote)
	if err != nil {
		return nil, err
	}
	c.tokens = tokens

	if c.endpointAntiColision == "" {
		id := make([]byte, 4)
		if _, err := rand.Read(id); err != nil {
			return nil, err
		}
		c.endpointAntiColision = hex.EncodeToString(id)
	}

	// With an Xtream provider the playlist is asked for when a client wants
	// it: nothing to download before starting.
	if c.RemoteURL != nil && c.RemoteURL.String() != "" && !c.xtreamServesPlaylist() {
		ctx, cancel := context.WithTimeout(context.Background(), playlistFetchTimeout)
		defer cancel()
		playlist, err := c.loadPlaylist(ctx, c.RemoteURL.String(), c.upstreamUserAgent(""))
		if err != nil {
			return nil, err
		}
		c.playlist, c.proxyfiedM3U = c.proxify(playlist, false)
	}

	return c, nil
}

// Handler is the proxy's HTTP API.
func (c *Config) Handler() http.Handler {
	router := gin.New()
	router.Use(gin.LoggerWithFormatter(c.accessLog), gin.Recovery(), cors.Default())
	c.routes(router.Group("/"))
	return router
}

// accessLog writes a request the way gin does, with the proxy's credentials
// masked: they sit in the query of API requests and in the path of streams,
// and logs get shared when asking for help.
func (c *Config) accessLog(p gin.LogFormatterParams) string {
	return fmt.Sprintf("[GIN] %s | %3d | %13v | %15s | %-7s %q\n%s",
		p.TimeStamp.Format("2006/01/02 - 15:04:05"),
		p.StatusCode,
		p.Latency.Round(time.Microsecond),
		p.ClientIP,
		p.Method,
		c.maskCredentials(p.Path),
		p.ErrorMessage,
	)
}

// maskCredentials hides the proxy's user and password in a request address.
func (c *Config) maskCredentials(address string) string {
	path, query, hasQuery := strings.Cut(address, "?")

	segments := strings.Split(path, "/")
	for i, segment := range segments {
		for _, secret := range []config.CredentialString{c.User, c.Password} {
			if secret != "" && (segment == secret.String() || segment == secret.PathEscape()) {
				segments[i] = "***"
			}
		}
	}
	path = strings.Join(segments, "/")
	if !hasQuery {
		return path
	}

	params := strings.Split(query, "&")
	for i, param := range params {
		if name, _, hasValue := strings.Cut(param, "="); hasValue && (name == "username" || name == "password") {
			params[i] = name + "=***"
		}
	}
	return path + "?" + strings.Join(params, "&")
}

// Serve the iptv-proxy api
func (c *Config) Serve() error {
	srv := &http.Server{
		Addr:              fmt.Sprintf(":%d", c.HostConfig.Port),
		Handler:           c.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	return srv.ListenAndServe()
}

// xtreamServesPlaylist tells whether the playlist address given to the proxy
// is the get.php of its Xtream provider: the playlist is then one more Xtream
// endpoint.
func (c *Config) xtreamServesPlaylist() bool {
	if c.XtreamBaseURL == "" || c.RemoteURL == nil || c.RemoteURL.Host == "" {
		return false
	}
	return strings.Contains(c.XtreamBaseURL, c.RemoteURL.Host) &&
		c.XtreamUser.String() == c.RemoteURL.Query().Get("username") &&
		c.XtreamPassword.String() == c.RemoteURL.Query().Get("password")
}

// upstreamUserAgent is the User-Agent sent to the provider: the configured
// one, else the client's, else a player's.
func (c *Config) upstreamUserAgent(client string) string {
	switch {
	case c.UserAgent != "":
		return c.UserAgent
	case client != "":
		return client
	default:
		return defaultUserAgent
	}
}

// loadPlaylist reads a playlist from an http(s) address or a local file.
func (c *Config) loadPlaylist(ctx context.Context, source, userAgent string) (*m3u.Playlist, error) {
	if !strings.HasPrefix(source, "http://") && !strings.HasPrefix(source, "https://") {
		f, err := os.Open(source)
		if err != nil {
			return nil, fmt.Errorf("unable to open playlist file: %w", err)
		}
		defer f.Close() // nolint: errcheck
		return m3u.Parse(f)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, source, nil)
	if err != nil {
		return nil, errors.New("invalid playlist address")
	}
	req.Header.Set("User-Agent", userAgent)

	resp, err := c.apiClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("unable to get the playlist: %w", withoutURL(err))
	}
	defer resp.Body.Close() // nolint: errcheck

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("unable to get the playlist: the provider answered %s", resp.Status)
	}
	return m3u.Parse(resp.Body)
}

// proxify returns the tracks of a playlist the proxy can serve, and the
// playlist as clients get it: the same lines, each address replaced by the
// proxy's. A track with an unusable address is dropped from both.
func (c *Config) proxify(playlist *m3u.Playlist, xtream bool) (*m3u.Playlist, []byte) {
	kept := &m3u.Playlist{Header: playlist.Header, Tracks: make([]m3u.Track, 0, len(playlist.Tracks))}
	out := &m3u.Playlist{Header: playlist.Header, Tracks: make([]m3u.Track, 0, len(playlist.Tracks))}

	for _, track := range playlist.Tracks {
		uri, err := c.replaceURL(track.URI, len(kept.Tracks), xtream)
		if err != nil {
			log.Printf("[iptv-proxy] ERROR: track %q dropped: invalid address", track.Name())
			continue
		}
		kept.Tracks = append(kept.Tracks, track)
		proxied := track
		proxied.URI = uri
		out.Tracks = append(out.Tracks, proxied)
	}

	var buf bytes.Buffer
	_, _ = out.WriteTo(&buf) // a bytes.Buffer does not fail
	return kept, buf.Bytes()
}

// proxyBaseURL is the address clients reach the proxy at.
func (c *Config) proxyBaseURL() string {
	protocol := "http"
	if c.HTTPS {
		protocol = "https"
	}

	customEnd := strings.Trim(c.CustomEndpoint, "/")
	if customEnd != "" {
		customEnd = "/" + customEnd
	}

	return fmt.Sprintf("%s://%s:%d%s", protocol, c.HostConfig.Hostname, c.AdvertisedPort, customEnd)
}

func (c *Config) providerAccount() xtream.Account {
	return xtream.Account{BaseURL: c.XtreamBaseURL, User: c.XtreamUser.String(), Password: c.XtreamPassword.String()}
}

func (c *Config) proxyAccount() xtream.Account {
	return xtream.Account{BaseURL: c.proxyBaseURL(), User: c.User.String(), Password: c.Password.String()}
}

// ReplaceURL replace original playlist url by proxy url
func (c *Config) replaceURL(uri string, trackIndex int, xtream bool) (string, error) {
	oriURL, err := url.Parse(uri)
	if err != nil {
		return "", err
	}

	uriPath := oriURL.EscapedPath()
	if xtream {
		uriPath = strings.Replace(
			uriPath,
			"/"+c.XtreamUser.PathEscape()+"/"+c.XtreamPassword.PathEscape()+"/",
			"/"+c.User.PathEscape()+"/"+c.Password.PathEscape()+"/",
			1,
		)
	} else {
		uriPath = path.Join("/", c.endpointAntiColision, c.User.PathEscape(), c.Password.PathEscape(), fmt.Sprintf("%d", trackIndex), path.Base(uriPath))
	}

	base, err := url.Parse(c.proxyBaseURL())
	if err != nil {
		return "", err
	}
	base.User = oriURL.User

	newURL, err := url.Parse(base.String() + uriPath)
	if err != nil {
		return "", err
	}

	return newURL.String(), nil
}

// withoutURL drops the address from an http client error: it holds the
// provider's credentials, and errors end up in logs.
func withoutURL(err error) error {
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return urlErr.Err
	}
	return err
}
