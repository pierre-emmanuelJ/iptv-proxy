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
