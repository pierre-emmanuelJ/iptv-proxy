package server

import (
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/pierre-emmanuelJ/iptv-proxy/pkg/config"
	"github.com/pierre-emmanuelJ/iptv-proxy/pkg/filter"
)

// fromFile serves the playlist of a local file, which the test rewrites.
func fromFile(t *testing.T, p *provider, list string) (string, func(string)) {
	t.Helper()
	file := t.TempDir() + "/list.m3u"
	write := func(list string) {
		if err := os.WriteFile(file, []byte(list), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(list)
	base := m3uProxy(t, p, func(c *config.ProxyConfig) {
		c.RemoteURL, _ = url.Parse(file)
		c.M3UCacheExpiration = 0 // read again at each request
	})
	return base, write
}

// The playlist is read again: a new track appears, and a track keeps its
// address wherever it moves.
func TestM3UPlaylistIsReadAgain(t *testing.T) {
	p := newProvider(t)
	a, b := p.URL+"/stream/a.ts", p.URL+"/hls/b.m3u8"
	base, write := fromFile(t, p, "#EXTM3U\n#EXTINF:-1,A\n"+a+"\n")

	if _, body := get(t, base+"/iptv.m3u?"+creds); strings.Contains(body, ",B") {
		t.Fatalf("B before it was added:\n%s", body)
	}
	write("#EXTM3U\n#EXTINF:-1,B\n" + b + "\n#EXTINF:-1,A\n" + a + "\n")
	_, body := get(t, base+"/iptv.m3u?"+creds)
	if !strings.Contains(body, ",B\nhttp://proxy.example:8080"+trackPath(b)+"\n") || !strings.Contains(body, ",A\nhttp://proxy.example:8080"+trackPath(a)+"\n") {
		t.Errorf("after the playlist changed:\n%s", body)
	}
	if resp, track := get(t, base+trackPath(a)); resp.StatusCode != http.StatusOK || track != "track-a" {
		t.Errorf("a track that moved: status %d, body %q", resp.StatusCode, track)
	}

	// a track removed from the playlist has no address any more
	write("#EXTM3U\n#EXTINF:-1,B\n" + b + "\n")
	if resp, _ := get(t, base+trackPath(a)); resp.StatusCode != http.StatusNotFound {
		t.Errorf("a removed track: status = %d", resp.StatusCode)
	}
}

// A playlist that cannot be read again is kept, and not asked for at every
// request.
func TestM3UPlaylistKeptWhenTheProviderFails(t *testing.T) {
	logs := captureLogs(t)
	p := newProvider(t)
	base := m3uProxy(t, p, func(c *config.ProxyConfig) { c.M3UCacheExpiration = 0 })
	_, before := get(t, base+"/iptv.m3u?"+creds)

	p.down.Store(true)
	for range 3 {
		if resp, body := get(t, base+"/iptv.m3u?"+creds); resp.StatusCode != http.StatusOK || body != before {
			t.Errorf("provider down: status %d, playlist:\n%s", resp.StatusCode, body)
		}
	}
	if resp, track := get(t, base+trackPath(p.URL+"/stream/a.ts?token=1")); resp.StatusCode != http.StatusOK || track != "track-a" {
		t.Errorf("track with the provider's playlist down: status %d, body %q", resp.StatusCode, track)
	}
	// at startup, for the first client, then once with the provider down:
	// after a failure, it waits before asking again
	if n := p.count("/list.m3u"); n != 3 {
		t.Errorf("playlist asked %d times, want 3", n)
	}
	if !strings.Contains(logs.String(), "playlist: reading it again failed") {
		t.Errorf("not logged:\n%s", logs.String())
	}
}

func TestM3UPlaylistReadAgainOnceDue(t *testing.T) {
	p := newProvider(t)
	base := m3uProxy(t, p, nil) // kept an hour
	for range 3 {
		get(t, base+"/iptv.m3u?"+creds)
	}
	if n := p.count("/list.m3u"); n != 1 {
		t.Errorf("playlist asked %d times within the hour, want 1", n)
	}
}

func TestM3UGuide(t *testing.T) {
	p := newProvider(t)
	base := m3uProxy(t, p, nil)

	resp, guide := get(t, base+"/xmltv.php?"+creds)
	if resp.StatusCode != http.StatusOK || guide != providerGuide {
		t.Errorf("status %d, guide:\n%s", resp.StatusCode, guide)
	}
	if resp, _ := get(t, base+"/xmltv.php?username=me&password=nope"); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("wrong password: status = %d", resp.StatusCode)
	}

	// kept for when the guide's host fails
	p.down.Store(true)
	if resp, body := get(t, base+"/xmltv.php?"+creds); resp.StatusCode != http.StatusOK || body != guide {
		t.Errorf("guide host down: status %d, guide:\n%s", resp.StatusCode, body)
	}
}

func TestM3UGuideFiltered(t *testing.T) {
	p := newProvider(t)
	list := "#EXTM3U x-tvg-url=\"" + p.URL + "/epg.xml, http://second.example/epg.xml\"\n" +
		"#EXTINF:-1 tvg-id=\"one.fr\" group-title=\"News\",One\n" + p.URL + "/stream/a.ts\n" +
		"#EXTINF:-1 tvg-id=\"sport.fr\" group-title=\"Sport\",Sport\n" + p.URL + "/stream/b.ts\n"
	file := t.TempDir() + "/list.m3u"
	if err := os.WriteFile(file, []byte(list), 0o600); err != nil {
		t.Fatal(err)
	}
	base := m3uProxy(t, p, func(c *config.ProxyConfig) {
		c.RemoteURL, _ = url.Parse(file)
		c.Filter = filter.Patterns{Group: "News"}
	})

	_, guide := get(t, base+"/xmltv.php?"+creds)
	if !strings.Contains(guide, `<channel id="one.fr">`) || strings.Contains(guide, "sport.fr") {
		t.Errorf("want the guide of One alone:\n%s", guide)
	}
	_, playlist := get(t, base+"/iptv.m3u?"+creds)
	if !strings.HasPrefix(playlist, "#EXTM3U x-tvg-url=\"http://proxy.example:8080/xmltv.php?username=me&password=secret\"\n") {
		t.Errorf("the playlist's guide is the proxy's:\n%s", playlist)
	}
}

// --xmltv-url gives a guide to a playlist that names none, or another one;
// it may be a local file.
func TestM3UGuideFromTheOption(t *testing.T) {
	p := newProvider(t)
	file := t.TempDir() + "/guide.xml"
	if err := os.WriteFile(file, []byte(`<tv><channel id="local"/></tv>`), 0o600); err != nil {
		t.Fatal(err)
	}
	playlist := t.TempDir() + "/list.m3u"
	if err := os.WriteFile(playlist, []byte("#EXTM3U\n#EXTINF:-1,A\n"+p.URL+"/stream/a.ts\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	without := m3uProxy(t, p, func(c *config.ProxyConfig) { c.RemoteURL, _ = url.Parse(playlist) })
	if resp, _ := get(t, without+"/xmltv.php?"+creds); resp.StatusCode != http.StatusNotFound {
		t.Errorf("no guide anywhere: status = %d, want 404", resp.StatusCode)
	}
	if _, body := get(t, without+"/iptv.m3u?"+creds); !strings.HasPrefix(body, "#EXTM3U\n") {
		t.Errorf("no guide: the header names none:\n%s", body)
	}

	with := m3uProxy(t, p, func(c *config.ProxyConfig) {
		c.RemoteURL, _ = url.Parse(playlist)
		c.XMLTVURL = file
	})
	if _, body := get(t, with+"/xmltv.php?"+creds); body != `<tv><channel id="local"/></tv>` {
		t.Errorf("guide from a file: %q", body)
	}
	if _, body := get(t, with+"/iptv.m3u?"+creds); !strings.HasPrefix(body, "#EXTM3U url-tvg=\"http://proxy.example:8080/xmltv.php?username=me&password=secret\"\n") {
		t.Errorf("the playlist names the proxy's guide:\n%s", body)
	}

	missing := m3uProxy(t, p, func(c *config.ProxyConfig) {
		c.RemoteURL, _ = url.Parse(playlist)
		c.XMLTVURL = file + ".missing"
	})
	if resp, _ := get(t, missing+"/xmltv.php?"+creds); resp.StatusCode != http.StatusBadGateway {
		t.Errorf("missing guide file: status = %d, want 502", resp.StatusCode)
	}
}

// Without --custom-id, track addresses stay the same from one start to the
// next; other credentials give other addresses.
func TestTrackAddressesSurviveARestart(t *testing.T) {
	p := newProvider(t)
	first := m3uProxy(t, p, func(c *config.ProxyConfig) { c.CustomId = "" })
	second := m3uProxy(t, p, func(c *config.ProxyConfig) { c.CustomId = "" })
	other := m3uProxy(t, p, func(c *config.ProxyConfig) { c.CustomId = ""; c.Password = "other" })

	_, a := get(t, first+"/iptv.m3u?"+creds)
	_, b := get(t, second+"/iptv.m3u?"+creds)
	_, c := get(t, other+"/iptv.m3u?username=me&password=other")
	if a != b {
		t.Errorf("addresses changed at restart:\n%s\n%s", a, b)
	}
	// the first element of a track's path, the --custom-id
	prefix := func(playlist string) string {
		lines := strings.Split(strings.TrimSpace(playlist), "\n")
		u, err := url.Parse(lines[len(lines)-1])
		if err != nil {
			t.Fatal(err)
		}
		return strings.Split(u.Path, "/")[1]
	}
	if prefix(c) == prefix(a) || len(prefix(a)) != 8 {
		t.Errorf("prefixes %q and %q: want two different ones", prefix(a), prefix(c))
	}
}
