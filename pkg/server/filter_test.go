package server

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/pierre-emmanuelJ/iptv-proxy/pkg/config"
	"github.com/pierre-emmanuelJ/iptv-proxy/pkg/filter"
)

func filtered(patterns filter.Patterns) func(*config.ProxyConfig) {
	return func(c *config.ProxyConfig) { c.Filter = patterns }
}

func TestInvalidFilterStopsTheStart(t *testing.T) {
	_, err := NewServer(&config.ProxyConfig{
		HostConfig: &config.HostConfiguration{Hostname: "proxy.example", Port: 8080},
		Filter:     filter.Patterns{Group: "(News"},
	})
	if err == nil || !strings.Contains(err.Error(), "--group-regex") {
		t.Errorf("err = %v, want it to name --group-regex", err)
	}
}

// --- Xtream API ---

func TestAPILiveListsAreFiltered(t *testing.T) {
	p := newProvider(t)
	base := proxy(t, p, filtered(filter.Patterns{Group: "^News$"}))

	// what is kept is the provider's own bytes
	for action, want := range map[string]string{
		"get_live_categories": `[{"category_id":"10","category_name":"News","parent_id":0}]`,
		"get_live_streams":    `[{"num":1,"name":"One","stream_type":"live","stream_id":1,"stream_icon":"http:\/\/logos.example\/one.png","epg_channel_id":"one.fr","added":"1700000000","category_id":10,"tv_archive":0,"direct_source":""}]`,
		// movies and series are not filtered
		"get_vod_categories": `[{"category_id":"30","category_name":"Films"}]`,
		"get_series":         `{"1":{"series_id":7,"name":"Show"}}`,
	} {
		resp, body := get(t, base+"/player_api.php?"+creds+"&action="+action)
		if resp.StatusCode != http.StatusOK || body != want {
			t.Errorf("%s: status %d, body:\n%s\nwant:\n%s", action, resp.StatusCode, body, want)
		}
	}
}

func TestAPIStreamsFilteredByName(t *testing.T) {
	p := newProvider(t)
	base := proxy(t, p, filtered(filter.Patterns{ChannelExclude: "^Two$"}))

	_, body := get(t, base+"/player_api.php?"+creds+"&action=get_live_streams")
	if strings.Contains(body, `"name":"Two"`) || !strings.Contains(body, `"name":"One"`) || !strings.Contains(body, `"name":"Lost"`) {
		t.Errorf("want One and Lost (its category is unknown, but no group is filtered):\n%s", body)
	}
	// nothing to look up: the categories are not asked for
	if n := p.count("/player_api.php"); n != 1 {
		t.Errorf("provider asked %d times, want 1", n)
	}
	_, categories := get(t, base+"/player_api.php?"+creds+"&action=get_live_categories")
	if !strings.Contains(categories, "News") || !strings.Contains(categories, "Sport") {
		t.Errorf("a channel filter leaves the categories alone:\n%s", categories)
	}
}

func TestAPIStreamsInSeveralCategories(t *testing.T) {
	c := &Config{}
	var err error
	if c.rules, err = filter.New(filter.Patterns{Group: "^Kids$"}); err != nil {
		t.Fatal(err)
	}
	groups := map[string]string{"1": "News", "2": "Kids"}
	if !c.keepStream(map[string]any{"name": "x", "category_id": "1", "category_ids": []any{"1", "2"}}, groups) {
		t.Error("a stream is kept when one of its categories is")
	}
	if c.keepStream(map[string]any{"name": "x", "category_id": "1", "category_ids": []any{"1"}}, groups) {
		t.Error("a stream none of whose categories is kept is left out")
	}
}

func TestAPIFilterWhenTheCategoriesFail(t *testing.T) {
	p := newProvider(t)
	base := proxy(t, p, func(c *config.ProxyConfig) {
		c.Filter = filter.Patterns{Group: "News"}
		c.XtreamPassword = "wrong" // every answer is the provider's refusal
	})
	if resp, _ := get(t, base+"/player_api.php?"+creds+"&action=get_live_streams"); resp.StatusCode != http.StatusBadGateway {
		t.Errorf("status = %d, want 502: the filters cannot be applied", resp.StatusCode)
	}
}

func TestAPICategoriesAreKept(t *testing.T) {
	p := newProvider(t)
	base := proxy(t, p, filtered(filter.Patterns{Group: "News"}))

	for range 3 {
		get(t, base+"/player_api.php?"+creds+"&action=get_live_streams")
	}
	// three lists, and the categories once: 4 requests
	if n := p.count("/player_api.php"); n != 4 {
		t.Errorf("provider asked %d times, want 4", n)
	}
}

// --- Xtream playlists and guide ---

func TestXtreamPlaylistsAreFiltered(t *testing.T) {
	p := newProvider(t)
	base := proxy(t, p, filtered(filter.Patterns{Group: "News"}))

	_, body := get(t, base+"/get.php?"+creds+"&type=m3u_plus&output=ts")
	if !strings.Contains(body, ",One\n") || strings.Contains(body, "A film") {
		t.Errorf("get.php: want One alone (the film has no group):\n%s", body)
	}

	_, generated := get(t, base+"/apiget?"+creds+"&output=ts")
	if !strings.Contains(generated, ",One\n") || strings.Contains(generated, "Two") || strings.Contains(generated, "Lost") {
		t.Errorf("generated playlist: want One alone:\n%s", generated)
	}
}

func TestGuideIsFiltered(t *testing.T) {
	p := newProvider(t)
	base := proxy(t, p, filtered(filter.Patterns{Group: "News"}))

	want := `<?xml version="1.0" encoding="UTF-8"?>
<tv generator-info-name="provider">
  <channel id="one.fr"><display-name>One</display-name></channel>
  <programme start="20251002200000 +0200" channel="one.fr"><title>News</title><desc><![CDATA[<b>live</b>]]></desc></programme>
  </tv>
`
	resp, body := get(t, base+"/xmltv.php?"+creds)
	if resp.StatusCode != http.StatusOK || body != want {
		t.Errorf("status %d, guide:\n%s\nwant:\n%s", resp.StatusCode, body, want)
	}
	if resp.Header.Get("Content-Length") != "" {
		t.Error("the provider's length does not describe a filtered guide")
	}

	// a guide sent compressed is filtered all the same
	resp, body = get(t, base+"/xmltv.php?"+creds+"&gzip=1")
	if resp.StatusCode != http.StatusOK || body != want {
		t.Errorf("compressed guide: status %d:\n%s", resp.StatusCode, body)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/xml" {
		t.Errorf("compressed guide: content type = %q", ct)
	}
}

func TestGuideFilterOnProviderErrors(t *testing.T) {
	p := newProvider(t)
	base := proxy(t, p, filtered(filter.Patterns{Group: "News"}))

	resp, body := get(t, base+"/xmltv.php?"+creds+"&fail=1")
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want the provider's 503", resp.StatusCode)
	}
	noProviderCredentials(t, "guide error", body)

	broken := proxy(t, p, func(c *config.ProxyConfig) {
		c.Filter = filter.Patterns{Group: "News"}
		c.XtreamPassword = "wrong"
	})
	if resp, _ := get(t, broken+"/xmltv.php?"+creds); resp.StatusCode != http.StatusBadGateway {
		t.Errorf("without the channel list: status = %d, want 502", resp.StatusCode)
	}
}

// --- plain M3U ---

func TestM3UPlaylistIsFiltered(t *testing.T) {
	p := newProvider(t)
	base := m3uProxy(t, p, filtered(filter.Patterns{Group: "^News$", ChannelExclude: "Three"}))

	_, body := get(t, base+"/iptv.m3u?"+creds)
	want := "#EXTM3U url-tvg=\"http://proxy.example:8080/xmltv.php?username=me&password=secret\"\n" +
		"#EXTINF:-1 tvg-id=\"\" tvg-logo=\"data:image/png;base64,AAAA\" group-title=\"News\",One, the first\n" +
		"#EXTGRP:News\n" +
		"http://proxy.example:8080" + trackPath(p.URL+"/stream/a.ts?token=1") + "\n"
	if body != want {
		t.Errorf("playlist:\n%s\nwant:\n%s", body, want)
	}
	// a track left out has no address on the proxy
	if resp, _ := get(t, base+trackPath(p.URL+"/hls/b.m3u8?token=2")); resp.StatusCode != http.StatusNotFound {
		t.Errorf("a track left out is served: status %d", resp.StatusCode)
	}
	if resp, track := get(t, base+trackPath(p.URL+"/stream/a.ts?token=1")); resp.StatusCode != http.StatusOK || track != "track-a" {
		t.Errorf("kept track: status %d, body %q", resp.StatusCode, track)
	}
}

// A playlist may name its provider's credentials outside of the track
// addresses: a guide, a catch-up address, a player option. None of it
// reaches the client; the rest of the lines is kept.
func TestM3UPlaylistDoesNotGiveTheProviderAway(t *testing.T) {
	p := newProvider(t)
	base := m3uProxy(t, p, func(c *config.ProxyConfig) {
		c.RemoteURL, _ = url.Parse(p.URL + "/leaky.m3u?username=" + xUser + "&password=" + xPass)
	})

	_, body := get(t, base+"/iptv.m3u?"+creds)
	// the guide it names, without the provider's password, is the proxy's
	want := "#EXTM3U x-tvg-url=\"http://proxy.example:8080/xmltv.php?username=me&password=secret\"\n" +
		"#EXTINF:-1 tvg-logo=\"http://logos.example/one.png\" group-title=\"News\",One\n" +
		"#EXTVLCOPT:http-user-agent=Player\n" +
		"http://proxy.example:8080" + trackPath(p.URL+"/stream/a.ts") + "\n"
	if body != want {
		t.Errorf("playlist:\n%s\nwant:\n%s", body, want)
	}
	noProviderCredentials(t, "playlist", body)
}

func TestXtreamCatchUpAddressPlays(t *testing.T) {
	p := newProvider(t)
	base := proxy(t, p, nil)

	_, body := get(t, base+"/get.php?"+creds+"&type=m3u_plus")
	noProviderCredentials(t, "get.php", body)
	// the player fills the catch-up address in, then asks the proxy
	i := strings.Index(body, `catchup-source="`)
	if i < 0 {
		t.Fatalf("no catch-up address:\n%s", body)
	}
	source := body[i+len(`catchup-source="`):]
	source = source[:strings.IndexByte(source, '"')]
	address := strings.NewReplacer("http://proxy.example:8080", base, "{duration}", "60", "{start}", "2025-10-02:20-00").Replace(source)
	if resp, got := get(t, address); resp.StatusCode != http.StatusOK || got != "catch-up" {
		t.Errorf("catch-up: status %d, body %q", resp.StatusCode, got)
	}
}

func TestListenAddress(t *testing.T) {
	for address, want := range map[string]string{"": ":8080", "192.168.1.10": "192.168.1.10:8080", "::1": "[::1]:8080"} {
		c := &Config{ProxyConfig: &config.ProxyConfig{HostConfig: &config.HostConfiguration{Port: 8080}, ListenAddress: address}}
		if got := c.listenAddress(); got != want {
			t.Errorf("listen address %q: %q, want %q", address, got, want)
		}
	}
}
