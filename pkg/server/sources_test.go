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
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/pierre-emmanuelJ/iptv-proxy/pkg/config"
	"github.com/pierre-emmanuelJ/iptv-proxy/pkg/xtream"
)

// withSource adds the second account of q as a second source.
func withSource(q *provider) func(*config.ProxyConfig) {
	return func(c *config.ProxyConfig) {
		c.Sources = []config.Source{{Name: "backup", XtreamBaseURL: q.URL, XtreamUser: x2User, XtreamPassword: x2Pass}}
	}
}

// listOf returns the entries of an API list, by their id.
func listOf(t *testing.T, base, action, idField string) map[string]map[string]any {
	t.Helper()
	resp, body := get(t, base+"/player_api.php?"+creds+"&action="+action)
	entries, err := xtream.DecodeList([]byte(body))
	if resp.StatusCode != http.StatusOK || err != nil {
		t.Fatalf("%s: status %d, %v: %s", action, resp.StatusCode, err, body)
	}
	byID := map[string]map[string]any{}
	for _, e := range entries {
		byID[xtream.Text(e[idField])] = e
	}
	return byID
}

func ids(entries map[string]map[string]any) string {
	var all []string
	for id := range entries {
		all = append(all, id)
	}
	sort.Strings(all)
	return strings.Join(all, ",")
}

// The sources are one catalogue: categories of the same name are one, ids
// of the second source are shown in their own range, and a live channel
// the first source has already is not shown twice.
func TestSourcesCatalogue(t *testing.T) {
	p, q := newProvider(t), newProvider(t)
	base := proxy(t, p, withSource(q))

	categories := listOf(t, base, "get_live_categories", "category_id")
	if got := ids(categories); got != "10,20" {
		t.Errorf("categories: %s", got) // "News" twice at the second source, "Sport" once
	}

	streams := listOf(t, base, "get_live_streams", "stream_id")
	if got := ids(streams); got != "1,100000002,100000003,100000005,2,3" {
		t.Errorf("live streams: %s", got) // the second source's "one.fr" is the first's
	}
	for id, category := range map[string]string{"1": "10", "100000005": "10", "100000002": "20", "100000003": "100000099"} {
		if got := xtream.Text(streams[id]["category_id"]); got != category {
			t.Errorf("stream %s: category %s, want %s", id, got, category)
		}
	}
	// the provider's types are kept: a number stays a number
	if _, isNumber := streams["100000003"]["stream_id"].(json.Number); !isNumber {
		t.Errorf("stream 100000003: id %#v", streams["100000003"]["stream_id"])
	}

	_, body := get(t, base+"/player_api.php?"+creds+"&action=get_live_streams&category_id=10")
	entries, _ := xtream.DecodeList([]byte(body))
	var inNews []string
	for _, e := range entries {
		inNews = append(inNews, xtream.Text(e["stream_id"]))
	}
	if strings.Join(inNews, ",") != "1,100000005" {
		t.Errorf("category 10: %v", inNews)
	}
	// a stream in several categories is in each, under their ids shown
	if got := xtream.Text(streams["100000005"]["category_ids"].([]any)[1]); got != "20" {
		t.Errorf("category_ids: %v", streams["100000005"]["category_ids"])
	}
	_, body = get(t, base+"/player_api.php?"+creds+"&action=get_live_streams&category_id=20")
	if !strings.Contains(body, `"Local news"`) {
		t.Errorf("category 20:\n%s", body)
	}

	// The categories are read once, not with every list: they change
	// seldom.
	before := q.count("/player_api.php")
	listOf(t, base, "get_live_streams", "stream_id")
	if n := q.count("/player_api.php") - before; n != 1 {
		t.Errorf("a list asked the second source %d times", n)
	}

	if got := ids(listOf(t, base, "get_vod_streams", "stream_id")); got != "100000012,100000013,12,13" {
		t.Errorf("movies: %s", got)
	}
	if got := ids(listOf(t, base, "get_series", "series_id")); got != "100000007,7" {
		t.Errorf("series: %s", got)
	}
}

func TestSourcesStreams(t *testing.T) {
	p, q := newProvider(t), newProvider(t)
	base := proxy(t, p, withSource(q))

	for path, want := range map[string]string{
		"/live/me/secret/1.ts":                                  "live-one",
		"/me/secret/1":                                          "short-form",
		"/live/me/secret/100000005.ts":                          "second-five",
		"/movie/me/secret/100000012.mkv":                        "second:12.mkv",
		"/series/me/secret/100000005.mp4":                       "second:5.mp4",
		"/timeshift/me/secret/60/2025-10-02:20-00/100000001.ts": "second:60/2025-10-02:20-00/1.ts",
		"/timeshift/me/secret/60/2025-10-02:20-00/1.ts":         "catch-up",
	} {
		if resp, body := get(t, base+path); resp.StatusCode >= 300 || body != want {
			t.Errorf("%s: status %d, %q, want %q", path, resp.StatusCode, body, want)
		}
	}

	// an error page of the second source does not give its account away
	resp, page := get(t, base+"/live/me/secret/100000099.ts")
	if resp.StatusCode != http.StatusNotFound || strings.Contains(page, x2User) || strings.Contains(page, x2Pass) || !strings.Contains(page, "/live/me/secret/99.ts") {
		t.Errorf("error page: status %d: %s", resp.StatusCode, page)
	}
}

// In the other order, the second account is the first source: its two
// "News" are two categories, and two streams of one source with the same
// guide id are both shown.
func TestSourcesInTheOtherOrder(t *testing.T) {
	p, q := newProvider(t), newProvider(t)
	q.late.Store(true) // a second "one.fr" at the first source
	base := proxy(t, q, func(c *config.ProxyConfig) {
		c.XtreamUser, c.XtreamPassword = x2User, x2Pass
		c.Sources = []config.Source{{XtreamBaseURL: p.URL, XtreamUser: xUser, XtreamPassword: xPass}}
	})
	if got := ids(listOf(t, base, "get_live_categories", "category_id")); got != "10,11,20" {
		t.Errorf("categories: %s", got)
	}
	if got := ids(listOf(t, base, "get_live_streams", "stream_id")); got != "1,100000002,100000003,2,3,4,5" {
		t.Errorf("live streams: %s", got)
	}
}

// A live channel that fails at its source plays from another source that
// has it.
func TestSourcesFailover(t *testing.T) {
	logs := captureLogs(t)
	p, q := newProvider(t), newProvider(t)
	base := proxy(t, p, withSource(q))

	p.liveDown.Store(true)
	if resp, body := get(t, base+"/live/me/secret/1.ts"); resp.StatusCode != http.StatusOK || body != "second-one" {
		t.Errorf("status %d, %q", resp.StatusCode, body)
	}
	if p.count("/live/xuser/xpass/1.ts") != 1 {
		t.Errorf("the first source was asked %d times", p.count("/live/xuser/xpass/1.ts"))
	}
	if !strings.Contains(logs.String(), "xtream failed (HTTP 503): trying backup") {
		t.Errorf("logs:\n%s", logs.String())
	}
	// a channel with no other source answers what its source answered
	if resp, _ := get(t, base+"/live/me/secret/100000099.ts"); resp.StatusCode != http.StatusNotFound {
		t.Errorf("no fallback: status %d", resp.StatusCode)
	}
	// the stream's query goes along, to whichever source
	get(t, base+"/live/me/secret/100000005.ts?token=abc")
	if got := q.query("/live/second/pw+2/5.ts"); got != "token=abc" {
		t.Errorf("query: %q", got)
	}
}

// The other sources of a channel are read again once older than the
// playlist cache.
func TestSourcesFallbacksAreReadAgain(t *testing.T) {
	p, q := newProvider(t), newProvider(t)
	base := proxy(t, p, func(c *config.ProxyConfig) {
		withSource(q)(c)
		c.M3UCacheExpiration = 0 // every list is old at once
	})
	p.liveDown.Store(true)
	get(t, base+"/live/me/secret/1.ts")
	before := q.query("/player_api.php")
	q.mu.Lock()
	q.queries["/player_api.php"] = ""
	q.mu.Unlock()
	if _, body := get(t, base+"/live/me/secret/1.ts"); body != "second-one" {
		t.Errorf("%q", body)
	}
	if q.query("/player_api.php") == "" {
		t.Errorf("the lists were not read again (before: %q)", before)
	}
}

// A source's limit is asked to its account once; a source whose limit is
// not known is never full.
func TestSourceLimit(t *testing.T) {
	p := newProvider(t)
	c := &Config{ProxyConfig: &config.ProxyConfig{}}
	c.apiClient = http.DefaultClient
	src := &source{name: "a", account: xtream.Account{BaseURL: p.URL, User: xUser, Password: xPass}}
	for range 3 {
		if n := c.sourceLimit(t.Context(), src); n != 2 {
			t.Errorf("limit %d, want the account's 2", n)
		}
	}
	if n := p.count("/player_api.php"); n != 1 {
		t.Errorf("the account was asked %d times", n)
	}
	unknown := &source{name: "b", asked: true, opened: 3}
	if c.sourceFull(t.Context(), unknown) {
		t.Error("a source with no known limit is full")
	}
}

func TestShownIDs(t *testing.T) {
	for _, c := range []struct {
		src      int
		id, want string
	}{{0, "12", "12"}, {1, "12", "100000012"}, {2, "0", "200000000"}, {1, "100000000", "100000000"}, {1, "-5", "-5"}, {1, "abc", "abc"}} {
		if got := shownID(c.src, c.id); got != c.want {
			t.Errorf("shownID(%d, %s) = %s, want %s", c.src, c.id, got, c.want)
		}
	}
	c := &Config{sources: []*source{{index: 0}, {index: 1}}}
	for id, want := range map[string]string{"12": "0/12", "100000012": "1/12", "200000012": "0/200000012", "x": "0/x"} {
		src, own := c.sourceOf(id)
		if got := strconv.Itoa(src.index) + "/" + own; got != want {
			t.Errorf("sourceOf(%s) = %s, want %s", id, got, want)
		}
	}
}

// A source with no connection left passes the channel to the next one.
func TestSourcesLimit(t *testing.T) {
	p, q := newProvider(t), newProvider(t)
	base := proxy(t, p, func(c *config.ProxyConfig) {
		withSource(q)(c)
		c.Sources[0].MaxConnections = 10
		c.NoStreamSharing = true // each client its own connection
	})

	// the first account allows two streams
	first, _ := open(t, base+"/live/me/secret/8.ts")
	second, _ := open(t, base+"/live/me/secret/8.ts")
	eventually(t, "two streams open", func() bool { return p.count("/live/xuser/xpass/8.ts") == 2 })

	if resp, body := get(t, base+"/live/me/secret/1.ts"); resp.StatusCode != http.StatusOK || body != "second-one" {
		t.Errorf("at the first source's limit: status %d, %q", resp.StatusCode, body)
	}
	if n := p.count("/live/xuser/xpass/1.ts"); n != 0 {
		t.Errorf("the full source was asked %d times", n)
	}

	// once its streams are closed, the first source has its places back
	first.leave()
	second.leave()
	eventually(t, "the streams closed", func() bool { return p.leftCount("/live/xuser/xpass/8.ts") == 2 })
	if _, body := get(t, base+"/live/me/secret/1.ts"); body != "live-one" {
		t.Errorf("after the streams closed: %q", body)
	}
}

// A list that misses a source does not leave its channels without their
// other sources.
func TestSourcesPartialListKeepsNoFallbacks(t *testing.T) {
	p, q := newProvider(t), newProvider(t)
	base := proxy(t, p, withSource(q))
	q.down.Store(true)
	listOf(t, base, "get_live_streams", "stream_id")
	q.down.Store(false)
	p.liveDown.Store(true)
	if _, body := get(t, base+"/live/me/secret/1.ts"); body != "second-one" {
		t.Errorf("%q", body)
	}
}

// An entry's details are asked to its source, under its id there, and
// given back with the ids shown.
func TestSourcesDetails(t *testing.T) {
	p, q := newProvider(t), newProvider(t)
	base := proxy(t, p, withSource(q))

	resp, body := get(t, base+"/player_api.php?"+creds+"&action=get_vod_info&vod_id=100000012")
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, `"stream_id":"100000012"`) {
		t.Errorf("movie: status %d: %s", resp.StatusCode, body)
	}
	asked, _ := url.ParseQuery(q.query("/player_api.php"))
	if asked.Get("vod_id") != "12" || asked.Get("username") != x2User {
		t.Errorf("the second source was asked %v", asked)
	}
	// the first source's own entries are its answers, as they are
	if _, body := get(t, base+"/player_api.php?"+creds+"&action=get_vod_info&vod_id=12"); !strings.Contains(body, `"stream_id":"12"`) {
		t.Errorf("movie of the first source: %s", body)
	}
}

func TestShownDetails(t *testing.T) {
	for _, c := range []struct{ answer, want string }{
		{`{"movie_data":{"stream_id":12,"name":"<b>"}}`, `{"movie_data":{"name":"<b>","stream_id":200000012}}`},
		{`{"episodes":{"1":[{"id":"7"},{"id":"8"}]}}`, `{"episodes":{"1":[{"id":"200000007"},{"id":"200000008"}]}}`},
		{`{"episodes":[[{"id":7}]]}`, `{"episodes":[[{"id":200000007}]]}`},
		{`not json`, `not json`},
	} {
		if got := string(shownDetails([]byte(c.answer), 2)); got != c.want {
			t.Errorf("%s: %s, want %s", c.answer, got, c.want)
		}
	}
}

// A source that fails leaves its part out; the last complete answer is
// served instead when there is one.
func TestSourcesPartial(t *testing.T) {
	p, q := newProvider(t), newProvider(t)
	base := proxy(t, p, withSource(q))

	q.down.Store(true)
	if got := ids(listOf(t, base, "get_live_streams", "stream_id")); got != "1,2,3" {
		t.Errorf("the second source down: %s", got)
	}
	listOf(t, base, "get_vod_streams", "stream_id") // partial: not kept
	q.down.Store(false)
	// the categories read while a source was down are not kept either
	if got := xtream.Text(listOf(t, base, "get_live_streams", "stream_id")["100000005"]["category_id"]); got != "10" {
		t.Errorf("category of 100000005: %s", got)
	}
	listOf(t, base, "get_live_streams", "stream_id") // complete, kept
	q.down.Store(true)
	if got := ids(listOf(t, base, "get_live_streams", "stream_id")); !strings.Contains(got, "100000005") {
		t.Errorf("the kept answer was not served: %s", got)
	}

	p.down.Store(true)
	if resp, _ := get(t, base+"/player_api.php?"+creds+"&action=get_vod_streams"); resp.StatusCode != http.StatusBadGateway {
		t.Errorf("every source down, nothing kept: status %d", resp.StatusCode)
	}
}

// The guide is the first source's, with the channels only the others have.
func TestSourcesGuide(t *testing.T) {
	p, q := newProvider(t), newProvider(t)
	base := proxy(t, p, withSource(q))

	resp, guide := get(t, base+"/xmltv.php?"+creds)
	want := strings.Replace(providerGuide, "</tv>", `<channel id="local.fr"><display-name>Local</display-name></channel>
<programme start="20251002200000 +0200" channel="local.fr"><title>Local news</title></programme>
</tv>`, 1)
	if resp.StatusCode != http.StatusOK || guide != want {
		t.Errorf("status %d, guide:\n%s\nwant:\n%s", resp.StatusCode, guide, want)
	}

	// a guide that misses a source is served, and not kept
	q.down.Store(true)
	if _, guide := get(t, base+"/xmltv.php?"+creds); guide != providerGuide {
		t.Errorf("the second source down:\n%s", guide)
	}
	p.down.Store(true)
	if _, guide := get(t, base+"/xmltv.php?"+creds); guide != want {
		t.Errorf("the kept guide:\n%s", guide)
	}
}

// A channel two later sources have comes once, from the first of them.
func TestSourcesGuideOfThree(t *testing.T) {
	p, q, r := newProvider(t), newProvider(t), newProvider(t)
	base := proxy(t, p, func(c *config.ProxyConfig) {
		c.Sources = []config.Source{
			{XtreamBaseURL: q.URL, XtreamUser: x2User, XtreamPassword: x2Pass},
			{XtreamBaseURL: r.URL, XtreamUser: x2User, XtreamPassword: x2Pass},
		}
	})
	_, guide := get(t, base+"/xmltv.php?"+creds)
	if n := strings.Count(guide, `<channel id="local.fr">`); n != 1 {
		t.Errorf("local.fr %d times:\n%s", n, guide)
	}
}

func TestSourcesPlaylist(t *testing.T) {
	p, q := newProvider(t), newProvider(t)
	base := proxy(t, p, withSource(q))

	resp, playlist := get(t, base+"/get.php?"+creds+"&type=m3u_plus&output=ts")
	if resp.StatusCode != http.StatusOK || !strings.Contains(playlist, "\nhttp://proxy.example:8080/live/me/secret/100000005.ts\n") ||
		!strings.Contains(playlist, "\nhttp://proxy.example:8080/live/me/secret/1.ts\n") {
		t.Errorf("status %d, playlist:\n%s", resp.StatusCode, playlist)
	}
	for _, provider := range []string{p.URL, q.URL, x2User, xPass} {
		if strings.Contains(playlist, provider) {
			t.Errorf("%q in the playlist:\n%s", provider, playlist)
		}
	}
	if _, same := get(t, base+"/iptv.m3u?"+creds); !strings.Contains(same, "\nhttp://proxy.example:8080/me/secret/100000005\n") {
		t.Errorf("iptv.m3u:\n%s", same)
	}
}

func TestSourcesTuner(t *testing.T) {
	p, q := newProvider(t), newProvider(t)
	p.late.Store(true) // stream 4 of the first source, "one.fr" too
	tuner := tunerOf(t, p, withSource(q))
	var numbers []string
	for _, ch := range lineupOf(t, tuner) {
		numbers = append(numbers, ch.GuideNumber)
	}
	sort.Strings(numbers)
	if strings.Join(numbers, ",") != "1,100000002,100000003,100000005,2,3" {
		t.Errorf("lineup: %v", numbers)
	}
	if resp, body := get(t, tuner+"/auto/v100000005"); resp.StatusCode != http.StatusOK || body != "second-five" {
		t.Errorf("channel of the second source: status %d, %q", resp.StatusCode, body)
	}
	// as many tuners as the sources have connections: 2 and 1
	if _, body := get(t, tuner+"/discover.json"); !strings.Contains(body, `"TunerCount":3`) {
		t.Errorf("discover.json: %s", body)
	}
	// one.fr: the first source's channel, the second source's, then the
	// first source's other one
	p.liveDown.Store(true)
	if _, body := get(t, tuner+"/auto/v1"); body != "second-one" {
		t.Errorf("channel 1 down: %q", body)
	}
	if q.count("/live/second/pw+2/4.ts") != 0 || p.count("/live/xuser/xpass/4.ts") != 0 {
		t.Error("a later fallback was opened")
	}
}

// A source's max-connections counts for the tuner too; a source whose
// account does not answer counts as one.
func TestSourcesTunerCount(t *testing.T) {
	p, q := newProvider(t), newProvider(t)
	tuner := tunerOf(t, p, func(c *config.ProxyConfig) {
		withSource(q)(c)
		c.Sources[0].MaxConnections = 5
	})
	if _, body := get(t, tuner+"/discover.json"); !strings.Contains(body, `"TunerCount":7`) {
		t.Errorf("discover.json: %s", body)
	}
	q.down.Store(true)
	if _, body := get(t, tunerOf(t, p, withSource(q))+"/discover.json"); !strings.Contains(body, `"TunerCount":3`) {
		t.Errorf("a source down: %s", body)
	}
}

// Sources alone, without the Xtream options: the first is the proxy's
// account.
func TestSourcesWithoutXtreamOptions(t *testing.T) {
	p, q := newProvider(t), newProvider(t)
	base := proxy(t, p, func(c *config.ProxyConfig) {
		c.XtreamBaseURL, c.XtreamUser, c.XtreamPassword = "", "", ""
		c.Sources = []config.Source{
			{XtreamBaseURL: p.URL, XtreamUser: xUser, XtreamPassword: xPass},
			{XtreamBaseURL: q.URL, XtreamUser: x2User, XtreamPassword: x2Pass},
		}
	})
	if resp, body := get(t, base+"/player_api.php?"+creds); resp.StatusCode != http.StatusOK || !strings.Contains(body, `"auth":1`) {
		t.Errorf("login: status %d: %s", resp.StatusCode, body)
	}
	if got := ids(listOf(t, base, "get_live_streams", "stream_id")); got != "1,100000002,100000003,100000005,2,3" {
		t.Errorf("live streams: %s", got)
	}
}

func TestSourcesSettings(t *testing.T) {
	file := t.TempDir() + "/list.m3u"
	if err := os.WriteFile(file, []byte("#EXTM3U\n#EXTINF:-1,One\nhttp://provider.example/1.ts\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	remote, _ := url.Parse(file)
	source := config.Source{XtreamBaseURL: "http://b.example", XtreamUser: "u", XtreamPassword: "p"}
	for name, conf := range map[string]config.ProxyConfig{
		"passthrough":         {XtreamBaseURL: "http://a.example", XtreamPassthrough: true, Sources: []config.Source{source}},
		"an M3U playlist":     {RemoteURL: remote, Sources: []config.Source{source}},
		"a source incomplete": {XtreamBaseURL: "http://a.example", XtreamUser: "u", XtreamPassword: "p", Sources: []config.Source{{XtreamBaseURL: "http://b.example", XtreamUser: "u"}}},
	} {
		conf.HostConfig = &config.HostConfiguration{Hostname: "proxy.example", Port: 8080}
		if _, err := NewServer(&conf); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}
