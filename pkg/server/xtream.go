package server

import (
	"encoding/base64"
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jamesnetherton/m3u"
	"github.com/pierre-emmanuelJ/iptv-proxy/pkg/config"
	proxyM3U "github.com/pierre-emmanuelJ/iptv-proxy/pkg/m3u"
	xtreamapi "github.com/pierre-emmanuelJ/iptv-proxy/pkg/xtream-proxy"
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
	playlist, err := m3u.Parse(m3uURL.String())
	if err != nil {
		return err
	}

	newM3U, err := xtreamReplaceURL(&playlist, c.User, c.Password, c.HostConfig, c.HTTPS)
	if err != nil {
		return err
	}

	result, err := proxyM3U.Marshall(newM3U)
	if err != nil {
		return err
	}

	xtreamM3uCacheLock.Lock()
	path, err := writeCacheTmp([]byte(result), m3uURL.String())
	if err != nil {
		return err
	}

	xtreamM3uCache[m3uURL.String()] = cacheMeta{path, time.Now()}
	xtreamM3uCacheLock.Unlock()

	return nil
}

func writeCacheTmp(data []byte, url string) (string, error) {
	filename := base64.StdEncoding.EncodeToString([]byte(url))
	path := filepath.Join("/tmp", filename)
	if err := ioutil.WriteFile(path, data, 0644); err != nil {
		return "", err
	}

	return path, nil
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

	println(rawURL)

	m3uURL, err := url.Parse(rawURL)
	if err != nil {
		ctx.AbortWithError(http.StatusInternalServerError, err)
		return
	}

	xtreamM3uCacheLock.RLock()
	meta, ok := xtreamM3uCache[m3uURL.String()]
	d := time.Now().Sub(meta.Time)
	if !ok || d.Hours() >= float64(c.M3UCacheExpiration) {
		log.Printf("[iptv-proxy] %v | %s | xtream cache m3u file\n", time.Now().Format("2006/01/02 - 15:04:05"), ctx.ClientIP())
		xtreamM3uCacheLock.RUnlock()
		if err := c.cacheXtreamM3u(m3uURL); err != nil {
			ctx.AbortWithError(http.StatusInternalServerError, err)
			return
		}
	} else {
		xtreamM3uCacheLock.RUnlock()
	}

	ctx.Header("Content-Disposition", fmt.Sprintf(`attachment; filename=%q`, c.M3UFileName))
	xtreamM3uCacheLock.RLock()
	path := xtreamM3uCache[m3uURL.String()].string
	xtreamM3uCacheLock.RUnlock()
	data, err := ioutil.ReadFile(path)
	if err != nil {
		ctx.AbortWithError(http.StatusInternalServerError, err)
		return
	}
	ctx.Data(http.StatusOK, "application/octet-stream", data)

}

func (c *Config) xtreamPlayerAPIGET(ctx *gin.Context) {
	c.xtreamPlayerAPI(ctx, ctx.Request.URL.Query())
}

func (c *Config) xtreamPlayerAPIPOST(ctx *gin.Context) {
	contents, err := ioutil.ReadAll(ctx.Request.Body)
	if err != nil {
		ctx.AbortWithError(http.StatusInternalServerError, err)
		return
	}

	q, err := url.ParseQuery(string(contents))
	if err != nil {
		ctx.AbortWithError(http.StatusInternalServerError, err)
		return
	}

	c.xtreamPlayerAPI(ctx, q)
}

func (c *Config) xtreamPlayerAPI(ctx *gin.Context, q url.Values) {
	var action string
	if len(q["action"]) > 0 {
		action = q["action"][0]
	}

	protocol := "http"
	if c.HTTPS {
		protocol = "https"
	}

	client, err := xtreamapi.New(c.XtreamUser, c.XtreamPassword, c.XtreamBaseURL)
	if err != nil {
		ctx.AbortWithError(http.StatusInternalServerError, err)
		return
	}

	var respBody interface{}

	switch action {
	case xtreamapi.GetLiveCategories:
		respBody, err = client.GetLiveCategories()
	case xtreamapi.GetLiveStreams:
		respBody, err = client.GetLiveStreams("")
	case xtreamapi.GetVodCategories:
		respBody, err = client.GetVideoOnDemandCategories()
	case xtreamapi.GetVodStreams:
		respBody, err = client.GetVideoOnDemandStreams("")
	case xtreamapi.GetVodInfo:
		if len(q["vod_id"]) < 1 {
			ctx.AbortWithError(http.StatusBadRequest, fmt.Errorf(`bad body url query parameters: missing "vod_id"`))
			return
		}
		respBody, err = client.GetVideoOnDemandInfo(q["vod_id"][0])
	case xtreamapi.GetSeriesCategories:
		respBody, err = client.GetSeriesCategories()
	case xtreamapi.GetSeries:
		respBody, err = client.GetSeries("")
	case xtreamapi.GetSerieInfo:
		if len(q["series_id"]) < 1 {
			ctx.AbortWithError(http.StatusBadRequest, fmt.Errorf(`bad body url query parameters: missing "series_id"`))
			return
		}
		respBody, err = client.GetSeriesInfo(q["series_id"][0])
	case xtreamapi.GetShortEPG:
		if len(q["stream_id"]) < 1 {
			ctx.AbortWithError(http.StatusBadRequest, fmt.Errorf(`bad body url query parameters: missing "stream_id"`))
			return
		}
		limit := 0
		if len(q["limit"]) > 0 {
			limit, err = strconv.Atoi(q["limit"][0])
			if err != nil {
				ctx.AbortWithError(http.StatusInternalServerError, err)
				return
			}
		}
		respBody, err = client.GetShortEPG(q["stream_id"][0], limit)
	case xtreamapi.GetSimpleDataTable:
		if len(q["stream_id"]) < 1 {
			ctx.AbortWithError(http.StatusBadRequest, fmt.Errorf(`bad body url query parameters: missing "stream_id"`))
			return
		}
		respBody, err = client.GetEPG(q["stream_id"][0])
	default:
		respBody, err = client.Login(c.User, c.Password, protocol+"://"+c.HostConfig.Hostname, int(c.HostConfig.Port), protocol)
	}

	log.Printf("[iptv-proxy] %v | %s |Action\t%s\n", time.Now().Format("2006/01/02 - 15:04:05"), ctx.ClientIP(), action)

	if err != nil {
		ctx.AbortWithError(http.StatusInternalServerError, err)
		return
	}

	ctx.JSON(http.StatusOK, respBody)
}

func (c *Config) xtreamXMLTV(ctx *gin.Context) {
	client, err := xtreamapi.New(c.XtreamUser, c.XtreamPassword, c.XtreamBaseURL)
	if err != nil {
		ctx.AbortWithError(http.StatusInternalServerError, err)
		return
	}

	resp, err := client.GetXMLTV()
	if err != nil {
		ctx.AbortWithError(http.StatusInternalServerError, err)
		return
	}

	ctx.Data(http.StatusOK, "application/xml", resp)
}

func (c *Config) xtreamStream(ctx *gin.Context) {
	id := ctx.Param("id")
	rpURL, err := url.Parse(fmt.Sprintf("%s/%s/%s/%s", c.XtreamBaseURL, c.XtreamUser, c.XtreamPassword, id))
	if err != nil {
		ctx.AbortWithError(http.StatusInternalServerError, err)
		return
	}

	c.stream(ctx, rpURL)
}

func (c *Config) xtreamStreamLive(ctx *gin.Context) {
	id := ctx.Param("id")
	rpURL, err := url.Parse(fmt.Sprintf("%s/live/%s/%s/%s", c.XtreamBaseURL, c.XtreamUser, c.XtreamPassword, id))
	if err != nil {
		ctx.AbortWithError(http.StatusInternalServerError, err)
		return
	}

	c.stream(ctx, rpURL)
}

func (c *Config) xtreamStreamMovie(ctx *gin.Context) {
	id := ctx.Param("id")
	rpURL, err := url.Parse(fmt.Sprintf("%s/movie/%s/%s/%s", c.XtreamBaseURL, c.XtreamUser, c.XtreamPassword, id))
	if err != nil {
		ctx.AbortWithError(http.StatusInternalServerError, err)
		return
	}

	c.stream(ctx, rpURL)
}

func (c *Config) xtreamStreamSeries(ctx *gin.Context) {
	id := ctx.Param("id")
	rpURL, err := url.Parse(fmt.Sprintf("%s/series/%s/%s/%s", c.XtreamBaseURL, c.XtreamUser, c.XtreamPassword, id))
	if err != nil {
		ctx.AbortWithError(http.StatusInternalServerError, err)
		return
	}

	c.stream(ctx, rpURL)
}

func (c *Config) hlsrStream(ctx *gin.Context) {
	hlsChannelsRedirectURLLock.RLock()
	url, ok := hlsChannelsRedirectURL[ctx.Param("channel")+".m3u8"]
	if !ok {
		ctx.AbortWithError(http.StatusNotFound, errors.New("HSL redirect url not found"))
		return
	}
	hlsChannelsRedirectURLLock.RUnlock()

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
		ctx.AbortWithError(http.StatusInternalServerError, err)
		return
	}

	c.stream(ctx, req)
}

func xtreamReplaceURL(playlist *m3u.Playlist, user, password string, hostConfig *config.HostConfiguration, https bool) (*m3u.Playlist, error) {
	result := make([]m3u.Track, 0, len(playlist.Tracks))
	for _, track := range playlist.Tracks {
		oriURL, err := url.Parse(track.URI)
		if err != nil {
			return nil, err
		}

		protocol := "http"
		if https {
			protocol = "https"
		}

		id := filepath.Base(oriURL.Path)

		uri := fmt.Sprintf(
			"%s://%s:%d/%s/%s/%s",
			protocol,
			hostConfig.Hostname,
			hostConfig.Port,
			url.QueryEscape(user),
			url.QueryEscape(password),
			url.QueryEscape(id),
		)
		destURL, err := url.Parse(uri)
		if err != nil {
			return nil, err
		}

		track.URI = destURL.String()
		result = append(result, track)
	}

	return &m3u.Playlist{
		Tracks: result,
	}, nil
}
