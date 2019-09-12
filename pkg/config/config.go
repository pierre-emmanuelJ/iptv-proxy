package config

import (
	"net/url"

	"github.com/jamesnetherton/m3u"
)

// HostConfiguration containt host infos
type HostConfiguration struct {
	Hostname string
	Port     int64
}

// ProxyConfig Contain original m3u playlist and HostConfiguration
type ProxyConfig struct {
	Playlist       *m3u.Playlist
	HostConfig     *HostConfiguration
	XtreamUser     string
	XtreamPassword string
	XtreamBaseURL  string
	RemoteURL      *url.URL
	HTTPS          bool
	//XXX Very unsafe
	User, Password string
}
