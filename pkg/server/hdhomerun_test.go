package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/pierre-emmanuelJ/iptv-proxy/pkg/config"
	"github.com/pierre-emmanuelJ/iptv-proxy/pkg/filter"
)

// tunerOf starts the HDHomeRun tuner of a proxy in front of p.
func tunerOf(t *testing.T, p *provider, mutate func(*config.ProxyConfig)) string {
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
		HDHomeRunPort:      5004,
	}
	if mutate != nil {
		mutate(conf)
	}
	c, err := NewServer(conf)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(c.HDHomeRunHandler())
	t.Cleanup(srv.Close)
	return srv.URL
}

func lineupOf(t *testing.T, tuner string) []lineupEntry {
	t.Helper()
	resp, body := get(t, tuner+"/lineup.json")
	var lineup []lineupEntry
	if resp.StatusCode != http.StatusOK || json.Unmarshal([]byte(body), &lineup) != nil {
		t.Fatalf("lineup: status %d, %s", resp.StatusCode, body)
	}
	return lineup
}

func noCredentials(t *testing.T, what, body string) {
	t.Helper()
	noProviderCredentials(t, what, body)
	if strings.Contains(body, "/"+user+"/") || strings.Contains(body, pass) {
		t.Errorf("%s: the proxy's credentials are in it:\n%s", what, body)
	}
}

func TestTunerDiscovery(t *testing.T) {
	p := newProvider(t)
	tuner := tunerOf(t, p, nil)

	_, body := get(t, tuner+"/discover.json")
	var discover map[string]any
	if err := json.Unmarshal([]byte(body), &discover); err != nil {
		t.Fatal(err)
	}
	if discover["BaseURL"] != tuner || discover["LineupURL"] != tuner+"/lineup.json" || discover["Manufacturer"] != "Silicondust" {
		t.Errorf("discover.json: %s", body)
	}
	// as many tuners as the account allows connections ("2" for the fake one)
	if discover["TunerCount"] != float64(2) {
		t.Errorf("tuners = %v", discover["TunerCount"])
	}
	if id, _ := discover["DeviceID"].(string); len(id) != 8 {
		t.Errorf("device id %q", id)
	}
	noCredentials(t, "discover.json", body)

	if _, again := get(t, tunerOf(t, p, nil)+"/discover.json"); !strings.Contains(again, `"DeviceID":"`+discover["DeviceID"].(string)+`"`) {
		t.Errorf("the device id changed at restart: %s", again)
	}
	if _, five := get(t, tunerOf(t, p, func(c *config.ProxyConfig) { c.HDHomeRunTuners = 5 })+"/discover.json"); !strings.Contains(five, `"TunerCount":5`) {
		t.Errorf("--hdhomerun-tuners: %s", five)
	}

	for path, want := range map[string]string{"/lineup_status.json": `"ScanPossible":1`, "/device.xml": "<URLBase>" + tuner + "</URLBase>"} {
		if resp, body := get(t, tuner+path); resp.StatusCode != http.StatusOK || !strings.Contains(body, want) {
			t.Errorf("%s: status %d, %s", path, resp.StatusCode, body)
		}
	}
	if resp, err := http.Post(tuner+"/lineup.post?scan=start", "text/plain", nil); err != nil || resp.StatusCode != http.StatusOK {
		t.Errorf("lineup.post: %v %v", resp, err)
	}
}

func TestTunerLineupAndStreams(t *testing.T) {
	p := newProvider(t)
	tuner := tunerOf(t, p, nil)

	lineup := lineupOf(t, tuner)
	want := []lineupEntry{
		{"1", "One", tuner + "/auto/v1"},
		{"2", "Two", tuner + "/auto/v2"},
		{"3", "Lost", tuner + "/auto/v3"},
	}
	if len(lineup) != len(want) {
		t.Fatalf("lineup: %+v", lineup)
	}
	for i := range want {
		if lineup[i] != want[i] {
			t.Errorf("channel %d: %+v, want %+v", i, lineup[i], want[i])
		}
	}
	_, body := get(t, tuner+"/lineup.json")
	noCredentials(t, "lineup.json", body)

	// a channel plays as MPEG-TS from the provider's live stream
	if resp, stream := get(t, tuner+"/auto/v1"); resp.StatusCode != http.StatusOK || stream != "live-one" {
		t.Errorf("channel 1: status %d, %q", resp.StatusCode, stream)
	}
	for _, path := range []string{"/auto/v999", "/auto/1", "/auto/v"} {
		if resp, _ := get(t, tuner+path); resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s: status %d, want 404", path, resp.StatusCode)
		}
	}
	// an unknown channel is not asked to the provider
	if n := p.count("/live/xuser/xpass/.ts") + p.count("/live/xuser/xpass/999.ts"); n != 0 {
		t.Errorf("the provider was asked %d times for unknown channels", n)
	}
}

// A stream asked before the list was read is found all the same.
func TestTunerStreamBeforeTheLineup(t *testing.T) {
	p := newProvider(t)
	tuner := tunerOf(t, p, nil)
	if resp, stream := get(t, tuner+"/auto/v1"); resp.StatusCode != http.StatusOK || stream != "live-one" {
		t.Errorf("status %d, %q", resp.StatusCode, stream)
	}
}

func TestTunerFiltered(t *testing.T) {
	p := newProvider(t)
	tuner := tunerOf(t, p, func(c *config.ProxyConfig) { c.Filter = filter.Patterns{Group: "^News$"} })

	if lineup := lineupOf(t, tuner); len(lineup) != 1 || lineup[0].GuideName != "One" {
		t.Errorf("lineup: %+v", lineup)
	}
	// a channel left out does not play here, even by its number
	if resp, _ := get(t, tuner+"/auto/v2"); resp.StatusCode != http.StatusNotFound {
		t.Errorf("filtered channel: status %d, want 404", resp.StatusCode)
	}
	if n := p.count("/live/xuser/xpass/2.ts") + p.count("/live/xuser/xpass/.ts"); n != 0 {
		t.Errorf("the provider was asked %d times for a filtered channel", n)
	}
}

// The guide names each channel by its tuner number, as media servers match
// them; channels of no tuner channel are left out.
func TestTunerGuide(t *testing.T) {
	p := newProvider(t)
	tuner := tunerOf(t, p, nil)

	resp, guide := get(t, tuner+"/guide.xml")
	want := `<?xml version="1.0" encoding="UTF-8"?>
<tv generator-info-name="provider">
  <channel id="1"><display-name>1</display-name><display-name>One</display-name></channel>
  <programme start="20251002200000 +0200" channel="1"><title>News</title><desc><![CDATA[<b>live</b>]]></desc></programme>
  </tv>
`
	if resp.StatusCode != http.StatusOK || guide != want {
		t.Errorf("status %d, guide:\n%s\nwant:\n%s", resp.StatusCode, guide, want)
	}
	noCredentials(t, "guide", guide)
}

func TestTunerM3U(t *testing.T) {
	p := newProvider(t)
	list := "#EXTM3U url-tvg=\"" + p.URL + "/epg.xml\"\n" +
		"#EXTINF:-1 tvg-ID=\"one.fr\" tvg-chno=\"7\",One\n" + p.URL + "/stream/a.ts\n" +
		"#EXTINF:-1 tvg-chno=\"7\",Also seven\n" + p.URL + "/hls/b.m3u8\n" +
		"#EXTINF:-1,A film\n" + p.URL + "/stream/endless.mp4\n" +
		"#EXTINF:-1 tvg-name=\"Named\",\n" + p.URL + "/stream/endless\n"
	file := t.TempDir() + "/list.m3u"
	if err := os.WriteFile(file, []byte(list), 0o600); err != nil {
		t.Fatal(err)
	}
	tuner := tunerOf(t, p, func(c *config.ProxyConfig) {
		c.XtreamBaseURL, c.XtreamUser, c.XtreamPassword = "", "", ""
		c.RemoteURL, _ = url.Parse(file)
	})

	lineup := lineupOf(t, tuner)
	// tvg-chno, else the place among live tracks; a file is not a channel
	want := []lineupEntry{{"7", "One", tuner + "/auto/v7"}, {"2", "Also seven", tuner + "/auto/v2"}, {"3", "Named", tuner + "/auto/v3"}}
	if len(lineup) != len(want) {
		t.Fatalf("lineup: %+v", lineup)
	}
	for i := range want {
		if lineup[i] != want[i] {
			t.Errorf("channel %d: %+v, want %+v", i, lineup[i], want[i])
		}
	}
	if resp, track := get(t, tuner+"/auto/v7"); resp.StatusCode != http.StatusOK || track != "track-a" {
		t.Errorf("channel 7: status %d, %q", resp.StatusCode, track)
	}
	// the M3U tuner has no Xtream account to ask: two tuners
	if _, body := get(t, tuner+"/discover.json"); !strings.Contains(body, `"TunerCount":2`) {
		t.Errorf("discover.json: %s", body)
	}

	_, guide := get(t, tuner+"/guide.xml")
	if !strings.Contains(guide, `<channel id="7"><display-name>7</display-name>`) || strings.Contains(guide, "sport.fr") || strings.Contains(guide, "one.fr") {
		t.Errorf("guide:\n%s", guide)
	}
}

func TestTunerWithoutGuide(t *testing.T) {
	p := newProvider(t)
	file := t.TempDir() + "/list.m3u"
	if err := os.WriteFile(file, []byte("#EXTM3U\n#EXTINF:-1,A\n"+p.URL+"/stream/a.ts\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tuner := tunerOf(t, p, func(c *config.ProxyConfig) {
		c.XtreamBaseURL, c.XtreamUser, c.XtreamPassword = "", "", ""
		c.RemoteURL, _ = url.Parse(file)
	})
	if resp, _ := get(t, tuner+"/guide.xml"); resp.StatusCode != http.StatusNotFound {
		t.Errorf("status %d, want 404", resp.StatusCode)
	}
}

func TestTunerProviderDown(t *testing.T) {
	p := newProvider(t)
	tuner := tunerOf(t, p, nil)
	p.down.Store(true)
	if resp, _ := get(t, tuner+"/lineup.json"); resp.StatusCode != http.StatusBadGateway {
		t.Errorf("lineup: status %d, want 502", resp.StatusCode)
	}
	if resp, _ := get(t, tuner+"/auto/v1"); resp.StatusCode != http.StatusBadGateway {
		t.Errorf("stream: status %d, want 502", resp.StatusCode)
	}
	// the tuner count falls back on 2
	if _, body := get(t, tuner+"/discover.json"); !strings.Contains(body, `"TunerCount":2`) {
		t.Errorf("discover.json: %s", body)
	}
}

func TestTunerAddress(t *testing.T) {
	c := &Config{ProxyConfig: &config.ProxyConfig{HDHomeRunPort: 5004, ListenAddress: "192.168.1.10"}}
	if got := c.hdhomerunAddress(); got != "192.168.1.10:5004" {
		t.Errorf("address = %q", got)
	}
}
