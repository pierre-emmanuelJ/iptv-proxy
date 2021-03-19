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
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jamesnetherton/m3u"
	xtreamapi "github.com/pierre-emmanuelJ/iptv-proxy/pkg/xtream-proxy"
	uuid "github.com/satori/go.uuid"
)

type cacheMeta struct {
	string
	time.Time
}

var hlsChannelsRedirectURL map[string]url.URL = map[string]url.URL{}
var hlsChannelsRedirectURLLock = sync.RWMutex{}

// XXX Use key/value storage e.g: etcd, redis...
// and remove that dirty globals
var xtreamM3uCache map[string]cacheMeta = map[string]cacheMeta{}
var xtreamM3uCacheLock = sync.RWMutex{}

func (c *Config) cacheXtreamM3u(m3uURL *url.URL) error {
	xtreamM3uCacheLock.Lock()
	defer xtreamM3uCacheLock.Unlock()

	playlist, err := m3u.Parse(m3uURL.String())
	if err != nil {
		return err
	}

	tmp := *c
	tmp.playlist = &playlist

	path := filepath.Join(os.TempDir(), uuid.NewV4().String()+".iptv-proxy.m3u")
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	if err := tmp.marshallInto(f, true); err != nil {
		return err
	}
	xtreamM3uCache[m3uURL.String()] = cacheMeta{path, time.Now()}

	return nil
}

func (c *Config) xtreamGetAuto(ctx *gin.Context) {
	newQuery := ctx.Request.URL.Query()
	q := c.RemoteURL.Query()
	for k, v := range q {
		if k == "username" || k == "password" {
			continue
		}

		newQuery.Add(k, strings.Join(v, ","))
	}
	ctx.Request.URL.RawQuery = newQuery.Encode()

	c.xtreamGet(ctx)
}

func (c *Config) xtreamGet(ctx *gin.Context) {
	rawURL := fmt.Sprintf("%s/get.php?username=%s&password=%s", c.XtreamBaseURL, c.XtreamUser, c.XtreamPassword)

	q := ctx.Request.URL.Query()

	for k, v := range q {
		if k == "username" || k == "password" {
			continue
		}

		rawURL = fmt.Sprintf("%s&%s=%s", rawURL, k, strings.Join(v, ","))
	}

	m3uURL, err := url.Parse(rawURL)
	if err != nil {
		ctx.AbortWithError(http.StatusInternalServerError, err) // nolint: errcheck
		return
	}

	xtreamM3uCacheLock.RLock()
	meta, ok := xtreamM3uCache[m3uURL.String()]
	d := time.Since(meta.Time)
	if !ok || d.Hours() >= float64(c.M3UCacheExpiration) {
		log.Printf("[iptv-proxy] %v | %s | xtream cache m3u file\n", time.Now().Format("2006/01/02 - 15:04:05"), ctx.ClientIP())
		xtreamM3uCacheLock.RUnlock()
		if err := c.cacheXtreamM3u(m3uURL); err != nil {
			ctx.AbortWithError(http.StatusInternalServerError, err) // nolint: errcheck
			return
		}
	} else {
		xtreamM3uCacheLock.RUnlock()
	}

	ctx.Header("Content-Disposition", fmt.Sprintf(`attachment; filename=%q`, c.M3UFileName))
	xtreamM3uCacheLock.RLock()
	path := xtreamM3uCache[m3uURL.String()].string
	xtreamM3uCacheLock.RUnlock()
	ctx.Header("Content-Type", "application/octet-stream")

	ctx.File(path)
}

func (c *Config) xtreamPlayerAPIGET(ctx *gin.Context) {
	c.xtreamPlayerAPI(ctx, ctx.Request.URL.Query())
}

func (c *Config) xtreamPlayerAPIPOST(ctx *gin.Context) {
	contents, err := ioutil.ReadAll(ctx.Request.Body)
	if err != nil {
		ctx.AbortWithError(http.StatusInternalServerError, err) // nolint: errcheck
		return
	}

	q, err := url.ParseQuery(string(contents))
	if err != nil {
		ctx.AbortWithError(http.StatusInternalServerError, err) // nolint: errcheck
		return
	}

	c.xtreamPlayerAPI(ctx, q)
}

func (c *Config) xtreamPlayerAPI(ctx *gin.Context, q url.Values) {
	var action string
	if len(q["action"]) > 0 {
		action = q["action"][0]
	}

	client, err := xtreamapi.New(c.XtreamUser.String(), c.XtreamPassword.String(), c.XtreamBaseURL, ctx.Request.UserAgent())
	if err != nil {
		ctx.AbortWithError(http.StatusInternalServerError, err) // nolint: errcheck
		return
	}

	resp, httpcode, err := client.Action(c.ProxyConfig, action, q)
	if err != nil {
		ctx.AbortWithError(httpcode, err) // nolint: errcheck
		return
	}

	log.Printf("[iptv-proxy] %v | %s |Action\t%s\n", time.Now().Format("2006/01/02 - 15:04:05"), ctx.ClientIP(), action)

	if err != nil {
		ctx.AbortWithError(http.StatusInternalServerError, err) // nolint: errcheck
		return
	}

	ctx.JSON(http.StatusOK, resp)
}

func (c *Config) xtreamXMLTV(ctx *gin.Context) {
	client, err := xtreamapi.New(c.XtreamUser.String(), c.XtreamPassword.String(), c.XtreamBaseURL, ctx.Request.UserAgent())
	if err != nil {
		ctx.AbortWithError(http.StatusInternalServerError, err) // nolint: errcheck
		return
	}

	resp, err := client.GetXMLTV()
	if err != nil {
		ctx.AbortWithError(http.StatusInternalServerError, err) // nolint: errcheck
		return
	}

	ctx.Data(http.StatusOK, "application/xml", resp)
}

func (c *Config) xtreamStreamHandler(ctx *gin.Context) {
	id := ctx.Param("id")
	rpURL, err := url.Parse(fmt.Sprintf("%s/%s/%s/%s", c.XtreamBaseURL, c.XtreamUser, c.XtreamPassword, id))
	if err != nil {
		ctx.AbortWithError(http.StatusInternalServerError, err) // nolint: errcheck
		return
	}

	c.xtreamStream(ctx, rpURL)
}

func (c *Config) xtreamStreamLive(ctx *gin.Context) {
	id := ctx.Param("id")
	rpURL, err := url.Parse(fmt.Sprintf("%s/live/%s/%s/%s", c.XtreamBaseURL, c.XtreamUser, c.XtreamPassword, id))
	if err != nil {
		ctx.AbortWithError(http.StatusInternalServerError, err) // nolint: errcheck
		return
	}

	c.xtreamStream(ctx, rpURL)
}

func (c *Config) xtreamStreamMovie(ctx *gin.Context) {
	id := ctx.Param("id")
	rpURL, err := url.Parse(fmt.Sprintf("%s/movie/%s/%s/%s", c.XtreamBaseURL, c.XtreamUser, c.XtreamPassword, id))
	if err != nil {
		ctx.AbortWithError(http.StatusInternalServerError, err) // nolint: errcheck
		return
	}

	c.xtreamStream(ctx, rpURL)
}

func (c *Config) xtreamStreamSeries(ctx *gin.Context) {
	id := ctx.Param("id")
	rpURL, err := url.Parse(fmt.Sprintf("%s/series/%s/%s/%s", c.XtreamBaseURL, c.XtreamUser, c.XtreamPassword, id))
	if err != nil {
		ctx.AbortWithError(http.StatusInternalServerError, err) // nolint: errcheck
		return
	}

	c.xtreamStream(ctx, rpURL)
}

func (c *Config) xtreamHlsStream(ctx *gin.Context) {
	chunk := ctx.Param("chunk")
	s := strings.Split(chunk, "_")
	if len(s) != 2 {
		ctx.AbortWithError( // nolint: errcheck
			http.StatusInternalServerError,
			errors.New("HSL malformed chunk"),
		)
		return
	}
	channel := s[0]

	url, err := getHlsRedirectURL(channel)
	if err != nil {
		ctx.AbortWithError(http.StatusInternalServerError, err) // nolint: errcheck
		return
	}

	req, err := url.Parse(
		fmt.Sprintf(
			"%s://%s/hls/%s/%s",
			url.Scheme,
			url.Host,
			ctx.Param("token"),
			ctx.Param("chunk"),
		),
	)

	if err != nil {
		ctx.AbortWithError(http.StatusInternalServerError, err) // nolint: errcheck
		return
	}

	c.xtreamStream(ctx, req)
}

func (c *Config) xtreamHlsrStream(ctx *gin.Context) {
	channel := ctx.Param("channel")

	url, err := getHlsRedirectURL(channel)
	if err != nil {
		ctx.AbortWithError(http.StatusInternalServerError, err) // nolint: errcheck
		return
	}

	req, err := url.Parse(
		fmt.Sprintf(
			"%s://%s/hlsr/%s/%s/%s/%s/%s/%s",
			url.Scheme,
			url.Host,
			ctx.Param("token"),
			c.XtreamUser,
			c.XtreamPassword,
			ctx.Param("channel"),
			ctx.Param("hash"),
			ctx.Param("chunk"),
		),
	)

	if err != nil {
		ctx.AbortWithError(http.StatusInternalServerError, err) // nolint: errcheck
		return
	}

	c.xtreamStream(ctx, req)
}

func getHlsRedirectURL(channel string) (*url.URL, error) {
	hlsChannelsRedirectURLLock.RLock()
	defer hlsChannelsRedirectURLLock.RUnlock()

	url, ok := hlsChannelsRedirectURL[channel+".m3u8"]
	if !ok {
		return nil, errors.New("HSL redirect url not found")
	}

	return &url, nil
}

func (c *Config) hlsXtreamStream(ctx *gin.Context, oriURL *url.URL) {
	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	req, err := http.NewRequest("GET", oriURL.String(), nil)
	if err != nil {
		ctx.AbortWithError(http.StatusInternalServerError, err) // nolint: errcheck
		return
	}

	copyHttpHeader(req.Header, ctx.Request.Header)

	resp, err := client.Do(req)
	if err != nil {
		ctx.AbortWithError(http.StatusInternalServerError, err) // nolint: errcheck
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusFound {
		location, err := resp.Location()
		if err != nil {
			ctx.AbortWithError(http.StatusInternalServerError, err) // nolint: errcheck
			return
		}
		id := ctx.Param("id")
		if strings.Contains(location.String(), id) {
			hlsChannelsRedirectURLLock.Lock()
			hlsChannelsRedirectURL[id] = *location
			hlsChannelsRedirectURLLock.Unlock()

			hlsReq, err := http.NewRequest("GET", location.String(), nil)
			if err != nil {
				ctx.AbortWithError(http.StatusInternalServerError, err) // nolint: errcheck
				return
			}

			copyHttpHeader(hlsReq.Header, ctx.Request.Header)

			hlsResp, err := client.Do(hlsReq)
			if err != nil {
				ctx.AbortWithError(http.StatusInternalServerError, err) // nolint: errcheck
				return
			}
			defer hlsResp.Body.Close()

			b, err := ioutil.ReadAll(hlsResp.Body)
			if err != nil {
				ctx.AbortWithError(http.StatusInternalServerError, err) // nolint: errcheck
				return
			}
			body := string(b)
			body = strings.ReplaceAll(body, "/"+c.XtreamUser.String()+"/"+c.XtreamPassword.String()+"/", "/"+c.User.String()+"/"+c.Password.String()+"/")

			copyHttpHeader(ctx.Request.Header, hlsResp.Header)

			ctx.Data(http.StatusOK, hlsResp.Header.Get("Content-Type"), []byte(body))
			return
		}
		ctx.AbortWithError(http.StatusInternalServerError, errors.New("Unable to HLS stream")) // nolint: errcheck
		return
	}

	ctx.Status(resp.StatusCode)
}
