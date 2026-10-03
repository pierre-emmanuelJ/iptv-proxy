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
	"strings"
	"testing"

	"github.com/pierre-emmanuelJ/iptv-proxy/pkg/config"
	"github.com/pierre-emmanuelJ/iptv-proxy/pkg/filter"
)

// passthroughMode is a proxy that knows only its provider's address.
func passthroughMode(c *config.ProxyConfig) {
	c.XtreamPassthrough = true
	c.XtreamUser, c.XtreamPassword = "", ""
}

var (
	firstAccount  = "username=" + xUser + "&password=" + xPass
	secondAccount = "username=" + x2User + "&password=" + url.QueryEscape(x2Pass)
)

func TestPassthroughLogin(t *testing.T) {
	p := newProvider(t)
	base := proxy(t, p, passthroughMode)

	for _, account := range []struct {
		query, name, password, connections string
	}{
		{firstAccount, xUser, xPass, "2"},
		{secondAccount, x2User, x2Pass, "1"},
	} {
		resp, body := get(t, base+"/player_api.php?"+account.query)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s: status %d: %s", account.name, resp.StatusCode, body)
		}
		var login struct {
			UserInfo   map[string]any `json:"user_info"`
			ServerInfo map[string]any `json:"server_info"`
		}
		if err := json.Unmarshal([]byte(body), &login); err != nil {
			t.Fatal(err)
		}
		if login.UserInfo["username"] != account.name || login.UserInfo["password"] != account.password {
			t.Errorf("%s: the answer names %v/%v", account.name, login.UserInfo["username"], login.UserInfo["password"])
		}
		// the account's own limit, as the proxy holds it
		if login.UserInfo["max_connections"] != account.connections {
			t.Errorf("%s: max_connections %v, want %s", account.name, login.UserInfo["max_connections"], account.connections)
		}
		if login.ServerInfo["url"] != "http://proxy.example" {
			t.Errorf("%s: server url %v", account.name, login.ServerInfo["url"])
		}
	}

	for name, query := range map[string]string{
		"wrong password":  "username=" + xUser + "&password=nope",
		"refused by 401":  "username=locked&password=x",
		"refused by []":   "username=empty&password=x",
		"proxy's own one": creds,
	} {
		if resp, _ := get(t, base+"/player_api.php?"+query); resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s: status %d, want 401", name, resp.StatusCode)
		}
	}
}

func TestPassthroughAnswersNameTheProxy(t *testing.T) {
	p := newProvider(t)
	base := proxy(t, p, passthroughMode)
	host := strings.TrimPrefix(p.URL, "http://")

	for _, path := range []string{
		"/player_api.php?" + firstAccount + "&action=get_vod_info&vod_id=12",
		"/get.php?" + firstAccount + "&type=m3u_plus",
		"/iptv.m3u?" + firstAccount,
	} {
		resp, body := get(t, base+path)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s: status %d", path, resp.StatusCode)
		}
		if strings.Contains(body, host) || strings.Contains(body, strings.ReplaceAll(p.URL, "/", `\/`)) {
			t.Errorf("%s: the provider's address reached the client:\n%s", path, body)
		}
		if !strings.Contains(body, "proxy.example:8080") {
			t.Errorf("%s: no proxy address in:\n%s", path, body)
		}
	}

	// The playlist's streams, guide and catch-up play with the account that
	// asked for it: they name its password, which the client knows.
	_, playlist := get(t, base+"/get.php?"+firstAccount+"&type=m3u_plus")
	for _, want := range []string{
		"\nhttp://proxy.example:8080/live/xuser/xpass/1.ts\n",
		`url-tvg="http://proxy.example:8080/xmltv.php?username=xuser&password=xpass"`,
		`catchup-source="http://proxy.example:8080/timeshift/xuser/xpass/{duration}/{start}/1.ts"`,
	} {
		if !strings.Contains(playlist, want) {
			t.Errorf("%s not in the playlist:\n%s", want, playlist)
		}
	}
	// what names the provider's host is left out, credentials or not
	_, playlist = get(t, base+"/get.php?"+secondAccount+"&type=hosted")
	if strings.Contains(playlist, host) || strings.Contains(playlist, "tvg-logo") || !strings.Contains(playlist, "/live/second/pw+2/1.ts") {
		t.Errorf("playlist:\n%s", playlist)
	}
	resp, page := get(t, base+"/player_api.php?"+firstAccount+"&action=where")
	if resp.StatusCode != http.StatusNotFound || strings.Contains(page, host) || !strings.Contains(page, "proxy.example") || resp.Header.Get("Link") != "" {
		t.Errorf("error page: status %d, Link %q:\n%s", resp.StatusCode, resp.Header.Get("Link"), page)
	}
	if resp, body := get(t, base+"/xmltv.php?"+firstAccount); resp.StatusCode != http.StatusOK || !strings.Contains(body, "<tv") {
		t.Errorf("guide: status %d", resp.StatusCode)
	}
}

func TestPassthroughStreams(t *testing.T) {
	p := newProvider(t)
	base := proxy(t, p, passthroughMode)

	for path, want := range map[string]string{
		"/live/xuser/xpass/1.ts":                          "live-one",
		"/xuser/xpass/1":                                  "short-form",
		"/movie/xuser/xpass/12.mkv":                       "film",
		"/series/xuser/xpass/5.mp4":                       "episode",
		"/timeshift/xuser/xpass/60/2025-10-02:20-00/1.ts": "catch-up",
		"/live/second/pw+2/1.ts":                          "second-one",
	} {
		if resp, body := get(t, base+path); resp.StatusCode >= 300 || body != want {
			t.Errorf("%s: status %d, %q, want %q", path, resp.StatusCode, body, want)
		}
	}

	// HLS addresses still belong to the proxy, not to an account
	_, playlist := get(t, base+"/live/xuser/xpass/2.m3u8")
	segments := hlsAddresses(t, playlist, "")
	if _, body := get(t, base+segments[0]); body != "segment-from-cdn" {
		t.Errorf("HLS segment: %q", body)
	}

	// an account the provider refuses watches nothing, and costs it nothing
	for _, path := range []string{"/live/xuser/wrong/1.ts", "/xuser/wrong/1", "/movie/locked/x/12.mkv"} {
		if resp, _ := get(t, base+path); resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s: status %d, want 401", path, resp.StatusCode)
		}
	}
	if n := p.count("/live/xuser/wrong/1.ts"); n != 0 {
		t.Errorf("the provider was asked a stream of a refused account %d times", n)
	}
}

// An account is checked once, not at every request: zapping would otherwise
// ask the provider's login each time.
func TestPassthroughChecksAnAccountOnce(t *testing.T) {
	p := newProvider(t)
	base := proxy(t, p, passthroughMode)

	for range 3 {
		get(t, base+"/live/xuser/xpass/1.ts")
	}
	if n := p.count("/player_api.php"); n != 1 {
		t.Errorf("the provider's login was asked %d times for 3 streams", n)
	}
}

// An account the provider accepted keeps playing while the provider cannot
// say; one it never accepted does not get in.
func TestPassthroughWhenTheProviderFails(t *testing.T) {
	check := accountCheck
	accountCheck = 0 // every request asks the provider again
	t.Cleanup(func() { accountCheck = check })

	p := newProvider(t)
	base := proxy(t, p, passthroughMode)
	if resp, _ := get(t, base+"/live/xuser/xpass/1.ts"); resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}

	p.down.Store(true)
	if resp, body := get(t, base+"/live/xuser/xpass/1.ts"); resp.StatusCode != http.StatusOK || body != "live-one" {
		t.Errorf("an accepted account, the provider down: status %d", resp.StatusCode)
	}
	if resp, _ := get(t, base+"/live/second/pw+2/1.ts"); resp.StatusCode != http.StatusBadGateway {
		t.Errorf("an unknown account, the provider down: status %d, want 502", resp.StatusCode)
	}
	if n := p.count("/live/second/pw+2/1.ts"); n != 0 {
		t.Errorf("the stream of an unchecked account was asked %d times", n)
	}
}

// A provider answering a maintenance page cannot accept anyone: that is its
// failure, not a refusal.
func TestPassthroughLoginAnswerNotJSON(t *testing.T) {
	p := newProvider(t)
	base := proxy(t, p, passthroughMode)
	p.garbage.Store(true)
	if resp, _ := get(t, base+"/player_api.php?"+firstAccount); resp.StatusCode != http.StatusBadGateway {
		t.Errorf("status %d, want 502", resp.StatusCode)
	}
}

// An account the provider stops accepting (expired, cancelled) is refused
// at its next check.
func TestPassthroughAccountRevoked(t *testing.T) {
	check := accountCheck
	accountCheck = 0
	t.Cleanup(func() { accountCheck = check })

	p := newProvider(t)
	base := proxy(t, p, passthroughMode)
	if resp, _ := get(t, base+"/live/second/pw+2/1.ts"); resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	p.revoked.Store(true)
	if resp, _ := get(t, base+"/live/second/pw+2/1.ts"); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("a revoked account: status %d, want 401", resp.StatusCode)
	}
}

// The proxy holds each account to the streams it allows: on a one-stream
// account, zapping on the same device works, another device is refused.
func TestPassthroughLimitIsTheAccounts(t *testing.T) {
	p := newProvider(t)
	base := proxy(t, p, passthroughMode)
	tv := []string{"X-Forwarded-For", "10.0.0.5"}
	phone := []string{"X-Forwarded-For", "10.0.0.9"}

	first, status := open(t, base+"/live/second/pw+2/8.ts", tv...)
	if status != http.StatusOK {
		t.Fatalf("first stream: status %d", status)
	}
	if _, status := open(t, base+"/live/second/pw+2/1.ts", phone...); status != http.StatusForbidden {
		t.Errorf("another device at the limit: status %d, want 403", status)
	}
	if _, status := open(t, base+"/live/second/pw+2/1.ts", tv...); status != http.StatusOK {
		t.Errorf("switching channels: status %d", status)
	}
	if !endsSoon(first) {
		t.Error("the first stream was not stopped")
	}
	// the other account has its own places
	if _, status := open(t, base+"/live/xuser/xpass/8.ts", phone...); status != http.StatusOK {
		t.Errorf("another account: status %d", status)
	}
}

// --max-connections, when set, is each account's limit instead.
func TestPassthroughConfiguredLimit(t *testing.T) {
	p := newProvider(t)
	base := proxy(t, p, func(c *config.ProxyConfig) {
		passthroughMode(c)
		c.MaxConnections = 3
	})
	_, body := get(t, base+"/player_api.php?"+secondAccount)
	if !strings.Contains(body, `"max_connections":"3"`) {
		t.Errorf("login: %s", body)
	}
}

// The proxy's filters hold for every account, on each account's own
// channels.
func TestPassthroughFilters(t *testing.T) {
	p := newProvider(t)
	base := proxy(t, p, func(c *config.ProxyConfig) {
		passthroughMode(c)
		c.Filter = filter.Patterns{Group: "^News$"}
	})

	_, body := get(t, base+"/player_api.php?"+secondAccount+"&action=get_live_streams")
	if !strings.Contains(body, `"One"`) || strings.Contains(body, `"Two"`) {
		t.Errorf("live streams:\n%s", body)
	}
	if resp, _ := get(t, base+"/live/second/pw+2/2.ts"); resp.StatusCode != http.StatusNotFound {
		t.Errorf("a channel left out: status %d, want 404", resp.StatusCode)
	}
	if resp, _ := get(t, base+"/live/second/pw+2/1.ts"); resp.StatusCode != http.StatusOK {
		t.Errorf("a channel kept: status %d", resp.StatusCode)
	}
}

// Accounts of a provider may have different channels: each is filtered on
// its own.
func TestPassthroughAccountsHaveTheirOwnChannels(t *testing.T) {
	p := newProvider(t)
	base := proxy(t, p, func(c *config.ProxyConfig) {
		passthroughMode(c)
		c.Filter = filter.Patterns{Group: "^News$"}
	})

	if resp, _ := get(t, base+"/live/xuser/xpass/1.ts"); resp.StatusCode != http.StatusOK {
		t.Fatalf("first account: status %d", resp.StatusCode)
	}
	if resp, body := get(t, base+"/live/second/pw+2/5.ts"); resp.StatusCode != http.StatusOK || body != "second-five" {
		t.Errorf("a channel of the second account only: status %d", resp.StatusCode)
	}
	_, body := get(t, base+"/player_api.php?"+secondAccount+"&action=get_live_streams")
	if !strings.Contains(body, `"Local news"`) {
		t.Errorf("second account's live streams:\n%s", body)
	}
}

// What is kept for when the provider fails is each account's own.
func TestPassthroughLastGoodAnswersAreEachAccounts(t *testing.T) {
	p := newProvider(t)
	base := proxy(t, p, passthroughMode)
	get(t, base+"/player_api.php?"+firstAccount+"&action=get_live_streams")
	get(t, base+"/player_api.php?"+secondAccount) // accepted, nothing kept

	p.down.Store(true)
	if resp, body := get(t, base+"/player_api.php?"+firstAccount+"&action=get_live_streams"); resp.StatusCode != http.StatusOK || !strings.Contains(body, `"One"`) {
		t.Errorf("the account's kept answer: status %d", resp.StatusCode)
	}
	if resp, body := get(t, base+"/player_api.php?"+secondAccount+"&action=get_live_streams"); resp.StatusCode == http.StatusOK {
		t.Errorf("another account got the first one's answer:\n%s", body)
	}
}

func TestPassthroughAccessLogHidesTheAccounts(t *testing.T) {
	for _, endpoint := range []string{"", "tv"} {
		logs := captureLogs(t)
		p := newProvider(t)
		base := proxy(t, p, func(c *config.ProxyConfig) {
			passthroughMode(c)
			c.CustomEndpoint = endpoint
		})
		prefix := base
		if endpoint != "" {
			prefix += "/" + endpoint
		}
		get(t, prefix+"/live/xuser/xpass/1.ts")
		get(t, prefix+"/xuser/xpass/1")
		get(t, prefix+"/timeshift/xuser/xpass/60/2025-10-02:20-00/1.ts")
		get(t, prefix+"/live/someone/typo/1.ts") // refused, logged all the same
		get(t, prefix+"/player_api.php?"+secondAccount)
		for _, secret := range []string{xPass, "/xuser/", "typo", "someone", "pw+2", "pw%2B2", "second"} {
			if strings.Contains(logs.String(), secret) {
				t.Errorf("endpoint %q: %q in the logs:\n%s", endpoint, secret, logs.String())
			}
		}
		if !strings.Contains(logs.String(), "/live/***/***/1.ts") {
			t.Errorf("endpoint %q: the stream is not logged:\n%s", endpoint, logs.String())
		}
	}
}

func TestPassthroughSettings(t *testing.T) {
	remote, _ := url.Parse("http://provider.example/list.m3u")
	for name, mutate := range map[string]func(*config.ProxyConfig){
		"no provider":        func(c *config.ProxyConfig) { c.XtreamBaseURL = "" },
		"a provider account": func(c *config.ProxyConfig) { c.XtreamUser, c.XtreamPassword = xUser, xPass },
		"an M3U playlist":    func(c *config.ProxyConfig) { c.RemoteURL = remote },
		"users of its own":   func(c *config.ProxyConfig) { c.Users = []config.User{{Name: "a", Password: "b"}} },
		"an HDHomeRun tuner": func(c *config.ProxyConfig) { c.HDHomeRunPort = 5004 },
	} {
		conf := &config.ProxyConfig{
			HostConfig:        &config.HostConfiguration{Hostname: "proxy.example", Port: 8080},
			XtreamBaseURL:     "http://provider.example",
			XtreamPassthrough: true,
		}
		mutate(conf)
		if _, err := NewServer(conf); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}
