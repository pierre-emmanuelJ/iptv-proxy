package routes

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"

	"github.com/jamesnetherton/m3u"

	"github.com/pierre-emmanuelJ/iptv-proxy/pkg/config"
	proxyM3U "github.com/pierre-emmanuelJ/iptv-proxy/pkg/m3u"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
)

type proxy struct {
	*config.ProxyConfig
	*m3u.Track
	newM3U []byte
}

// Serve the pfinder api
func Serve(proxyConfig *config.ProxyConfig) error {
	router := gin.Default()
	router.Use(cors.Default())
	newM3U, err := initm3u(proxyConfig)
	if err != nil {
		return err
	}
	Routes(proxyConfig, router.Group("/"), newM3U)

	return router.Run(fmt.Sprintf(":%d", proxyConfig.HostConfig.Port))
}

// Routes adds the routes for the app to the RouterGroup r
func Routes(proxyConfig *config.ProxyConfig, r *gin.RouterGroup, newM3U []byte) {

	p := &proxy{
		proxyConfig,
		nil,
		newM3U,
	}

	r.GET("/iptv.m3u", p.authenticate, p.getM3U)

	// XXX Private need for external Android app
	r.POST("/iptv.m3u", p.authenticate, p.getM3U)

	for i, track := range proxyConfig.Playlist.Tracks {
		oriURL, err := url.Parse(track.URI)
		if err != nil {
			return
		}
		tmp := &proxy{
			nil,
			&proxyConfig.Playlist.Tracks[i],
			nil,
		}
		r.GET(oriURL.RequestURI(), p.authenticate, tmp.reverseProxy)
	}
}

func (p *proxy) reverseProxy(c *gin.Context) {
	rpURL, err := url.Parse(p.Track.URI)
	if err != nil {
		log.Fatal(err)
	}

	resp, err := http.Get(rpURL.String())
	if err != nil {
		log.Fatal(err)
	}
	defer resp.Body.Close()

	copyHTTPHeader(c, resp.Header)
	c.Status(resp.StatusCode)
	c.Stream(func(w io.Writer) bool {
		io.Copy(w, resp.Body)
		return false
	})
}

func copyHTTPHeader(c *gin.Context, header http.Header) {
	for k, v := range header {
		c.Header(k, strings.Join(v, ", "))
	}
}

func (p *proxy) getM3U(c *gin.Context) {
	c.Header("Content-Disposition", "attachment; filename=\"iptv.m3u\"")
	c.Data(http.StatusOK, "application/octet-stream", p.newM3U)
}

// AuthRequest handle auth credentials
type AuthRequest struct {
	User     string `form:"user" binding:"required"`
	Password string `form:"password" binding:"required"`
} // XXX very unsafe

func (p *proxy) authenticate(ctx *gin.Context) {
	var authReq AuthRequest
	if err := ctx.Bind(&authReq); err != nil {
		ctx.AbortWithError(http.StatusBadRequest, err)
		return
	}
	//XXX very unsafe
	if p.ProxyConfig.User != authReq.User || p.ProxyConfig.Password != authReq.Password {
		ctx.AbortWithStatus(http.StatusUnauthorized)
	}
}

func initm3u(p *config.ProxyConfig) ([]byte, error) {
	playlist, err := proxyM3U.ReplaceURL(p)
	if err != nil {
		return nil, err
	}

	result, err := proxyM3U.Marshall(playlist)
	if err != nil {
		return nil, err
	}

	return []byte(result), nil
}
