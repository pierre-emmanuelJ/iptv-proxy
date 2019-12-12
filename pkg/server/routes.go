package server

import (
	"fmt"
	"log"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"
)

func (c *Config) routes(r *gin.RouterGroup) {
	r = r.Group(c.CustomEndpoint)

	//Xtream service endopoints
	if c.ProxyConfig.XtreamBaseURL != "" {
		c.xtreamRoutes(r)
		if strings.Contains(c.XtreamBaseURL, c.RemoteURL.Host) &&
			c.XtreamUser == c.RemoteURL.Query().Get("username") &&
			c.XtreamPassword == c.RemoteURL.Query().Get("password") {

			r.GET("/"+c.M3UFileName, c.authenticate, c.xtreamGetAuto)
			// XXX Private need: for external Android app
			r.POST("/"+c.M3UFileName, c.authenticate, c.xtreamGetAuto)

			return
		}
	}

	c.m3uRoutes(r)
}

func (c *Config) xtreamRoutes(r *gin.RouterGroup) {
	r.GET("/get.php", c.authenticate, c.xtreamGet)
	r.POST("/get.php", c.authenticate, c.xtreamGet)
	r.GET("/player_api.php", c.authenticate, c.xtreamPlayerAPIGET)
	r.POST("/player_api.php", c.appAuthenticate, c.xtreamPlayerAPIPOST)
	r.GET("/xmltv.php", c.authenticate, c.xtreamXMLTV)
	r.GET(fmt.Sprintf("/%s/%s/:id", c.User, c.Password), c.xtreamStream)
	r.GET(fmt.Sprintf("/live/%s/%s/:id", c.User, c.Password), c.xtreamStreamLive)
	r.GET(fmt.Sprintf("/movie/%s/%s/:id", c.User, c.Password), c.xtreamStreamMovie)
	r.GET(fmt.Sprintf("/series/%s/%s/:id", c.User, c.Password), c.xtreamStreamSeries)
	r.GET(fmt.Sprintf("/hlsr/:token/%s/%s/:channel/:hash/:chunk", c.User, c.Password), c.hlsrStream)
}

func (c *Config) m3uRoutes(r *gin.RouterGroup) {
	r.GET("/"+c.M3UFileName, c.authenticate, c.getM3U)
	// XXX Private need: for external Android app
	r.POST("/"+c.M3UFileName, c.authenticate, c.getM3U)

	// List to verify duplicate entry endpoints
	checkList := map[string]int8{}
	for i, track := range c.playlist.Tracks {
		oriURL, err := url.Parse(track.URI)
		if err != nil {
			return
		}
		trackConfig := &Config{
			ProxyConfig: c.ProxyConfig,
			track:       &c.playlist.Tracks[i],
		}
		_, ok := checkList[oriURL.Path]
		if ok {
			log.Printf("[iptv-proxy] WARNING endpoint %q already exist, skipping it", oriURL.Path)
			continue
		}

		r.GET(oriURL.Path, c.authenticate, trackConfig.reverseProxy)

		checkList[oriURL.Path] = 0
	}
}
