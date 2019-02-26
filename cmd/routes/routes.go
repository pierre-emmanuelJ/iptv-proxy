package routes

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/jamesnetherton/m3u"
)

type Configuration struct {
	Hostname string
	Port     int64
}

type ProxyPlaylist struct {
	playlist *m3u.Playlist
	track    *m3u.Track
	conf     *Configuration
}

// Serve the pfinder api
func Serve(playlist *m3u.Playlist, conf Configuration) error {
	router := gin.Default()
	router.Use(cors.Default())
	Routes(playlist, conf, router.Group("/"))

	return router.Run(fmt.Sprintf(":%d", conf.Port))
}

// Routes adds the routes for the app to the RouterGroup r
func Routes(playlist *m3u.Playlist, conf Configuration, r *gin.RouterGroup) {

	p := ProxyPlaylist{playlist, nil, &conf}

	r.GET("/m3u", p.getM3U)

	for i, track := range playlist.Tracks {
		oriURL, err := url.Parse(track.URI)
		if err != nil {
			return
		}
		tmp := &ProxyPlaylist{playlist, &playlist.Tracks[i], &conf}
		r.GET(oriURL.RequestURI(), tmp.reversProxy)
	}
}

func (p *ProxyPlaylist) reversProxy(c *gin.Context) {

	rpURL, err := url.Parse(p.track.URI)
	if err != nil {
		log.Fatal(err)
	}

	resp, err := http.Get(rpURL.String())
	if err != nil {
		log.Fatal(err)
	}

	c.Status(resp.StatusCode)
	c.Stream(func(w io.Writer) bool {
		io.Copy(w, resp.Body)
		return false
	})
}

func (p *ProxyPlaylist) getM3U(c *gin.Context) {
	result := "#EXTM3U\n"

	for _, track := range p.playlist.Tracks {
		result += "#EXTINF:"
		result += fmt.Sprintf("%d ", track.Length)
		for i := range track.Tags {
			if i == len(track.Tags)-1 {
				result += fmt.Sprintf("%s=%q,", track.Tags[i].Name, track.Tags[i].Value)
				continue
			}
			result += fmt.Sprintf("%s=%q ", track.Tags[i].Name, track.Tags[i].Value)
		}
		result += fmt.Sprintf("%s\n", track.Name)

		oriURL, err := url.Parse(track.URI)
		if err != nil {
			c.AbortWithError(http.StatusInternalServerError, err)
			return
		}

		destURL, err := url.Parse(fmt.Sprintf("http://%s:%d%s", p.conf.Hostname, p.conf.Port, oriURL.RequestURI()))
		if err != nil {
			c.AbortWithError(http.StatusInternalServerError, err)
			return
		}

		result += fmt.Sprintf("%s\n", destURL.String())
	}

	c.Data(http.StatusOK, "text/plain", []byte(result))
}
