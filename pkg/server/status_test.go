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
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pierre-emmanuelJ/iptv-proxy/pkg/config"
)

const statusPassword = "look"

func withStatus(c *config.ProxyConfig) {
	c.StatusPassword = statusPassword
	c.Version = "v9.9.9"
}

func statusOf(t *testing.T, base, path, password string) (*http.Response, string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, base+path, nil)
	if password != "" {
		req.SetBasicAuth("anyone", password)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() // nolint: errcheck
	var b strings.Builder
	buf := make([]byte, 4096)
	for {
		n, err := resp.Body.Read(buf)
		b.Write(buf[:n])
		if err != nil {
			break
		}
	}
	return resp, b.String()
}

func TestStatusPageNeedsItsPassword(t *testing.T) {
	p := newProvider(t)
	if resp, _ := statusOf(t, proxy(t, p, nil), "/status", statusPassword); resp.StatusCode != http.StatusNotFound {
		t.Errorf("without --status-password: status %d, want 404", resp.StatusCode)
	}

	base := proxy(t, p, withStatus)
	for _, password := range []string{"", "wrong", pass} {
		resp, _ := statusOf(t, base, "/status", password)
		if resp.StatusCode != http.StatusUnauthorized || !strings.Contains(resp.Header.Get("WWW-Authenticate"), "Basic") {
			t.Errorf("password %q: status %d", password, resp.StatusCode)
		}
	}
	resp, page := statusOf(t, base, "/status", statusPassword)
	if resp.StatusCode != http.StatusOK || !strings.Contains(page, "iptv-proxy v9.9.9") || !strings.Contains(page, "<td>me</td>") || !strings.Contains(page, "Nobody is watching.") {
		t.Errorf("status %d:\n%s", resp.StatusCode, page)
	}
}

// The page shows who watches what, from where, without any credentials.
func TestStatusShowsTheStreams(t *testing.T) {
	p := newProvider(t)
	base := proxy(t, p, func(c *config.ProxyConfig) {
		twoUsers(c)
		withStatus(c)
	})
	watching, code := open(t, base+"/live/family/fampass/8.ts", "X-Forwarded-For", "10.0.0.5")
	if code != http.StatusOK {
		t.Fatalf("stream: status %d", code)
	}
	eventually(t, "the stream open", func() bool { return p.count("/live/xuser/xpass/8.ts") == 1 })
	get(t, base+"/player_api.php?"+family+"&action=get_live_streams") // kept

	_, body := statusOf(t, base, "/status.json", statusPassword)
	var s status
	if err := json.Unmarshal([]byte(body), &s); err != nil {
		t.Fatal(err)
	}
	if len(s.Users) != 2 || s.Users[0].Name != "family" || len(s.Users[0].Streams) != 1 || s.Shared != 1 || s.Watched != 1 {
		t.Fatalf("status: %s", body)
	}
	if s.Kept != 1 || s.KeptBytes == 0 {
		t.Errorf("kept answers: %d, %d bytes", s.Kept, s.KeptBytes)
	}
	if got := s.Users[0].Streams[0]; got.Address != "/live/***/***/8.ts" || got.From != "10.0.0.5" || s.Users[0].Limit != 1 {
		t.Errorf("stream: %+v, limit %d", got, s.Users[0].Limit)
	}
	_, page := statusOf(t, base, "/status", statusPassword)
	for _, text := range []string{body, page} {
		for _, secret := range []string{"fampass", "kidspass", xUser, xPass, statusPassword} {
			if strings.Contains(text, secret) {
				t.Errorf("%q on the status page:\n%s", secret, text)
			}
		}
	}
	if !strings.Contains(page, "<td>/live/***/***/8.ts</td><td>10.0.0.5</td>") {
		t.Errorf("page:\n%s", page)
	}

	watching.leave()
	eventually(t, "the stream gone from the page", func() bool {
		_, body := statusOf(t, base, "/status.json", statusPassword)
		return strings.Contains(body, `"streams":[]`) && !strings.Contains(body, "8.ts")
	})
}

func TestStatusOfSourcesAndTuner(t *testing.T) {
	p, q := newProvider(t), newProvider(t)
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
		HDHomeRunPort:      5004,
	}
	withSource(q)(conf)
	withStatus(conf)
	c, err := NewServer(conf)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(c.Handler())
	t.Cleanup(srv.Close)
	tuner := httptest.NewServer(c.HDHomeRunHandler())
	t.Cleanup(tuner.Close)
	lineupOf(t, tuner.URL)
	get(t, srv.URL+"/live/me/secret/100000005.ts")

	_, body := statusOf(t, srv.URL, "/status.json", statusPassword)
	var s status
	if err := json.Unmarshal([]byte(body), &s); err != nil {
		t.Fatal(err)
	}
	if len(s.Sources) != 2 || s.Sources[1].Name != "backup" || s.Sources[1].Host != strings.TrimPrefix(q.URL, "http://") || s.Sources[1].Limit != 1 {
		t.Errorf("sources: %+v", s.Sources)
	}
	if s.Tuner == nil || s.Tuner.Port != 5004 || s.Tuner.Channels != 6 {
		t.Errorf("tuner: %+v", s.Tuner)
	}
	if strings.Contains(body, x2Pass) || strings.Contains(body, xPass) {
		t.Errorf("credentials on the status page: %s", body)
	}
}

// Passthrough accounts are listed as they log in.
func TestStatusOfPassthrough(t *testing.T) {
	p := newProvider(t)
	base := proxy(t, p, func(c *config.ProxyConfig) {
		passthroughMode(c)
		withStatus(c)
	})
	get(t, base+"/player_api.php?"+firstAccount)
	_, body := statusOf(t, base, "/status.json", statusPassword)
	if !strings.Contains(body, `"name":"account xuser"`) || !strings.Contains(body, `"passthrough_accounts":1`) || strings.Contains(body, xPass) {
		t.Errorf("status: %s", body)
	}
}

func TestFormatBytes(t *testing.T) {
	for n, want := range map[int]string{0: "0 B", 1023: "1023 B", 1536: "1.5 KB", 3 << 20: "3.0 MB"} {
		if got := formatBytes(n); got != want {
			t.Errorf("%d: %s, want %s", n, got, want)
		}
	}
}
