package config

import "github.com/jamesnetherton/m3u"

// HostConfiguration containt host infos
type HostConfiguration struct {
	Hostname string
	Port     int64
}

// ProxyConfig Contain original m3u playlist and HostConfiguration,
// if track is not nil current track selected in playlist
type ProxyConfig struct {
	Playlist   *m3u.Playlist
	HostConfig *HostConfiguration
}
