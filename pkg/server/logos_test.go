package server

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/pierre-emmanuelJ/iptv-proxy/pkg/config"
)

func proxyLogos(c *config.ProxyConfig) { c.ProxyLogos = true }

func TestAPIImagesThroughTheProxy(t *testing.T) {
	p := newProvider(t)
	base := proxy(t, p, proxyLogos)

	_, body := get(t, base+"/player_api.php?"+creds+"&action=get_series_info&series_id=7")
	var info struct {
		Info struct {
			Name     string   `json:"name"`
			Cover    string   `json:"cover"`
			Backdrop []string `json:"backdrop_path"`
		} `json:"info"`
	}
	if err := json.Unmarshal([]byte(body), &info); err != nil {
		t.Fatalf("%v: %s", err, body)
	}
	if strings.Contains(body, p.cdn.URL) || strings.Contains(body, strings.ReplaceAll(p.cdn.URL, "/", `\/`)) {
		t.Errorf("the images' host is still named:\n%s", body)
	}
	if info.Info.Name != "Show" || len(info.Info.Backdrop) != 1 {
		t.Errorf("the rest of the answer changed:\n%s", body)
	}
	for want, address := range map[string]string{"jpeg:cover.jpg": info.Info.Cover, "jpeg:back.jpg": info.Info.Backdrop[0]} {
		if !strings.HasPrefix(address, "http://proxy.example:8080/logo/") {
			t.Errorf("image address %q", address)
			continue
		}
		resp, image := get(t, base+strings.TrimPrefix(address, "http://proxy.example:8080"))
		if resp.StatusCode != http.StatusOK || image != want || resp.Header.Get("Content-Type") != "image/jpeg" {
			t.Errorf("%s: status %d, %q, %s", address, resp.StatusCode, image, resp.Header.Get("Content-Type"))
		}
	}

	// a logo token the proxy did not give out is refused
	if resp, _ := get(t, base+"/logo/not-a-token/x.png"); resp.StatusCode != http.StatusNotFound {
		t.Errorf("unknown token: status %d", resp.StatusCode)
	}
}

func TestImagesLeftAloneByDefault(t *testing.T) {
	p := newProvider(t)
	base := proxy(t, p, nil)
	_, body := get(t, base+"/player_api.php?"+creds+"&action=get_series_info&series_id=7")
	if !strings.Contains(body, strings.ReplaceAll(p.cdn.URL, "/", `\/`)+`\/img\/cover.jpg`) {
		t.Errorf("without --proxy-logos, the answer is the provider's:\n%s", body)
	}
	_, playlist := get(t, base+"/apiget?"+creds+"&output=ts")
	if !strings.Contains(playlist, `tvg-logo="http://logos.example/one.png"`) {
		t.Errorf("without --proxy-logos, logos are the provider's:\n%s", playlist)
	}
}

func TestPlaylistLogosThroughTheProxy(t *testing.T) {
	p := newProvider(t)
	base := proxy(t, p, proxyLogos)
	_, playlist := get(t, base+"/apiget?"+creds+"&output=ts")
	if strings.Contains(playlist, "logos.example") || !strings.Contains(playlist, `tvg-logo="http://proxy.example:8080/logo/`) {
		t.Errorf("generated playlist:\n%s", playlist)
	}

	m3u := m3uProxy(t, p, func(c *config.ProxyConfig) {
		c.ProxyLogos = true
		c.RemoteURL, _ = url.Parse(p.URL + "/leaky.m3u?username=" + xUser + "&password=" + xPass)
	})
	_, body := get(t, m3u+"/iptv.m3u?"+creds)
	if strings.Contains(body, "logos.example") || !strings.Contains(body, `tvg-logo="http://proxy.example:8080/logo/`) {
		t.Errorf("M3U playlist:\n%s", body)
	}
	// a data: image stays in the playlist
	_, inline := get(t, m3uProxy(t, p, proxyLogos)+"/iptv.m3u?"+creds)
	if !strings.Contains(inline, `tvg-logo="data:image/png;base64,AAAA"`) {
		t.Errorf("inline logo:\n%s", inline)
	}
}

func TestLogoAddress(t *testing.T) {
	c := &Config{ProxyConfig: &config.ProxyConfig{HostConfig: &config.HostConfiguration{Hostname: "proxy.example"}, AdvertisedPort: 8080}}
	var err error
	if c.tokens, err = newAddressTokens("me", "secret"); err != nil {
		t.Fatal(err)
	}
	for _, kept := range []string{"", "data:image/png;base64,AAAA", "ftp://host/x.png", "rtmp://host/x.png", "/relative.png", "http://", "http://[broken/x.png"} {
		if got := c.logoAddress(kept); got != "" {
			t.Errorf("%q: %q, want it left alone", kept, got)
		}
	}
	got := c.logoAddress(" https://img.example/a/b.jpg?w=1 ")
	if !strings.HasPrefix(got, "http://proxy.example:8080/logo/") || !strings.HasSuffix(got, "/b.jpg") {
		t.Errorf("logo address %q", got)
	}
	if got := c.logoAddress("https://img.example/"); !strings.HasSuffix(got, "/logo") {
		t.Errorf("an address without a file name: %q", got)
	}
}
