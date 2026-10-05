package server

import (
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"html"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/pierre-emmanuelJ/iptv-proxy/pkg/config"
)

func TestMain(m *testing.M) {
	gin.SetMode(gin.TestMode)
	gin.DefaultWriter = io.Discard
	gin.DefaultErrorWriter = io.Discard
	os.Exit(m.Run())
}

const (
	xUser, xPass = "xuser", "xpass"
	user, pass   = "me", "secret"
	// a second account of the provider, allowed one stream at once
	x2User, x2Pass = "second", "pw+2"
)

// provider is a fake Xtream provider, with the quirks real ones have.
type provider struct {
	*httptest.Server
	// cdn is a second host the provider redirects HLS streams to.
	cdn *httptest.Server

	mu   sync.Mutex
	hits map[string]int
	// left counts, per path, the endless streams whose client went away.
	left       map[string]int
	userAgents []string
	queries    map[string]string
	// guideDelay is how long the provider takes to start sending its guide.
	guideDelay time.Duration
	// streamClosed is closed once the endless stream saw its client leave.
	streamClosed chan struct{}
	// down makes the API, playlists and guides fail, as a provider does now
	// and then.
	down atomic.Bool
	// garbage makes the API answer a page that is not JSON, with a 200.
	garbage atomic.Bool
	// late adds a news channel to the live streams, as a provider does now
	// and then.
	late atomic.Bool
	// revoked makes the provider refuse its second account.
	revoked atomic.Bool
	// liveDown makes live channel 1 of the first account fail.
	liveDown atomic.Bool
	// icons gives the guide images: a logo over http, a picture over https.
	icons atomic.Bool
}

func (p *provider) hit(r *http.Request) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.hits[r.URL.Path]++
	p.userAgents = append(p.userAgents, r.UserAgent())
	p.queries[r.URL.Path] = r.URL.RawQuery
}

func (p *provider) count(path string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.hits[path]
}

func (p *provider) leftCount(path string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.left[path]
}

// endless is a stream with no end and no length: it sends "tick" until its
// client goes away.
func (p *provider) endless(w http.ResponseWriter, r *http.Request) {
	p.hit(r)
	defer func() {
		p.mu.Lock()
		p.left[r.URL.Path]++
		p.mu.Unlock()
	}()
	for {
		if _, err := w.Write([]byte("tick")); err != nil {
			return
		}
		w.(http.Flusher).Flush()
		select {
		case <-r.Context().Done():
			return
		case <-time.After(5 * time.Millisecond):
		}
	}
}

func (p *provider) query(path string) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.queries[path]
}

func (p *provider) lastUserAgent() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.userAgents) == 0 {
		return ""
	}
	return p.userAgents[len(p.userAgents)-1]
}

func newProvider(t *testing.T) *provider {
	t.Helper()
	p := &provider{left: map[string]int{}, hits: map[string]int{}, queries: map[string]string{}, streamClosed: make(chan struct{})}

	cdn := http.NewServeMux()
	cdn.HandleFunc("/hlsr/tok/xuser/xpass/2/hash/2.m3u8", func(w http.ResponseWriter, r *http.Request) {
		p.hit(r)
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		fmt.Fprint(w, "#EXTM3U\n#EXTINF:10,\n/hlsr/tok/xuser/xpass/2/hash/seg1.ts\n")
	})
	cdn.HandleFunc("/hlsr/tok/xuser/xpass/2/hash/seg1.ts", func(w http.ResponseWriter, r *http.Request) {
		p.hit(r)
		fmt.Fprint(w, "segment-from-cdn")
	})
	// a session playlist whose segments are named by an absolute path, with
	// a query the segment cannot be fetched without
	cdn.HandleFunc("/session/abc/index", func(w http.ResponseWriter, r *http.Request) {
		p.hit(r)
		fmt.Fprint(w, "#EXTM3U\n#EXTINF:10,\n/play/hls/tok==/chan/seg_1.ts?h=1&r=2\n")
	})
	cdn.HandleFunc("/play/hls/tok==/chan/seg_1.ts", func(w http.ResponseWriter, r *http.Request) {
		p.hit(r)
		fmt.Fprint(w, "segment-play")
	})
	// a master playlist: variants, an alternate audio, then segments and a key
	cdn.HandleFunc("/master.m3u8", func(w http.ResponseWriter, r *http.Request) {
		p.hit(r)
		fmt.Fprint(w, "#EXTM3U\n"+
			"#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID=\"a\",NAME=\"English\",URI=\"audio/en.m3u8\"\n"+
			"#EXT-X-STREAM-INF:BANDWIDTH=640996,AUDIO=\"a\"\n"+
			"variant/v1.m3u8\n")
	})
	cdn.HandleFunc("/variant/v1.m3u8", func(w http.ResponseWriter, r *http.Request) {
		p.hit(r)
		fmt.Fprint(w, "#EXTM3U\n#EXT-X-KEY:METHOD=AES-128,URI=\"../key.bin\"\n#EXTINF:6,\nseg_a.ts\n")
	})
	cdn.HandleFunc("/variant/seg_a.ts", func(w http.ResponseWriter, r *http.Request) {
		p.hit(r)
		fmt.Fprint(w, "segment-a")
	})
	cdn.HandleFunc("/key.bin", func(w http.ResponseWriter, r *http.Request) {
		p.hit(r)
		fmt.Fprint(w, "the-key")
	})
	cdn.HandleFunc("/audio/en.m3u8", func(w http.ResponseWriter, r *http.Request) {
		p.hit(r)
		fmt.Fprint(w, "#EXTM3U\n#EXTINF:6,\nen_1.aac\n")
	})
	cdn.HandleFunc("/img/", func(w http.ResponseWriter, r *http.Request) {
		p.hit(r)
		w.Header().Set("Content-Type", "image/jpeg")
		fmt.Fprint(w, "jpeg:"+path.Base(r.URL.Path))
	})
	p.cdn = httptest.NewServer(cdn)
	t.Cleanup(p.cdn.Close)

	mux := http.NewServeMux()
	authorized := func(w http.ResponseWriter, r *http.Request) bool {
		p.hit(r)
		if p.down.Load() {
			w.WriteHeader(http.StatusBadGateway)
			fmt.Fprint(w, "<html>502 Bad Gateway</html>")
			return false
		}
		if p.garbage.Load() {
			fmt.Fprint(w, "<html>Down for maintenance</html>")
			return false
		}
		switch q := r.URL.Query(); {
		case q.Get("username") == xUser && q.Get("password") == xPass:
			return true
		case q.Get("username") == x2User && q.Get("password") == x2Pass && !p.revoked.Load():
			return true
		case q.Get("username") == "empty":
			// others with an empty list
			fmt.Fprint(w, `[]`)
			return false
		case q.Get("username") == "locked":
			// some providers refuse with a status
			w.WriteHeader(http.StatusUnauthorized)
			return false
		}
		fmt.Fprint(w, `{"user_info":{"auth":0}}`)
		return false
	}

	mux.HandleFunc("/player_api.php", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(w, r) {
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		base := strings.ReplaceAll(p.URL, "/", `\/`)
		switch r.URL.Query().Get("action") {
		case "":
			connections := "2"
			if r.URL.Query().Get("username") == x2User {
				connections = "1"
			}
			fmt.Fprintf(w, `{"user_info":{"username":%q,"password":%q,"message":"","auth":1,"status":"Active","exp_date":null,"is_trial":"0","active_cons":0,"created_at":"1700000000","max_connections":%q,"allowed_output_formats":["m3u8","ts","rtmp"]},"server_info":{"url":"provider.example","port":"80","https_port":"443","server_protocol":"http","rtmp_port":"8880","timezone":"Europe\/Paris","timestamp_now":1759400000,"time_now":"2025-10-02 12:00:00","process":true}}`,
				r.URL.Query().Get("username"), r.URL.Query().Get("password"), connections)
		case "get_live_categories":
			// ids as strings here, as numbers in the streams
			fmt.Fprint(w, `[{"category_id":"10","category_name":"News","parent_id":0},{"category_id":"20","category_name":"Sport \"HD\"","parent_id":0}`)
			if r.URL.Query().Get("username") == x2User {
				// the second account has a package of its own
				fmt.Fprint(w, `,{"category_id":"11","category_name":"News"}`)
			}
			fmt.Fprint(w, `]`)
		case "get_live_streams":
			fmt.Fprint(w, `[{"num":1,"name":"One","stream_type":"live","stream_id":1,"stream_icon":"http:\/\/logos.example\/one.png","epg_channel_id":"one.fr","added":"1700000000","category_id":10,"tv_archive":0,"direct_source":""},`+
				`{"num":"2","name":"Two","stream_id":"2","stream_icon":"","epg_channel_id":null,"category_id":"20"},`+
				`{"num":3,"name":"Lost","stream_id":3,"category_id":"99"}`)
			if p.late.Load() {
				fmt.Fprint(w, `,{"num":4,"name":"Late","stream_id":4,"category_id":"10","epg_channel_id":"one.fr"}`)
			}
			if r.URL.Query().Get("username") == x2User {
				fmt.Fprint(w, `,{"num":5,"name":"Local news","stream_id":5,"category_id":"11","category_ids":[11,20]}`)
			}
			fmt.Fprint(w, `]`)
		case "get_vod_categories":
			fmt.Fprint(w, `[{"category_id":"30","category_name":"Films"}]`)
		case "get_vod_streams":
			fmt.Fprint(w, `[{"num":1,"name":"A film","stream_type":"movie","stream_id":12,"stream_icon":"http:\/\/img.example\/film.jpg","category_id":"30","container_extension":"mkv"},`+
				`{"name":"No format","stream_id":"13","category_id":30}]`)
		case "get_vod_info":
			// a number where others send a string, an object where others
			// send an array, and the provider's credentials in an address
			fmt.Fprintf(w, `{"info":{"tmdb_id":550,"duration_secs":"8340","backdrop_path":"https:\/\/img.example\/b.jpg","rating":7.9,"video":[]},"movie_data":{"stream_id":"12","added":1700000000,"container_extension":"mkv","direct_source":"%s\/movie\/xuser\/xpass\/12.mkv"}}`, base)
		case "get_short_epg":
			// titles that are not valid base64 must not be an error
			fmt.Fprint(w, `{"epg_listings":[{"id":"1","title":"not base64 !!","start":"2025-10-02 20:00:00","start_timestamp":"1759428000","now_playing":0}]}`)
		case "get_series_info":
			// images on another host, one in a list
			cdn := strings.ReplaceAll(p.cdn.URL, "/", `\/`)
			fmt.Fprintf(w, `{"info":{"name":"Show","cover":"%[1]s\/img\/cover.jpg","backdrop_path":["%[1]s\/img\/back.jpg"]},"episodes":{}}`, cdn)
		case "get_series":
			fmt.Fprint(w, `{"1":{"series_id":7,"name":"Show"}}`) // an object instead of an array
		case "echo":
			// an error page repeating the address it was asked
			w.Header().Set("Content-Type", "text/html")
			w.Header().Set("Link", "<"+r.URL.RequestURI()+">; rel=self")
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprintf(w, "<html>404: %s<br>%s</html>", r.URL.RequestURI(), html.EscapeString(r.URL.RequestURI()))
		case "where":
			// an error page naming the provider's own address
			w.Header().Set("Link", "<"+p.URL+"/help>; rel=help")
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprintf(w, "<html>See %s/help</html>", p.URL)
		case "broken":
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, `<html>Fatal error</html>`)
		default:
			fmt.Fprint(w, `[]`)
		}
	})

	mux.HandleFunc("/get.php", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(w, r) {
			return
		}
		if r.URL.Query().Get("type") == "hosted" {
			// an attribute naming the provider, without credentials
			fmt.Fprintf(w, "#EXTM3U\n#EXTINF:-1 tvg-logo=\"%[1]s/logos/one.png\" group-title=\"News\",One\n%[1]s/live/%[2]s/%[3]s/1.ts\n", p.URL, r.URL.Query().Get("username"), r.URL.Query().Get("password"))
			return
		}
		if r.URL.Query().Get("type") == "forbidden" {
			fmt.Fprintf(w, `<html>No %s/%s for you</html>`, xUser, xPass)
			return
		}
		fmt.Fprintf(w, "#EXTM3U url-tvg=\"%[1]s/xmltv.php?username=xuser&password=xpass\"\n"+
			"#EXTINF:-1 tvg-id=\"\" tvg-name=\"One\" group-title=\"News\" catchup=\"default\" catchup-source=\"%[1]s/timeshift/xuser/xpass/{duration}/{start}/1.ts\",One\n%[1]s/live/xuser/xpass/1.ts\n"+
			"#EXTINF:-1 tvg-id=\"film.fr\",A film, with a comma\n%[1]s/movie/xuser/xpass/12.mkv\n", p.URL)
	})

	mux.HandleFunc("/xmltv.php", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(w, r) {
			return
		}
		if r.URL.Query().Get("fail") != "" {
			w.WriteHeader(http.StatusServiceUnavailable)
			fmt.Fprintf(w, "<html>Busy: %s</html>", r.URL.RequestURI())
			return
		}
		time.Sleep(p.guideDelay) // a large guide takes a while to generate
		if r.URL.Query().Get("cut") != "" {
			// a guide whose connection breaks halfway
			w.Header().Set("Content-Length", fmt.Sprint(len(providerGuide)))
			fmt.Fprint(w, providerGuide[:len(providerGuide)/2])
			conn, _, _ := w.(http.Hijacker).Hijack()
			conn.Close() // nolint: errcheck
			return
		}
		if r.URL.Query().Get("gzip") != "" {
			// a guide some providers send compressed, unasked
			w.Header().Set("Content-Type", "application/gzip")
			zw := gzip.NewWriter(w)
			fmt.Fprint(zw, providerGuide)
			zw.Close() // nolint: errcheck
			return
		}
		w.Header().Set("Content-Type", "text/xml")
		if r.URL.Query().Get("username") == x2User {
			// the second account's package has a channel of its own
			fmt.Fprint(w, strings.Replace(providerGuide, "</tv>", `  <channel id="local.fr"><display-name>Local</display-name></channel>
  <programme start="20251002200000 +0200" channel="local.fr"><title>Local news</title></programme>
</tv>`, 1))
			return
		}
		if p.icons.Load() {
			fmt.Fprint(w, strings.NewReplacer(
				`<display-name>One</display-name>`, `<display-name>One</display-name><icon src="`+p.cdn.URL+`/img/one.png?a=1&amp;b=2" />`,
				`<title>Match</title>`, `<title>Match</title><icon src='https://pictures.example/match.jpg'/>`,
			).Replace(providerGuide))
			return
		}
		fmt.Fprint(w, providerGuide)
	})

	mux.HandleFunc("/live/xuser/xpass/1.ts", func(w http.ResponseWriter, r *http.Request) {
		p.hit(r)
		if p.liveDown.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Keep-Alive", "timeout=5")
		w.Header().Set("X-Got-Range", r.Header.Get("Range"))
		w.Header().Set("X-Got-Connection", r.Header.Get("Connection"))
		fmt.Fprint(w, "live-one")
	})
	mux.HandleFunc("/live/xuser/xpass/4.ts", func(w http.ResponseWriter, r *http.Request) {
		p.hit(r)
		fmt.Fprint(w, "live-late")
	})
	mux.HandleFunc("/xuser/xpass/1", func(w http.ResponseWriter, r *http.Request) {
		p.hit(r)
		fmt.Fprint(w, "short-form")
	})
	mux.HandleFunc("/movie/xuser/xpass/12.mkv", func(w http.ResponseWriter, r *http.Request) {
		p.hit(r)
		w.Header().Set("Content-Range", "bytes 2-5/100")
		w.WriteHeader(http.StatusPartialContent)
		fmt.Fprint(w, "film")
	})
	mux.HandleFunc("/live/xuser/xpass/slow.ts", func(w http.ResponseWriter, r *http.Request) {
		p.hit(r)
		select {
		case <-time.After(400 * time.Millisecond):
		case <-r.Context().Done():
		}
	})
	mux.HandleFunc("/live/xuser/xpass/missing.ts", func(w http.ResponseWriter, r *http.Request) {
		p.hit(r)
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprintf(w, "<html>No such stream: %s</html>", r.URL.Path)
	})
	mux.HandleFunc("/series/xuser/xpass/5.mp4", func(w http.ResponseWriter, r *http.Request) {
		p.hit(r)
		fmt.Fprint(w, "episode")
	})
	mux.HandleFunc("/timeshift/xuser/xpass/60/2025-10-02:20-00/1.ts", func(w http.ResponseWriter, r *http.Request) {
		p.hit(r)
		fmt.Fprint(w, "catch-up")
	})
	// HLS: redirected to the CDN, served directly, and redirected elsewhere
	mux.HandleFunc("/live/xuser/xpass/2.m3u8", func(w http.ResponseWriter, r *http.Request) {
		p.hit(r)
		http.Redirect(w, r, p.cdn.URL+"/hlsr/tok/xuser/xpass/2/hash/2.m3u8", http.StatusFound)
	})
	mux.HandleFunc("/live/xuser/xpass/3.m3u8", func(w http.ResponseWriter, r *http.Request) {
		p.hit(r)
		if r.Header.Get("Accept-Encoding") != "" {
			// a compressed playlist could not be rewritten
			http.Error(w, "asked for a compressed playlist", http.StatusNotAcceptable)
			return
		}
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		playlist := fmt.Sprintf("#EXTM3U\n#EXTINF:10,\n3_1.ts\n#EXTINF:10,\n%s/live/xuser/xpass/3_2.ts\n", p.URL)
		w.Header().Set("Etag", `"original"`)
		if r.Header.Get("Range") != "" {
			// players ask "bytes=0-": the answer describes the original bytes
			w.Header().Set("Accept-Ranges", "bytes")
			w.Header().Set("Content-Range", fmt.Sprintf("bytes 0-%d/%d", len(playlist)-1, len(playlist)))
			w.Header().Set("Content-Length", fmt.Sprint(len(playlist)))
			w.WriteHeader(http.StatusPartialContent)
		}
		fmt.Fprint(w, playlist)
	})
	mux.HandleFunc("/live/xuser/xpass/3_1.ts", func(w http.ResponseWriter, r *http.Request) {
		p.hit(r)
		fmt.Fprint(w, "segment-3-1")
	})
	mux.HandleFunc("/live/xuser/xpass/4.m3u8", func(w http.ResponseWriter, r *http.Request) {
		p.hit(r)
		http.Redirect(w, r, p.cdn.URL+"/session/abc/index", http.StatusFound)
	})
	mux.HandleFunc("/live/second/pw+2/1.ts", func(w http.ResponseWriter, r *http.Request) {
		p.hit(r)
		fmt.Fprint(w, "second-one")
	})
	mux.HandleFunc("/live/second/pw+2/5.ts", func(w http.ResponseWriter, r *http.Request) {
		p.hit(r)
		fmt.Fprint(w, "second-five")
	})
	// the second account's other streams: files, and an error page that
	// repeats the address asked
	for _, prefix := range []string{"/movie/second/pw+2/", "/series/second/pw+2/", "/timeshift/second/pw+2/"} {
		mux.HandleFunc(prefix, func(w http.ResponseWriter, r *http.Request) {
			p.hit(r)
			fmt.Fprint(w, "second:"+strings.TrimPrefix(r.URL.Path, prefix))
		})
	}
	mux.HandleFunc("/live/second/pw+2/", func(w http.ResponseWriter, r *http.Request) {
		p.hit(r)
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprintf(w, "<html>No such stream: %s</html>", r.URL.Path)
	})
	for _, path := range []string{"/live/xuser/xpass/8.ts", "/live/second/pw+2/8.ts", "/movie/xuser/xpass/endless.mkv", "/stream/endless", "/stream/endless.mp4"} {
		mux.HandleFunc(path, p.endless)
	}
	// A file being read: endless for the test's purpose, but with a length.
	mux.HandleFunc("/live/xuser/xpass/sized.ts", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "1073741824")
		p.endless(w, r)
	})
	// A stream the provider drops after a few bytes, every time.
	mux.HandleFunc("/live/xuser/xpass/dropped.ts", func(w http.ResponseWriter, r *http.Request) {
		p.hit(r)
		w.(http.Flusher).Flush() // no length: a live stream
		fmt.Fprintf(w, "[part %d]", p.count(r.URL.Path))
	})
	// A live stream that never ends.
	mux.HandleFunc("/live/xuser/xpass/9.ts", func(w http.ResponseWriter, r *http.Request) {
		p.hit(r)
		for {
			if _, err := w.Write([]byte("tick")); err != nil {
				break
			}
			w.(http.Flusher).Flush()
			select {
			case <-r.Context().Done():
				close(p.streamClosed)
				return
			case <-time.After(5 * time.Millisecond):
			}
		}
		close(p.streamClosed)
	})

	// A plain M3U playlist naming its provider's credentials outside of the
	// track addresses.
	mux.HandleFunc("/leaky.m3u", func(w http.ResponseWriter, r *http.Request) {
		p.hit(r)
		fmt.Fprintf(w, "#EXTM3U url-tvg=\"%[1]s/epg.php?username=xuser&password=xpass\" x-tvg-url=\"http://guide.example/epg.xml\"\n"+
			"#EXTINF:-1 tvg-logo=\"http://logos.example/one.png\" catchup-source=\"%[1]s/catchup/xuser/xpass/{start}.ts\" group-title=\"News\",One\n"+
			"#EXTVLCOPT:http-referrer=%[1]s/portal?password=xpass\n"+
			"#EXTVLCOPT:http-user-agent=Player\n"+
			"%[1]s/stream/a.ts\n", p.URL)
	})

	// Plain M3U provider
	mux.HandleFunc("/list.m3u", func(w http.ResponseWriter, r *http.Request) {
		p.hit(r)
		if p.down.Load() {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		fmt.Fprintf(w, "#EXTM3U url-tvg=\"%[1]s/epg.xml\"\n"+
			"#EXTINF:-1 tvg-id=\"\" tvg-logo=\"data:image/png;base64,AAAA\" group-title=\"News\",One, the first\n"+
			"#EXTGRP:News\n"+
			"%[1]s/stream/a.ts?token=1\n"+
			"#EXTINF:-1,Broken\nhttp://%%zz/broken\n"+
			"#EXTINF:-1,Two\n%[1]s/hls/b.m3u8?token=2\n"+
			"#EXTINF:-1,Three\n%[1]s/redirected/c.m3u8\n", p.URL)
	})
	mux.HandleFunc("/epg.xml", func(w http.ResponseWriter, r *http.Request) {
		p.hit(r)
		if p.down.Load() {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "text/xml")
		fmt.Fprint(w, providerGuide)
	})
	mux.HandleFunc("/redirected/c.m3u8", func(w http.ResponseWriter, r *http.Request) {
		p.hit(r)
		http.Redirect(w, r, p.cdn.URL+"/master.m3u8", http.StatusFound)
	})
	mux.HandleFunc("/stream/a.ts", func(w http.ResponseWriter, r *http.Request) {
		p.hit(r)
		fmt.Fprint(w, "track-a")
	})
	mux.HandleFunc("/hls/b.m3u8", func(w http.ResponseWriter, r *http.Request) {
		p.hit(r)
		fmt.Fprint(w, "#EXTM3U\n#EXTINF:10,\nb_1.ts\n")
	})
	mux.HandleFunc("/hls/b_1.ts", func(w http.ResponseWriter, r *http.Request) {
		p.hit(r)
		fmt.Fprint(w, "segment-b-1")
	})

	// anything else is counted too, and not found
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		p.hit(r)
		http.NotFound(w, r)
	})

	p.Server = httptest.NewServer(mux)
	t.Cleanup(p.Close)
	return p
}

// providerGuide is the provider's guide: two channels, their programmes,
// and markup in a description.
const providerGuide = `<?xml version="1.0" encoding="UTF-8"?>
<tv generator-info-name="provider">
  <channel id="one.fr"><display-name>One</display-name></channel>
  <channel id="sport.fr"><display-name>Sport</display-name></channel>
  <programme start="20251002200000 +0200" channel="one.fr"><title>News</title><desc><![CDATA[<b>live</b>]]></desc></programme>
  <programme start="20251002200000 +0200" channel="sport.fr"><title>Match</title></programme>
</tv>
`

// proxy starts the proxy in front of p and returns its address.
func proxy(t *testing.T, p *provider, mutate func(*config.ProxyConfig)) string {
	t.Helper()
	remote, _ := url.Parse("")
	conf := &config.ProxyConfig{
		HostConfig:         &config.HostConfiguration{Hostname: "proxy.example", Port: 8080},
		AdvertisedPort:     8080,
		XtreamBaseURL:      p.URL,
		XtreamUser:         xUser,
		XtreamPassword:     xPass,
		User:               user,
		Password:           pass,
		M3UFileName:        "iptv.m3u",
		M3UCacheExpiration: 1,
		RemoteURL:          remote,
		CustomId:           "tracks",
	}
	if mutate != nil {
		mutate(conf)
	}

	c, err := NewServer(conf)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(c.Handler())
	t.Cleanup(srv.Close)
	return srv.URL
}

func get(t *testing.T, rawURL string, header ...string) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i+1 < len(header); i += 2 {
		req.Header.Set(header[i], header[i+1])
	}
	// redirects are part of what is tested
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() // nolint: errcheck
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp, string(body)
}

const creds = "username=" + user + "&password=" + pass

// addresses returns what an HLS playlist asks the player to fetch: its
// address lines and the URI attributes of its tags.
func addresses(playlist string) []string {
	var out []string
	for _, line := range strings.Split(playlist, "\n") {
		line = strings.TrimSpace(line)
		if i := strings.Index(line, `URI="`); strings.HasPrefix(line, "#") && i >= 0 {
			rest := line[i+5:]
			out = append(out, rest[:strings.IndexByte(rest, '"')])
		} else if line != "" && !strings.HasPrefix(line, "#") {
			out = append(out, line)
		}
	}
	return out
}

// hlsAddresses checks that a playlist only names the proxy, and returns
// its addresses.
func hlsAddresses(t *testing.T, playlist, prefix string) []string {
	t.Helper()
	refs := addresses(playlist)
	if len(refs) == 0 {
		t.Fatalf("no address in the playlist:\n%s", playlist)
	}
	for _, ref := range refs {
		if !strings.HasPrefix(ref, prefix+"/hls/") {
			t.Fatalf("%q is not served by the proxy, in:\n%s", ref, playlist)
		}
	}
	return refs
}

// lockedBuffer collects what concurrent handlers log.
type lockedBuffer struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// captureLogs collects everything the proxy logs until the test ends. It
// must be called before the proxy is started.
func captureLogs(t *testing.T) *lockedBuffer {
	t.Helper()
	logs := &lockedBuffer{}
	ginOut, ginErr, stdOut := gin.DefaultWriter, gin.DefaultErrorWriter, log.Writer()
	gin.DefaultWriter, gin.DefaultErrorWriter = logs, logs
	log.SetOutput(logs)
	t.Cleanup(func() {
		gin.DefaultWriter, gin.DefaultErrorWriter = ginOut, ginErr
		log.SetOutput(stdOut)
	})
	return logs
}

func noProviderCredentials(t *testing.T, what, body string) {
	t.Helper()
	for _, secret := range []string{xUser, xPass} {
		if strings.Contains(body, secret) {
			t.Errorf("%s: the provider's %q reached the client:\n%s", what, secret, body)
		}
	}
}

// --- Xtream API ---

func TestLoginAnswerIsTheProxys(t *testing.T) {
	p := newProvider(t)
	base := proxy(t, p, nil)

	resp, body := get(t, base+"/player_api.php?"+creds)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d: %s", resp.StatusCode, body)
	}
	noProviderCredentials(t, "login", body)
	for _, want := range []string{
		`"username":"me"`, `"password":"secret"`,
		`"url":"http://proxy.example"`, `"port":"8080"`, `"https_port":"8080"`, `"server_protocol":"http"`,
		// the account's state is the provider's, untouched
		`"exp_date":null`, `"max_connections":"2"`, `"active_cons":0`, `"allowed_output_formats":["m3u8","ts","rtmp"]`, `"timestamp_now":1759400000`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("login answer lacks %s:\n%s", want, body)
		}
	}
}

func TestAPIAnswersArePassedOnAsTheyAre(t *testing.T) {
	p := newProvider(t)
	base := proxy(t, p, nil)

	for action, want := range map[string]string{
		"get_live_categories": `[{"category_id":"10","category_name":"News","parent_id":0},{"category_id":"20","category_name":"Sport \"HD\"","parent_id":0}]`,
		"get_short_epg":       `{"epg_listings":[{"id":"1","title":"not base64 !!","start":"2025-10-02 20:00:00","start_timestamp":"1759428000","now_playing":0}]}`,
		"get_series":          `{"1":{"series_id":7,"name":"Show"}}`,
		"anything_new":        `[]`,
	} {
		resp, body := get(t, base+"/player_api.php?"+creds+"&action="+action+"&stream_id=1")
		if resp.StatusCode != http.StatusOK || body != want {
			t.Errorf("%s: status %d, body:\n%s\nwant:\n%s", action, resp.StatusCode, body, want)
		}
		if ct := resp.Header.Get("Content-Type"); ct != "application/json; charset=utf-8" {
			t.Errorf("%s: content type = %q", action, ct)
		}
	}
	if q := p.query("/player_api.php"); !strings.Contains(q, "username="+xUser) || !strings.Contains(q, "stream_id=1") || strings.Contains(q, user+"&") {
		t.Errorf("provider was asked %q", q)
	}
}

func TestAPIStreamAddressesPointToTheProxy(t *testing.T) {
	p := newProvider(t)
	base := proxy(t, p, nil)

	_, body := get(t, base+"/player_api.php?"+creds+"&action=get_vod_info&vod_id=12")
	noProviderCredentials(t, "get_vod_info", body)
	if want := `"direct_source":"http:\/\/proxy.example:8080\/movie\/me\/secret\/12.mkv"`; !strings.Contains(body, want) {
		t.Errorf("address not rewritten, want %s in:\n%s", want, body)
	}
	if want := `"tmdb_id":550,"duration_secs":"8340"`; !strings.Contains(body, want) {
		t.Errorf("the rest of the answer changed:\n%s", body)
	}
}

func TestAPIProviderErrorIsPassedOn(t *testing.T) {
	p := newProvider(t)
	base := proxy(t, p, nil)

	resp, body := get(t, base+"/player_api.php?"+creds+"&action=broken")
	if resp.StatusCode != http.StatusInternalServerError || !strings.Contains(body, "Fatal error") {
		t.Errorf("status %d, body %q", resp.StatusCode, body)
	}
}

// A provider's error page may repeat the address it was asked: its
// credentials are in it, in an order and a form of its own.
func TestProviderErrorPagesDoNotCarryItsCredentials(t *testing.T) {
	p := newProvider(t)
	base := proxy(t, p, nil)

	for path, want := range map[string]int{
		"/player_api.php?" + creds + "&action=echo&stream_id=1&limit=2": http.StatusNotFound,
		"/live/me/secret/missing.ts":                                    http.StatusNotFound,
		"/xmltv.php?" + creds + "&fail=1":                               http.StatusServiceUnavailable,
	} {
		resp, body := get(t, base+path)
		if resp.StatusCode != want {
			t.Errorf("%s: status = %d, want the provider's %d", path, resp.StatusCode, want)
		}
		noProviderCredentials(t, path, body)
		if !strings.Contains(body, "<html>") || !strings.Contains(body, pass) {
			t.Errorf("%s: the page should come through, with the proxy's credentials in place of the provider's:\n%s", path, body)
		}
		if cl := resp.Header.Get("Content-Length"); cl != fmt.Sprint(len(body)) {
			t.Errorf("%s: content length = %s for %d bytes", path, cl, len(body))
		}
		for name, values := range resp.Header {
			noProviderCredentials(t, path+" header "+name, strings.Join(values, " "))
		}
	}
}

func TestAPIByPost(t *testing.T) {
	p := newProvider(t)
	base := proxy(t, p, nil)

	resp, err := http.PostForm(base+"/player_api.php", url.Values{"username": {user}, "password": {pass}, "action": {"get_live_categories"}})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() // nolint: errcheck
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"category_name":"News"`) {
		t.Errorf("status %d, body %s", resp.StatusCode, body)
	}
}

func TestAuthentication(t *testing.T) {
	p := newProvider(t)
	base := proxy(t, p, nil)

	for name, c := range map[string]struct {
		query string
		want  int
	}{
		"no credentials":           {"", http.StatusBadRequest},
		"wrong password":           {"username=me&password=nope", http.StatusUnauthorized},
		"wrong user":               {"username=you&password=secret", http.StatusUnauthorized},
		"the provider's own":       {"username=xuser&password=xpass", http.StatusUnauthorized},
		"right ones":               {creds, http.StatusOK},
		"prefix of the password":   {"username=me&password=secre", http.StatusUnauthorized},
		"password with more after": {"username=me&password=secretx", http.StatusUnauthorized},
	} {
		for _, endpoint := range []string{"/player_api.php", "/get.php", "/xmltv.php", "/apiget"} {
			if resp, _ := get(t, base+endpoint+"?"+c.query); resp.StatusCode != c.want {
				t.Errorf("%s on %s: status = %d, want %d", name, endpoint, resp.StatusCode, c.want)
			}
		}
	}
	if resp, _ := get(t, base+"/live/me/wrong/1.ts"); resp.StatusCode != http.StatusNotFound {
		t.Errorf("stream with a wrong password: status = %d", resp.StatusCode)
	}
	if n := p.count("/live/xuser/xpass/1.ts"); n != 0 {
		t.Errorf("the provider was asked %d times for a refused request", n)
	}
}

// --- Xtream playlists and guide ---

func TestGetPhpPlaylist(t *testing.T) {
	p := newProvider(t)
	base := proxy(t, p, nil)

	resp, body := get(t, base+"/get.php?"+creds+"&type=m3u_plus&output=ts")
	// the guide and catch-up addresses are the proxy's too
	want := "#EXTM3U url-tvg=\"http://proxy.example:8080/xmltv.php?username=me&password=secret\"\n" +
		"#EXTINF:-1 tvg-id=\"\" tvg-name=\"One\" group-title=\"News\" catchup=\"default\" catchup-source=\"http://proxy.example:8080/timeshift/me/secret/{duration}/{start}/1.ts\",One\nhttp://proxy.example:8080/live/me/secret/1.ts\n" +
		"#EXTINF:-1 tvg-id=\"film.fr\",A film, with a comma\nhttp://proxy.example:8080/movie/me/secret/12.mkv\n"
	if resp.StatusCode != http.StatusOK || body != want {
		t.Fatalf("status %d, playlist:\n%s\nwant:\n%s", resp.StatusCode, body, want)
	}
	if cd := resp.Header.Get("Content-Disposition"); cd != `attachment; filename="iptv.m3u"` {
		t.Errorf("content disposition = %q", cd)
	}
	if q := p.query("/get.php"); !strings.Contains(q, "type=m3u_plus") || !strings.Contains(q, "output=ts") {
		t.Errorf("provider was asked %q", q)
	}

	// kept for the configured time, per set of parameters
	get(t, base+"/get.php?"+creds+"&type=m3u_plus&output=ts")
	if n := p.count("/get.php"); n != 1 {
		t.Errorf("provider asked %d times for the same playlist, want 1", n)
	}
	get(t, base+"/get.php?"+creds+"&type=m3u&output=hls")
	if n := p.count("/get.php"); n != 2 {
		t.Errorf("provider asked %d times after other parameters, want 2", n)
	}
}

func TestGetPhpProviderRefusal(t *testing.T) {
	logs := captureLogs(t)
	p := newProvider(t)
	base := proxy(t, p, nil)

	resp, body := get(t, base+"/get.php?"+creds+"&type=forbidden")
	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("status = %d, want 502", resp.StatusCode)
	}
	noProviderCredentials(t, "refused playlist", body)
	// the provider's page names its credentials: it stays out of the logs too
	noProviderCredentials(t, "logs", logs.String())
	if !strings.Contains(logs.String(), errNotAPlaylist.Error()) {
		t.Errorf("the refusal is not logged:\n%s", logs.String())
	}
}

func TestPlaylistFromTheAPI(t *testing.T) {
	p := newProvider(t)
	base := proxy(t, p, nil)

	_, body := get(t, base+"/apiget?"+creds+"&output=ts")
	want := "#EXTM3U\n" +
		"#EXTINF:-1 tvg-id=\"one.fr\" tvg-name=\"One\" tvg-logo=\"http://logos.example/one.png\" group-title=\"News\",One\nhttp://proxy.example:8080/live/me/secret/1.ts\n" +
		"#EXTINF:-1 tvg-name=\"Two\" group-title=\"Sport 'HD'\",Two\nhttp://proxy.example:8080/live/me/secret/2.ts\n" +
		"#EXTINF:-1 tvg-name=\"Lost\",Lost\nhttp://proxy.example:8080/live/me/secret/3.ts\n"
	if body != want {
		t.Errorf("playlist:\n%s\nwant:\n%s", body, want)
	}

	_, short := get(t, base+"/apiget?"+creds)
	if !strings.Contains(short, "\nhttp://proxy.example:8080/me/secret/1\n") {
		t.Errorf("without an output format, the short stream form is expected:\n%s", short)
	}
}

// Movies are added on request only: a large catalogue makes a playlist some
// players cannot load.
func TestPlaylistFromTheAPIWithMovies(t *testing.T) {
	p := newProvider(t)

	base := proxy(t, p, nil)
	if _, body := get(t, base+"/apiget?"+creds+"&output=ts"); strings.Contains(body, "/movie/") {
		t.Errorf("movies in the playlist without being asked:\n%s", body)
	}

	base = proxy(t, p, func(c *config.ProxyConfig) { c.XtreamApiGetMovies = true })
	_, body := get(t, base+"/apiget?"+creds+"&output=ts")
	want := "#EXTM3U\n" +
		"#EXTINF:-1 tvg-id=\"one.fr\" tvg-name=\"One\" tvg-logo=\"http://logos.example/one.png\" group-title=\"News\",One\nhttp://proxy.example:8080/live/me/secret/1.ts\n" +
		"#EXTINF:-1 tvg-name=\"Two\" group-title=\"Sport 'HD'\",Two\nhttp://proxy.example:8080/live/me/secret/2.ts\n" +
		"#EXTINF:-1 tvg-name=\"Lost\",Lost\nhttp://proxy.example:8080/live/me/secret/3.ts\n" +
		// a movie keeps its own format, whatever the one asked for live
		"#EXTINF:-1 tvg-name=\"A film\" tvg-logo=\"http://img.example/film.jpg\" group-title=\"Films\",A film\nhttp://proxy.example:8080/movie/me/secret/12.mkv\n" +
		"#EXTINF:-1 tvg-name=\"No format\" group-title=\"Films\",No format\nhttp://proxy.example:8080/movie/me/secret/13.mp4\n"
	if body != want {
		t.Errorf("playlist:\n%s\nwant:\n%s", body, want)
	}

	// and the address it gives plays
	if resp, film := get(t, base+"/movie/me/secret/12.mkv"); resp.StatusCode != http.StatusPartialContent || film != "film" {
		t.Errorf("movie: status %d, body %q", resp.StatusCode, film)
	}
}

// With an Xtream account and no playlist of its own, the playlist's usual
// name serves the account's playlist rather than an empty one.
func TestXtreamAccountAloneServesItsPlaylistUnderTheUsualName(t *testing.T) {
	p := newProvider(t)

	base := proxy(t, p, nil)
	resp, body := get(t, base+"/iptv.m3u?"+creds+"&type=m3u_plus&output=ts")
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "http://proxy.example:8080/live/me/secret/1.ts") || !strings.Contains(body, "A film, with a comma") {
		t.Errorf("status %d, playlist (want the provider's get.php):\n%s", resp.StatusCode, body)
	}
	if resp, _ := get(t, base+"/iptv.m3u?username=me&password=nope"); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("wrong password: status = %d", resp.StatusCode)
	}

	base = proxy(t, p, func(c *config.ProxyConfig) { c.XtreamGenerateApiGet = true })
	if _, body := get(t, base+"/iptv.m3u?"+creds+"&output=ts"); !strings.Contains(body, `tvg-id="one.fr"`) || strings.Contains(body, "A film, with a comma") {
		t.Errorf("with the generated playlist asked for, want it under the usual name too:\n%s", body)
	}
}

func TestXtreamPlaylistIsNotFetchedAtStartup(t *testing.T) {
	p := newProvider(t)
	base := proxy(t, p, func(c *config.ProxyConfig) {
		c.RemoteURL, _ = url.Parse(p.URL + "/get.php?username=" + xUser + "&password=" + xPass + "&type=m3u_plus&output=ts")
	})
	if n := p.count("/get.php"); n != 0 {
		t.Fatalf("the provider was asked for its playlist %d times before any client", n)
	}

	resp, body := get(t, base+"/iptv.m3u?"+creds)
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "http://proxy.example:8080/live/me/secret/1.ts") {
		t.Errorf("status %d, playlist:\n%s", resp.StatusCode, body)
	}
	if q := p.query("/get.php"); !strings.Contains(q, "type=m3u_plus") || !strings.Contains(q, "output=ts") {
		t.Errorf("the parameters of the configured address were lost: %q", q)
	}
}

func TestGuideIsPassedOn(t *testing.T) {
	p := newProvider(t)
	base := proxy(t, p, nil)

	resp, body := get(t, base+"/xmltv.php?"+creds)
	if resp.StatusCode != http.StatusOK || body != providerGuide {
		t.Errorf("status %d, guide: %s", resp.StatusCode, body)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/xml" {
		t.Errorf("content type = %q", ct)
	}
}

// A guide or a playlist is generated on request and may take minutes; a
// stream that does not start is given up on quickly.
func TestProviderSlowToAnswer(t *testing.T) {
	stream, api := streamHeaderTimeout, apiHeaderTimeout
	streamHeaderTimeout, apiHeaderTimeout = 100*time.Millisecond, 5*time.Second
	t.Cleanup(func() { streamHeaderTimeout, apiHeaderTimeout = stream, api })

	p := newProvider(t)
	p.guideDelay = 400 * time.Millisecond
	base := proxy(t, p, nil)

	if resp, body := get(t, base+"/xmltv.php?"+creds); resp.StatusCode != http.StatusOK || body != providerGuide {
		t.Errorf("slow guide: status %d, body %q", resp.StatusCode, body)
	}
	if resp, _ := get(t, base+"/live/me/secret/slow.ts"); resp.StatusCode != http.StatusBadGateway {
		t.Errorf("stream that does not start: status = %d, want 502", resp.StatusCode)
	}
}

func TestAccessLogHidesTheProxysCredentials(t *testing.T) {
	logs := captureLogs(t)
	p := newProvider(t)
	base := proxy(t, p, nil)

	get(t, base+"/player_api.php?"+creds+"&action=get_live_categories")
	get(t, base+"/live/me/secret/1.ts")
	get(t, base+"/player_api.php?username=me&password=wrong")
	get(t, base+"/live/me/secret/2.m3u8")

	out := logs.String()
	for _, want := range []string{`"/player_api.php?username=***&password=***&action=get_live_categories"`, `"/live/***/***/1.ts"`, "| 200 |", "| 401 |"} {
		if !strings.Contains(out, want) {
			t.Errorf("log lacks %s:\n%s", want, out)
		}
	}
	for _, secret := range []string{pass, "wrong", xPass} {
		if strings.Contains(out, secret) {
			t.Errorf("%q is in the log:\n%s", secret, out)
		}
	}
}

// --- Xtream streams ---

func TestStreams(t *testing.T) {
	p := newProvider(t)
	base := proxy(t, p, nil)

	for path, want := range map[string]string{
		"/live/me/secret/1.ts":                          "live-one",
		"/me/secret/1":                                  "short-form",
		"/series/me/secret/5.mp4":                       "episode",
		"/timeshift/me/secret/60/2025-10-02:20-00/1.ts": "catch-up",
	} {
		if resp, body := get(t, base+path); resp.StatusCode != http.StatusOK || body != want {
			t.Errorf("%s: status %d, body %q, want %q", path, resp.StatusCode, body, want)
		}
	}
}

func TestStreamHeaders(t *testing.T) {
	p := newProvider(t)
	base := proxy(t, p, nil)

	// seeking in a movie: the range goes to the provider, its answer comes back
	resp, body := get(t, base+"/movie/me/secret/12.mkv", "Range", "bytes=2-5")
	if resp.StatusCode != http.StatusPartialContent || body != "film" || resp.Header.Get("Content-Range") != "bytes 2-5/100" {
		t.Errorf("status %d, body %q, content range %q", resp.StatusCode, body, resp.Header.Get("Content-Range"))
	}

	resp, _ = get(t, base+"/live/me/secret/1.ts", "Range", "bytes=0-", "Connection", "keep-alive, X-Secret-Hop", "X-Secret-Hop", "1", "User-Agent", "MyPlayer/1.0")
	if got := resp.Header.Get("X-Got-Range"); got != "bytes=0-" {
		t.Errorf("range seen by the provider = %q", got)
	}
	if got := resp.Header.Get("X-Got-Connection"); strings.Contains(got, "X-Secret-Hop") {
		t.Errorf("the client's Connection header reached the provider: %q", got)
	}
	if resp.Header.Get("Keep-Alive") != "" {
		t.Error("the provider's Keep-Alive header reached the client")
	}
	if ua := p.lastUserAgent(); ua != "MyPlayer/1.0" {
		t.Errorf("user agent seen by the provider = %q, want the client's", ua)
	}
}

func TestConfiguredUserAgent(t *testing.T) {
	p := newProvider(t)
	base := proxy(t, p, func(c *config.ProxyConfig) { c.UserAgent = "Custom/9" })

	get(t, base+"/live/me/secret/1.ts", "User-Agent", "MyPlayer/1.0")
	if ua := p.lastUserAgent(); ua != "Custom/9" {
		t.Errorf("stream: user agent = %q", ua)
	}
	get(t, base+"/player_api.php?"+creds, "User-Agent", "MyPlayer/1.0")
	if ua := p.lastUserAgent(); ua != "Custom/9" {
		t.Errorf("API: user agent = %q", ua)
	}
}

func TestProviderIsLeftWhenTheClientLeaves(t *testing.T) {
	p := newProvider(t)
	base := proxy(t, p, nil)

	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, base+"/live/me/secret/9.ts", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() // nolint: errcheck

	// data flows without waiting for the stream to end
	buf := make([]byte, 4)
	if _, err := io.ReadFull(resp.Body, buf); err != nil || string(buf) != "tick" {
		t.Fatalf("first bytes: %q, %v", buf, err)
	}

	cancel()
	select {
	case <-p.streamClosed:
	case <-time.After(5 * time.Second):
		t.Fatal("the provider's stream was still open 5s after the client left")
	}
}

// watch opens a stream through the proxy and reads its first bytes. The
// returned function leaves it.
func watch(t *testing.T, rawURL string, header ...string) func() {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	for i := 0; i+1 < len(header); i += 2 {
		req.Header.Set(header[i], header[i+1])
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	buf := make([]byte, 4)
	if _, err := io.ReadFull(resp.Body, buf); err != nil || string(buf) != "tick" {
		cancel()
		t.Fatalf("%s: first bytes %q, %v", rawURL, buf, err)
	}
	// keep reading, as a player does
	go func() { _, _ = io.Copy(io.Discard, resp.Body) }()
	return func() {
		cancel()
		_ = resp.Body.Close()
	}
}

func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("still not true after 5s: %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// An account allows a few connections, often one: every device on the same
// channel shares a single connection to the provider.
func TestLiveStreamIsSharedBetweenItsClients(t *testing.T) {
	const stream = "/live/xuser/xpass/8.ts"
	p := newProvider(t)
	base := proxy(t, p, nil)

	leaveFirst := watch(t, base+"/live/me/secret/8.ts")
	leaveSecond := watch(t, base+"/live/me/secret/8.ts", "Range", "bytes=0-")
	leaveThird := watch(t, base+"/live/me/secret/8.ts")
	if n := p.count(stream); n != 1 {
		t.Fatalf("three clients on a channel took %d connections at the provider", n)
	}

	leaveFirst()
	leaveSecond()
	time.Sleep(50 * time.Millisecond)
	if n := p.leftCount(stream); n != 0 {
		t.Fatal("the provider's stream was closed while a client was watching")
	}

	leaveThird()
	eventually(t, "the provider's stream is closed with the last client", func() bool { return p.leftCount(stream) == 1 })

	// another channel, later the same one: each a connection of its own
	leave := watch(t, base+"/live/me/secret/8.ts")
	defer leave()
	if n := p.count(stream); n != 2 {
		t.Errorf("connections = %d, want a new one for a new viewing", n)
	}
}

func TestWhatIsNotLiveIsNotShared(t *testing.T) {
	p := newProvider(t)

	for name, c := range map[string]struct {
		path     string
		provider string
		header   []string
		mutate   func(*config.ProxyConfig)
	}{
		"a stream with a length": {path: "/live/me/secret/sized.ts", provider: "/live/xuser/xpass/sized.ts"},
		"a movie":                {path: "/movie/me/secret/endless.mkv", provider: "/movie/xuser/xpass/endless.mkv"},
		"a part of a stream":     {path: "/live/me/secret/8.ts", provider: "/live/xuser/xpass/8.ts", header: []string{"Range", "bytes=100-"}},
		"sharing switched off":   {path: "/live/me/secret/8.ts", provider: "/live/xuser/xpass/8.ts", mutate: func(c *config.ProxyConfig) { c.NoStreamSharing = true }},
	} {
		base := proxy(t, p, c.mutate)
		before := p.count(c.provider)
		leaveFirst := watch(t, base+c.path, c.header...)
		leaveSecond := watch(t, base+c.path, c.header...)
		if n := p.count(c.provider) - before; n != 2 {
			t.Errorf("%s: %d connections for two clients, want one each", name, n)
		}
		leaveFirst()
		leaveSecond()
		eventually(t, name+": both connections closed", func() bool { return p.leftCount(c.provider) == p.count(c.provider) })
	}
}

// The provider drops the stream: the proxy opens it again, the player keeps
// its connection.
func TestLiveStreamIsOpenedAgainWhenTheProviderDropsIt(t *testing.T) {
	retries := streamRetries
	streamRetries = []time.Duration{0, time.Millisecond, time.Millisecond}
	t.Cleanup(func() { streamRetries = retries })

	p := newProvider(t)
	base := proxy(t, p, nil)

	resp, body := get(t, base+"/live/me/secret/dropped.ts")
	// the first connection and every retry, then the provider is given up
	want := "[part 1][part 2][part 3][part 4]"
	if resp.StatusCode != http.StatusOK || body != want {
		t.Errorf("status %d, the client got %q, want %q", resp.StatusCode, body, want)
	}
	if n := p.count("/live/xuser/xpass/dropped.ts"); n != 4 {
		t.Errorf("the provider was asked %d times, want 4", n)
	}
}

func TestM3UTracksAreSharedUnlessTheyAreFiles(t *testing.T) {
	p := newProvider(t)
	file := t.TempDir() + "/list.m3u"
	list := "#EXTM3U\n#EXTINF:-1,Live\n" + p.URL + "/stream/endless\n#EXTINF:-1,Film\n" + p.URL + "/stream/endless.mp4\n"
	if err := os.WriteFile(file, []byte(list), 0o600); err != nil {
		t.Fatal(err)
	}
	base := m3uProxy(t, p, func(c *config.ProxyConfig) { c.RemoteURL, _ = url.Parse(file) })

	for track, want := range map[string]int{trackPath(p.URL + "/stream/endless"): 1, trackPath(p.URL + "/stream/endless.mp4"): 2} {
		leaveFirst := watch(t, base+track)
		leaveSecond := watch(t, base+track)
		provider := "/stream/" + track[strings.LastIndex(track, "/")+1:]
		if n := p.count(provider); n != want {
			t.Errorf("%s: %d connections for two clients, want %d", track, n, want)
		}
		leaveFirst()
		leaveSecond()
		eventually(t, track+": connections closed", func() bool { return p.leftCount(provider) == want })
	}
}

func TestProviderDown(t *testing.T) {
	logs := captureLogs(t)
	p := newProvider(t)
	base := proxy(t, p, nil)
	p.Close()
	defer func() { noProviderCredentials(t, "logs", logs.String()) }()

	for _, path := range []string{"/live/me/secret/1.ts", "/player_api.php?" + creds, "/xmltv.php?" + creds, "/get.php?" + creds, "/live/me/secret/2.m3u8"} {
		resp, body := get(t, base+path)
		if resp.StatusCode != http.StatusBadGateway {
			t.Errorf("%s: status = %d, want 502", path, resp.StatusCode)
		}
		noProviderCredentials(t, path, body)
	}
}

func TestErrorsDoNotCarryTheProviderAddress(t *testing.T) {
	err := withoutURL(&url.Error{Op: "Get", URL: "http://provider.example/get.php?username=xuser&password=xpass", Err: errors.New("connection refused")})
	if strings.Contains(err.Error(), xPass) || err.Error() != "connection refused" {
		t.Errorf("error = %q", err)
	}
}

// --- HLS, whatever the provider ---

func TestHLSRedirectedToAnotherHost(t *testing.T) {
	p := newProvider(t)
	base := proxy(t, p, nil)

	resp, playlist := get(t, base+"/live/me/secret/2.m3u8")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	noProviderCredentials(t, "playlist", playlist)
	refs := hlsAddresses(t, playlist, "")
	if !strings.HasSuffix(refs[0], "/seg1.ts") {
		t.Errorf("the segment lost its name: %q", refs[0])
	}
	if strings.Contains(playlist, p.cdn.URL) {
		t.Errorf("the provider's host is named:\n%s", playlist)
	}

	if resp, body := get(t, base+refs[0]); resp.StatusCode != http.StatusOK || body != "segment-from-cdn" {
		t.Errorf("segment: status %d, body %q", resp.StatusCode, body)
	}
}

func TestHLSServedDirectly(t *testing.T) {
	p := newProvider(t)
	base := proxy(t, p, nil)

	resp, playlist := get(t, base+"/live/me/secret/3.m3u8", "Accept-Encoding", "gzip, deflate")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	noProviderCredentials(t, "playlist", playlist)
	if cl := resp.Header.Get("Content-Length"); cl != fmt.Sprint(len(playlist)) {
		t.Errorf("content length = %s for %d bytes", cl, len(playlist))
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/vnd.apple.mpegurl" {
		t.Errorf("content type = %q", ct)
	}
	if !strings.HasPrefix(playlist, "#EXTM3U\n#EXTINF:10,\n/hls/") {
		t.Errorf("the playlist's own lines changed:\n%s", playlist)
	}

	// a relative address and an absolute one, both through the proxy
	refs := hlsAddresses(t, playlist, "")
	if len(refs) != 2 {
		t.Fatalf("addresses = %q", refs)
	}
	if resp, body := get(t, base+refs[0]); resp.StatusCode != http.StatusOK || body != "segment-3-1" {
		t.Errorf("segment: status %d, body %q", resp.StatusCode, body)
	}

	// a player recognizes a segment by its address from one refresh to the next
	if _, again := get(t, base+"/live/me/secret/3.m3u8"); again != playlist {
		t.Errorf("the same playlist gave other addresses:\n%s\n%s", playlist, again)
	}
}

// Players ask for a playlist with "Range: bytes=0-". The provider's answer
// gives the size of its own playlist: passed on, it would make the player cut
// the rewritten one, which is longer.
func TestHLSPlaylistAskedWithARange(t *testing.T) {
	p := newProvider(t)
	base := proxy(t, p, nil)

	_, whole := get(t, base+"/live/me/secret/3.m3u8")
	resp, ranged := get(t, base+"/live/me/secret/3.m3u8", "Range", "bytes=0-")
	if resp.StatusCode != http.StatusOK || ranged != whole {
		t.Errorf("status %d, playlist:\n%s\nwant the whole one:\n%s", resp.StatusCode, ranged, whole)
	}
	for _, name := range []string{"Content-Range", "Accept-Ranges", "Etag"} {
		if v := resp.Header.Get(name); v != "" {
			t.Errorf("%s = %q describes the provider's playlist, not the one sent", name, v)
		}
	}
	if cl := resp.Header.Get("Content-Length"); cl != fmt.Sprint(len(ranged)) {
		t.Errorf("content length = %s for %d bytes", cl, len(ranged))
	}
}

// The provider redirects to a session playlist whose segments are named by
// an absolute path and need their query.
func TestHLSSegmentsOnAnAbsolutePath(t *testing.T) {
	p := newProvider(t)
	base := proxy(t, p, nil)

	resp, playlist := get(t, base+"/live/me/secret/4.m3u8")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	refs := hlsAddresses(t, playlist, "")

	if resp, body := get(t, base+refs[0]); resp.StatusCode != http.StatusOK || body != "segment-play" {
		t.Errorf("segment: status %d, body %q", resp.StatusCode, body)
	}
	if q := p.query("/play/hls/tok==/chan/seg_1.ts"); q != "h=1&r=2" {
		t.Errorf("the segment's query was lost: %q", q)
	}
}

func TestHLSBehindACustomEndpoint(t *testing.T) {
	p := newProvider(t)
	base := proxy(t, p, func(c *config.ProxyConfig) { c.CustomEndpoint = "/tv/" })

	_, playlist := get(t, base+"/tv/live/me/secret/3.m3u8")
	refs := hlsAddresses(t, playlist, "/tv")
	if resp, body := get(t, base+refs[0]); resp.StatusCode != http.StatusOK || body != "segment-3-1" {
		t.Errorf("segment: status %d, body %q", resp.StatusCode, body)
	}
}

// A token is the right to fetch one address: only this proxy can issue it.
func TestHLSTokens(t *testing.T) {
	p := newProvider(t)
	base := proxy(t, p, nil)
	_, playlist := get(t, base+"/live/me/secret/3.m3u8")
	good := hlsAddresses(t, playlist, "")[0]

	other := proxy(t, p, func(c *config.ProxyConfig) { c.Password = "another" })
	_, otherPlaylist := get(t, other+"/live/me/another/3.m3u8")
	foreign := hlsAddresses(t, otherPlaylist, "")[0]

	token := strings.Split(good, "/")[2]
	for name, ref := range map[string]string{
		"garbage":                 "/hls/not-a-token/seg.ts",
		"empty-ish":               "/hls/AAAA/seg.ts",
		"truncated":               "/hls/" + token[:len(token)-4] + "/seg.ts",
		"altered":                 "/hls/" + token[:10] + otherChar(token[10]) + token[11:] + "/seg.ts",
		"issued by another proxy": foreign,
	} {
		if ref == good {
			t.Fatalf("%s: the test did not change the token", name)
		}
		if resp, _ := get(t, base+ref); resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s: status = %d, want 404", name, resp.StatusCode)
		}
	}
	if n := p.count("/live/xuser/xpass/3_1.ts"); n != 0 {
		t.Errorf("the provider was asked %d times for refused tokens", n)
	}

	// the name after the token is free: it only carries the extension
	if resp, body := get(t, base+"/hls/"+token+"/whatever.ts"); resp.StatusCode != http.StatusOK || body != "segment-3-1" {
		t.Errorf("status %d, body %q", resp.StatusCode, body)
	}
}

// otherChar returns a token character other than c.
func otherChar(c byte) string {
	if c == 'A' {
		return "B"
	}
	return "A"
}

func TestAddressTokens(t *testing.T) {
	tokens, err := newAddressTokens("me", "secret")
	if err != nil {
		t.Fatal(err)
	}
	address := "http://cdn.example/hlsr/tok/xuser/xpass/2/seg.ts?h=1"

	token := tokens.seal(address)
	if strings.Contains(token, "xpass") || strings.ContainsAny(token, "/+=") {
		t.Errorf("token = %q", token)
	}
	if got, err := tokens.open(token); err != nil || got != address {
		t.Errorf("open = %q, %v", got, err)
	}
	if tokens.seal(address) != token {
		t.Error("the same address gave two tokens")
	}
	if tokens.seal(address+"x") == token {
		t.Error("two addresses gave the same token")
	}

	same, _ := newAddressTokens("me", "secret")
	if got, err := same.open(token); err != nil || got != address {
		t.Errorf("a restarted proxy does not read its own tokens: %q, %v", got, err)
	}
	for name, secrets := range map[string][]string{"other password": {"me", "secret2"}, "shifted": {"mes", "ecret"}} {
		other, _ := newAddressTokens(secrets...)
		if _, err := other.open(token); err == nil {
			t.Errorf("%s: another proxy read the token", name)
		}
	}
}

// --- plain M3U ---

// trackPath is the proxy's path of the track at address, with the test's
// --custom-id and credentials.
func trackPath(address string) string {
	u, err := url.Parse(address)
	if err != nil {
		panic(err)
	}
	return "/tracks/me/secret/" + trackKey(address) + "/" + path.Base(u.Path)
}

func m3uProxy(t *testing.T, p *provider, mutate func(*config.ProxyConfig)) string {
	base := proxy(t, p, func(c *config.ProxyConfig) {
		c.XtreamBaseURL, c.XtreamUser, c.XtreamPassword = "", "", ""
		c.RemoteURL, _ = url.Parse(p.URL + "/list.m3u")
		if mutate != nil {
			mutate(c)
		}
	})
	return base
}

func TestM3UPlaylist(t *testing.T) {
	p := newProvider(t)
	base := m3uProxy(t, p, nil)

	if ua := p.lastUserAgent(); ua != defaultUserAgent {
		t.Errorf("the playlist was fetched as %q, want a player's user agent", ua)
	}

	resp, body := get(t, base+"/iptv.m3u?"+creds)
	// every line of the provider is kept; the track with an invalid address is gone
	// the guide is the proxy's
	want := "#EXTM3U url-tvg=\"http://proxy.example:8080/xmltv.php?username=me&password=secret\"\n" +
		"#EXTINF:-1 tvg-id=\"\" tvg-logo=\"data:image/png;base64,AAAA\" group-title=\"News\",One, the first\n" +
		"#EXTGRP:News\n" +
		"http://proxy.example:8080" + trackPath(p.URL+"/stream/a.ts?token=1") + "\n" +
		"#EXTINF:-1,Two\n" +
		"http://proxy.example:8080" + trackPath(p.URL+"/hls/b.m3u8?token=2") + "\n" +
		"#EXTINF:-1,Three\n" +
		"http://proxy.example:8080" + trackPath(p.URL+"/redirected/c.m3u8") + "\n"
	if resp.StatusCode != http.StatusOK || body != want {
		t.Fatalf("status %d, playlist:\n%s\nwant:\n%s", resp.StatusCode, body, want)
	}

	if resp, _ := get(t, base+"/iptv.m3u?username=me&password=nope"); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("wrong password: status = %d", resp.StatusCode)
	}
}

func TestM3UTracks(t *testing.T) {
	p := newProvider(t)
	base := m3uProxy(t, p, nil)

	if resp, body := get(t, base+trackPath(p.URL+"/stream/a.ts?token=1")); resp.StatusCode != http.StatusOK || body != "track-a" {
		t.Errorf("track: status %d, body %q", resp.StatusCode, body)
	}
	if q := p.query("/stream/a.ts"); q != "token=1" {
		t.Errorf("the track's own query was lost: %q", q)
	}

	resp, playlist := get(t, base+trackPath(p.URL+"/hls/b.m3u8?token=2"))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("HLS playlist: status %d", resp.StatusCode)
	}
	if q := p.query("/hls/b.m3u8"); q != "token=2" {
		t.Errorf("the HLS playlist's query was lost: %q", q)
	}
	refs := hlsAddresses(t, playlist, "")
	if resp, body := get(t, base+refs[0]); resp.StatusCode != http.StatusOK || body != "segment-b-1" {
		t.Errorf("HLS segment: status %d, body %q", resp.StatusCode, body)
	}

	key := trackKey(p.URL + "/stream/a.ts?token=1")
	for _, path := range []string{"/tracks/me/nope/" + key + "/a.ts", "/tracks/me/secret/0/a.ts", "/tracks/me/secret/" + trackKey("http://elsewhere.example/a.ts") + "/a.ts", "/other/me/secret/" + key + "/a.ts"} {
		if resp, _ := get(t, base+path); resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s: status = %d, want 404", path, resp.StatusCode)
		}
	}
}

// A track redirects to another host's master playlist: its variants, its
// alternate audio, their segments and keys are all reached through the proxy.
func TestM3UHLSMasterPlaylistOnAnotherHost(t *testing.T) {
	p := newProvider(t)
	base := m3uProxy(t, p, nil)

	_, master := get(t, base+trackPath(p.URL+"/redirected/c.m3u8"))
	refs := hlsAddresses(t, master, "")
	if len(refs) != 2 || !strings.HasSuffix(refs[0], "/en.m3u8") || !strings.HasSuffix(refs[1], "/v1.m3u8") {
		t.Fatalf("master playlist:\n%s", master)
	}
	if !strings.Contains(master, `#EXT-X-STREAM-INF:BANDWIDTH=640996,AUDIO="a"`) {
		t.Errorf("the master playlist's own lines changed:\n%s", master)
	}

	_, audio := get(t, base+refs[0])
	if a := hlsAddresses(t, audio, ""); !strings.HasSuffix(a[0], "/en_1.aac") {
		t.Errorf("audio playlist:\n%s", audio)
	}

	_, variant := get(t, base+refs[1])
	inVariant := hlsAddresses(t, variant, "")
	if len(inVariant) != 2 {
		t.Fatalf("variant playlist:\n%s", variant)
	}
	if resp, body := get(t, base+inVariant[0]); resp.StatusCode != http.StatusOK || body != "the-key" {
		t.Errorf("key: status %d, body %q", resp.StatusCode, body)
	}
	if resp, body := get(t, base+inVariant[1]); resp.StatusCode != http.StatusOK || body != "segment-a" {
		t.Errorf("segment: status %d, body %q", resp.StatusCode, body)
	}
	if strings.Contains(master+audio+variant, p.cdn.URL) {
		t.Error("the other host is named in a playlist")
	}
}

func TestM3UAdvertisedAddress(t *testing.T) {
	p := newProvider(t)
	base := m3uProxy(t, p, func(c *config.ProxyConfig) {
		c.HTTPS = true
		c.AdvertisedPort = 443
		c.CustomEndpoint = "/tv/"
		c.User, c.Password = "a user", "p@ss/word"
	})

	_, body := get(t, base+"/tv/iptv.m3u?username=a+user&password=p%40ss%2Fword")
	if want := "https://proxy.example:443/tv/tracks/a%20user/p@ss%2Fword/" + trackKey(p.URL+"/stream/a.ts?token=1") + "/a.ts\n"; !strings.Contains(body, want) {
		t.Errorf("want %s in:\n%s", want, body)
	}
}

func TestM3UFromAFile(t *testing.T) {
	p := newProvider(t)
	file := t.TempDir() + "/list.m3u"
	if err := os.WriteFile(file, []byte("#EXTM3U\n#EXTINF:-1,Local\n"+p.URL+"/stream/a.ts\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	base := m3uProxy(t, p, func(c *config.ProxyConfig) { c.RemoteURL, _ = url.Parse(file) })

	if resp, body := get(t, base+trackPath(p.URL+"/stream/a.ts")); resp.StatusCode != http.StatusOK || body != "track-a" {
		t.Errorf("status %d, body %q", resp.StatusCode, body)
	}
}

func TestStartupFailsOnAProviderThatRefuses(t *testing.T) {
	var asked atomic.Int32
	refusing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked.Add(1)
		if r.URL.Path == "/html" {
			fmt.Fprint(w, "<html>Access denied</html>")
			return
		}
		w.WriteHeader(http.StatusForbidden)
	}))
	defer refusing.Close()

	for path, want := range map[string]string{
		"/403?username=xuser&password=xpass":  "403 Forbidden",
		"/html?username=xuser&password=xpass": "expected an #EXTM3U header",
	} {
		remote, _ := url.Parse(refusing.URL + path)
		_, err := NewServer(&config.ProxyConfig{
			HostConfig: &config.HostConfiguration{Hostname: "proxy.example", Port: 8080},
			RemoteURL:  remote,
		})
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: err = %v, want it to say %q", path, err, want)
		}
		if err != nil && strings.Contains(err.Error(), xPass) {
			t.Errorf("%s: the error names the provider's password: %v", path, err)
		}
	}
	if asked.Load() != 2 {
		t.Errorf("provider asked %d times", asked.Load())
	}
}
