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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"html"
	"net"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/pierre-emmanuelJ/iptv-proxy/pkg/m3u"
	"github.com/pierre-emmanuelJ/iptv-proxy/pkg/xtream"
)

// With --hdhomerun-port, the proxy also answers as an HDHomeRun network
// tuner, on a port of its own: Plex, Emby, Jellyfin or Channels DVR then see
// the live channels as a TV tuner, with a guide matched to them.
//
// A tuner has no login: anyone who reaches that port can watch. It is meant
// for the local network only. In exchange, nothing it answers holds the
// proxy's or the provider's credentials.

const defaultTuners = 2

// lineupStale is how old a channel list may be when a stream is asked for a
// channel it does not have: older, the list is read again.
var lineupStale = time.Minute

// tunerChannel is a channel of the tuner.
type tunerChannel struct {
	Number  string
	Name    string
	GuideID string
	// source is the provider's stream id (Xtream) or address (M3U).
	source string
}

// tuner is what the tuner remembers between requests.
type tuner struct {
	mu       sync.Mutex
	channels map[string]tunerChannel // by number
	at       time.Time
	count    int // tuners, once known
}

// HDHomeRunHandler is the tuner's HTTP API.
func (c *Config) HDHomeRunHandler() http.Handler {
	router := gin.New()
	router.Use(gin.LoggerWithFormatter(c.accessLog), gin.Recovery())
	router.GET("/discover.json", c.tunerDiscover)
	router.GET("/device.xml", c.tunerDevice)
	router.GET("/lineup_status.json", c.tunerStatus)
	router.GET("/lineup.json", c.tunerLineup)
	router.POST("/lineup.post", func(ctx *gin.Context) { ctx.Status(http.StatusOK) })
	router.GET("/guide.xml", c.tunerGuide)
	router.GET("/auto/:channel", c.tunerStream)
	return router
}

// tunerBase is the tuner's address, as its client reached it.
func tunerBase(ctx *gin.Context) string {
	return "http://" + ctx.Request.Host
}

// deviceID is the tuner's id: the same settings give the same id.
func (c *Config) deviceID() string {
	sum := sha256.Sum256([]byte("iptv-proxy tuner\x00" + c.XtreamBaseURL + "\x00" + c.XtreamUser.String() + "\x00" + c.remoteAddress()))
	return strings.ToUpper(hex.EncodeToString(sum[:4]))
}

func (c *Config) remoteAddress() string {
	if c.RemoteURL == nil {
		return ""
	}
	return c.RemoteURL.String()
}

func (c *Config) tunerDiscover(ctx *gin.Context) {
	base := tunerBase(ctx)
	ctx.JSON(http.StatusOK, gin.H{
		"FriendlyName":    "iptv-proxy",
		"Manufacturer":    "Silicondust",
		"ManufacturerURL": "https://github.com/pierre-emmanuelJ/iptv-proxy",
		"ModelNumber":     "HDTC-2US",
		"FirmwareName":    "hdhomeruntc_atsc",
		"FirmwareVersion": "20200101",
		"DeviceID":        c.deviceID(),
		"DeviceAuth":      "iptv-proxy",
		"TunerCount":      c.tunerCount(ctx),
		"BaseURL":         base,
		"LineupURL":       base + "/lineup.json",
	})
}

func (c *Config) tunerDevice(ctx *gin.Context) {
	base := html.EscapeString(tunerBase(ctx))
	ctx.Data(http.StatusOK, "application/xml", []byte(`<?xml version="1.0" encoding="UTF-8"?>
<root xmlns="urn:schemas-upnp-org:device-1-0">
  <URLBase>`+base+`</URLBase>
  <specVersion><major>1</major><minor>0</minor></specVersion>
  <device>
    <deviceType>urn:schemas-upnp-org:device:MediaServer:1</deviceType>
    <friendlyName>iptv-proxy</friendlyName>
    <manufacturer>Silicondust</manufacturer>
    <modelName>HDTC-2US</modelName>
    <modelNumber>HDTC-2US</modelNumber>
    <serialNumber>`+c.deviceID()+`</serialNumber>
    <UDN>uuid:iptv-proxy-`+c.deviceID()+`</UDN>
  </device>
</root>
`))
}

func (c *Config) tunerStatus(ctx *gin.Context) {
	ctx.JSON(http.StatusOK, gin.H{"ScanInProgress": 0, "ScanPossible": 1, "Source": "Cable", "SourceList": []string{"Cable"}})
}

type lineupEntry struct {
	GuideNumber string `json:"GuideNumber"`
	GuideName   string `json:"GuideName"`
	URL         string `json:"URL"`
}

func (c *Config) tunerLineup(ctx *gin.Context) {
	channels, err := c.tunerChannels(ctx)
	if err != nil {
		c.upstreamError(ctx, err)
		return
	}
	base := tunerBase(ctx)
	lineup := make([]lineupEntry, 0, len(channels))
	for _, ch := range channels {
		lineup = append(lineup, lineupEntry{GuideNumber: ch.Number, GuideName: ch.Name, URL: base + "/auto/v" + url.PathEscape(ch.Number)})
	}
	ctx.JSON(http.StatusOK, lineup)
}

// tunerChannels reads the channel list from the provider (or the M3U
// playlist), the filters applied, and remembers it to find the channels
// streams are asked for.
func (c *Config) tunerChannels(ctx *gin.Context) ([]tunerChannel, error) {
	var channels []tunerChannel
	if c.servesM3U() {
		channels = c.m3uCurrent(ctx.Request.Context()).live
	} else {
		var err error
		if channels, err = c.xtreamTunerChannels(ctx); err != nil {
			return nil, err
		}
	}

	byNumber := make(map[string]tunerChannel, len(channels))
	for _, ch := range channels {
		byNumber[ch.Number] = ch
	}
	c.tuner.mu.Lock()
	c.tuner.channels, c.tuner.at = byNumber, time.Now()
	c.tuner.mu.Unlock()
	return channels, nil
}

// xtreamTunerChannels lists the live streams the filters keep. A stream's
// number is its id at the provider: it does not change when the provider
// reorders its list, so recordings stay on their channel.
func (c *Config) xtreamTunerChannels(ctx *gin.Context) ([]tunerChannel, error) {
	var groups map[string]string
	if c.tunerRules.FiltersGroups() {
		var err error
		if groups, err = c.liveGroupNames(ctx); err != nil {
			return nil, err
		}
	}
	streams, err := c.providerList(ctx, "get_live_streams")
	if err != nil {
		return nil, err
	}
	channels := make([]tunerChannel, 0, len(streams))
	seen := map[string]bool{}
	for _, stream := range streams {
		id := xtream.Text(stream["stream_id"])
		if id == "" || seen[id] || !keepStream(stream, groups, c.tunerRules) {
			continue
		}
		seen[id] = true
		channels = append(channels, tunerChannel{
			Number:  id,
			Name:    xtream.Text(stream["name"]),
			GuideID: xtream.Text(stream["epg_channel_id"]),
			source:  id,
		})
	}
	return channels, nil
}

// m3uTunerChannels lists the live tracks of a playlist: those that are not
// files. A track's number is its tvg-chno, else its place among them.
func m3uTunerChannels(tracks []m3u.Track) []tunerChannel {
	var channels []tunerChannel
	used := map[string]bool{}
	for _, track := range tracks {
		address, err := url.Parse(track.URI)
		if err != nil || fileExtensions[strings.ToLower(path.Ext(address.Path))] {
			continue
		}
		number, _ := m3u.Attribute(track.ExtInf, "tvg-chno")
		if number = strings.TrimSpace(number); number == "" || used[number] {
			number = strconv.Itoa(len(channels) + 1)
			for used[number] {
				number += ".1"
			}
		}
		used[number] = true
		name := track.Name()
		if name == "" {
			name, _ = m3u.Attribute(track.ExtInf, "tvg-name")
		}
		guideID, _ := m3u.Attribute(track.ExtInf, "tvg-id")
		channels = append(channels, tunerChannel{Number: number, Name: name, GuideID: guideID, source: track.URI})
	}
	return channels
}

// tunerStream serves a channel, "/auto/v<number>", as a tuner does.
func (c *Config) tunerStream(ctx *gin.Context) {
	number, ok := strings.CutPrefix(ctx.Param("channel"), "v")
	if !ok {
		ctx.AbortWithStatus(http.StatusNotFound)
		return
	}

	c.tuner.mu.Lock()
	ch, known := c.tuner.channels[number]
	stale := time.Since(c.tuner.at) > lineupStale
	c.tuner.mu.Unlock()
	if !known && stale {
		// a client may ask before it read the list, or after it changed
		if _, err := c.tunerChannels(ctx); err != nil {
			c.upstreamError(ctx, err)
			return
		}
		c.tuner.mu.Lock()
		ch, known = c.tuner.channels[number]
		c.tuner.mu.Unlock()
	}
	if !known {
		// only the channels of the list play: the filters hold here
		ctx.AbortWithStatus(http.StatusNotFound)
		return
	}

	if c.servesM3U() {
		address, err := url.Parse(ch.source)
		if err != nil {
			ctx.AbortWithStatus(http.StatusNotFound)
			return
		}
		c.streamLive(ctx, address)
		return
	}
	// a tuner sends MPEG-TS
	c.xtreamProviderStream(ctx, "live/", url.PathEscape(ch.source)+".ts", true)
}

// tunerGuide serves the guide with each channel under its tuner number, as
// media servers match them.
func (c *Config) tunerGuide(ctx *gin.Context) {
	address := ""
	if c.servesM3U() {
		address = c.m3uCurrent(ctx.Request.Context()).guide
	} else if c.XtreamBaseURL != "" {
		address = c.providerAccount().APIURL("xmltv.php", nil)
	}
	if address == "" {
		ctx.AbortWithStatus(http.StatusNotFound)
		return
	}
	c.guide(ctx, address, "tuner guide", func() (channelMap, error) {
		channels, err := c.tunerChannels(ctx)
		if err != nil {
			return nil, err
		}
		numbers := map[string][]string{}
		for _, ch := range channels {
			if ch.GuideID != "" {
				numbers[ch.GuideID] = append(numbers[ch.GuideID], ch.Number)
			}
		}
		return func(channel string) []string { return numbers[channel] }, nil
	}, c.otherGuides(nil))
}

// tunerCount is the number of tuners announced: --hdhomerun-tuners, else the
// connections the Xtream account allows, else 2. A media server does not
// open more streams at once than it has tuners.
func (c *Config) tunerCount(ctx *gin.Context) int {
	if c.HDHomeRunTuners > 0 {
		return c.HDHomeRunTuners
	}
	c.tuner.mu.Lock()
	known := c.tuner.count
	c.tuner.mu.Unlock()
	if known > 0 {
		return known
	}

	count := defaultTuners
	if c.XtreamBaseURL != "" {
		if status, _, body, err := c.providerGet(ctx, "player_api.php", nil); err == nil && status == http.StatusOK {
			var login struct {
				UserInfo struct {
					MaxConnections any `json:"max_connections"`
				} `json:"user_info"`
			}
			if json.Unmarshal(body, &login) == nil {
				if n, err := strconv.Atoi(xtream.Text(login.UserInfo.MaxConnections)); err == nil && n > 0 {
					count = n
				}
			}
		}
	}
	c.tuner.mu.Lock()
	c.tuner.count = count
	c.tuner.mu.Unlock()
	return count
}

// hdhomerunAddress is the address the tuner listens on.
func (c *Config) hdhomerunAddress() string {
	return net.JoinHostPort(c.ListenAddress, strconv.Itoa(c.HDHomeRunPort))
}
