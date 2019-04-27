package routes

import (
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/jamesnetherton/m3u"

	"github.com/pierre-emmanuelJ/iptv-proxy/pkg/config"
	proxyM3U "github.com/pierre-emmanuelJ/iptv-proxy/pkg/m3u"
	xtreamapi "github.com/pierre-emmanuelJ/iptv-proxy/pkg/xtream-proxy"

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

	//Xtr Smarter android app compatibility
	r.POST("/player_api.php", p.appAuthenticate, p.xtreamPlayerAPI)
	r.GET("/xmltv.php", p.authenticate, p.xtreamXMLTV)
	r.GET(fmt.Sprintf("/%s/%s/:id", proxyConfig.User, proxyConfig.Password), p.xtreamStream)
	r.GET(fmt.Sprintf("/movie/%s/%s/:id", proxyConfig.User, proxyConfig.Password), p.xtreamStreamMovie)
	r.GET(fmt.Sprintf("/series/%s/%s/:id", proxyConfig.User, proxyConfig.Password), p.xtreamStreamSeries)

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

func (p *proxy) xtreamPlayerAPI(c *gin.Context) {
	contents, err := ioutil.ReadAll(c.Request.Body)
	if err != nil {
		c.AbortWithError(http.StatusInternalServerError, err)
		return
	}

	log.Println(string(contents))

	q, err := url.ParseQuery(string(contents))
	if err != nil {
		c.AbortWithError(http.StatusInternalServerError, err)
		return
	}
	if len(q["username"]) == 0 || len(q["password"]) == 0 {
		c.AbortWithError(http.StatusBadRequest, fmt.Errorf(`bad body url query parameters: missing "username" and "password"`))
		return
	}

	var action string
	if len(q["action"]) > 0 {
		action = q["action"][0]
	}

	client, err := xtreamapi.New(p.XtreamUser, p.XtreamPassword, p.XtreamBaseURL)
	if err != nil {
		c.AbortWithError(http.StatusInternalServerError, err)
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
			c.AbortWithError(http.StatusBadRequest, fmt.Errorf(`bad body url query parameters: missing "vod_id"`))
			return
		}
		respBody, err = client.GetVideoOnDemandInfo(q["vod_id"][0])
	case xtreamapi.GetSeriesCategories:
		respBody, err = client.GetSeriesCategories()
	case xtreamapi.GetSeries:
		respBody, err = client.GetSeries("")
	case xtreamapi.GetSerieInfo:
		if len(q["series_id"]) < 1 {
			c.AbortWithError(http.StatusBadRequest, fmt.Errorf(`bad body url query parameters: missing "series_id"`))
			return
		}
		respBody, err = client.GetSeriesInfo(q["series_id"][0])
	default:
		respBody, err = client.Login(p.User, p.Password, "http://"+p.HostConfig.Hostname, int(p.HostConfig.Port))
	}

	log.Printf("[iptv-proxy] %v | %s |Action\t%s\n", time.Now().Format("2006/01/02 - 15:04:05"), c.ClientIP(), action)

	if err != nil {
		c.AbortWithError(http.StatusInternalServerError, err)
		return
	}

	c.JSON(http.StatusOK, respBody)
}

func (p *proxy) xtreamXMLTV(c *gin.Context) {
	client, err := xtreamapi.New(p.XtreamUser, p.XtreamPassword, p.XtreamBaseURL)
	if err != nil {
		c.AbortWithError(http.StatusInternalServerError, err)
		return
	}

	resp, err := client.GetXMLTV()
	if err != nil {
		c.AbortWithError(http.StatusInternalServerError, err)
		return
	}

	c.Data(http.StatusOK, "application/xml", resp)
}

func (p *proxy) xtreamStream(c *gin.Context) {
	id := c.Param("id")
	rpURL, err := url.Parse(fmt.Sprintf("%s/%s/%s/%s", p.XtreamBaseURL, p.XtreamUser, p.XtreamPassword, id))
	if err != nil {
		c.AbortWithError(http.StatusInternalServerError, err)
		return
	}

	stream(c, rpURL)
}

func (p *proxy) xtreamStreamMovie(c *gin.Context) {
	id := c.Param("id")
	rpURL, err := url.Parse(fmt.Sprintf("%s/movie/%s/%s/%s", p.XtreamBaseURL, p.XtreamUser, p.XtreamPassword, id))
	if err != nil {
		c.AbortWithError(http.StatusInternalServerError, err)
		return
	}

	stream(c, rpURL)
}

func (p *proxy) xtreamStreamSeries(c *gin.Context) {
	id := c.Param("id")
	rpURL, err := url.Parse(fmt.Sprintf("%s/series/%s/%s/%s", p.XtreamBaseURL, p.XtreamUser, p.XtreamPassword, id))
	if err != nil {
		c.AbortWithError(http.StatusInternalServerError, err)
		return
	}

	stream(c, rpURL)
}

func (p *proxy) reverseProxy(c *gin.Context) {
	rpURL, err := url.Parse(p.Track.URI)
	if err != nil {
		log.Fatal(err)
	}

	stream(c, rpURL)
}

func stream(c *gin.Context, oriURL *url.URL) {
	resp, err := http.Get(oriURL.String())
	if err != nil {
		c.AbortWithError(http.StatusInternalServerError, err)
		return
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
	User     string `form:"username" binding:"required"`
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

func (p *proxy) appAuthenticate(c *gin.Context) {
	contents, err := ioutil.ReadAll(c.Request.Body)
	if err != nil {
		c.AbortWithError(http.StatusInternalServerError, err)
		return
	}

	q, err := url.ParseQuery(string(contents))
	if err != nil {
		c.AbortWithError(http.StatusInternalServerError, err)
		return
	}
	if len(q["username"]) == 0 || len(q["password"]) == 0 {
		c.AbortWithError(http.StatusBadRequest, fmt.Errorf("bad body url query parameters"))
		return
	}
	log.Printf("[iptv-proxy] %v | %s |App Auth\n", time.Now().Format("2006/01/02 - 15:04:05"), c.ClientIP())
	//XXX very unsafe
	if p.ProxyConfig.User != q["username"][0] || p.ProxyConfig.Password != q["password"][0] {
		c.AbortWithStatus(http.StatusUnauthorized)
	}

	c.Request.Body = ioutil.NopCloser(bytes.NewReader(contents))
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
