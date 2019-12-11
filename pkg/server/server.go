package server

import (
	"fmt"
	"net/http"
	"net/url"

	"github.com/pierre-emmanuelJ/iptv-proxy/pkg/config"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/grafov/m3u8"
)

const (
	defaultProxyfiedM3UPath = "/tmp/iptv-proxy.m3u"
)

// Config represent the server configuration
type Config struct {
	*config.ProxyConfig

	// M3U service part
	playlist *m3u8.MasterPlaylist
	// this variable is set only for m3u proxy endpoints
	track *m3u8.Variant
	// path to the proxyfied m3u file
	proxyfiedM3UPath string
}

// NewServer initialize a new server configuration
func NewServer(config *config.ProxyConfig) (*Config, error) {
	resp, err := http.Get(config.RemoteURL.String())
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	p := m3u8.NewMasterPlaylist()
	err = p.DecodeFrom(resp.Body, true)
	if err != nil {
		return nil, err
	}

	println(p.String())

	return &Config{
		config,
		p,
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
	c.m3uRoutes(group)

	return router.Run(fmt.Sprintf(":%d", c.HostConfig.Port))
}

func (c *Config) playlistInitialization() error {
	if len(c.playlist.Variants) == 0 {
		return nil
	}

	return c.initm3u()
}

func (c *Config) initm3u() error {
	new := m3u8.NewMasterPlaylist()

	for _, variant := range c.playlist.Variants {

		oriURL, err := url.Parse(variant.URI)
		if err != nil {
			return err
		}

		protocol := "http"
		if c.HTTPS {
			protocol = "https"
		}

		uri := fmt.Sprintf(
			"%s://%s:%d%s?username=%s&password=%s",
			protocol,
			c.HostConfig.Hostname,
			c.HostConfig.Port,
			oriURL.EscapedPath(),
			url.QueryEscape(c.User),
			url.QueryEscape(c.Password),
		)
		destURI, err := url.Parse(uri)
		if err != nil {
			return err
		}

		new.Append(destURI.String(), nil, m3u8.VariantParams{})
	}

	return nil
}
