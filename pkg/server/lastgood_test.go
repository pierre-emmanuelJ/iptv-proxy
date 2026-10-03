package server

import (
	"bytes"
	"io"
	"math/rand/v2"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/pierre-emmanuelJ/iptv-proxy/pkg/config"
	"github.com/pierre-emmanuelJ/iptv-proxy/pkg/filter"
)

// When the provider fails, clients get the last answer it gave in full
// rather than an error.
func TestAPIServesTheLastGoodAnswer(t *testing.T) {
	logs := captureLogs(t)
	p := newProvider(t)
	base := proxy(t, p, nil)

	_, live := get(t, base+"/player_api.php?"+creds+"&action=get_live_streams")
	_, login := get(t, base+"/player_api.php?"+creds)

	p.down.Store(true)
	resp, body := get(t, base+"/player_api.php?"+creds+"&action=get_live_streams")
	if resp.StatusCode != http.StatusOK || body != live {
		t.Errorf("provider down: status %d, body:\n%s\nwant the last good one:\n%s", resp.StatusCode, body, live)
	}
	if resp.Header.Get("Age") == "" || resp.Header.Get("Content-Type") != "application/json; charset=utf-8" {
		t.Errorf("headers of the last good answer: %v", resp.Header)
	}
	if _, body := get(t, base+"/player_api.php?"+creds); body != login {
		t.Errorf("login with the provider down:\n%s\nwant:\n%s", body, login)
	}
	if !strings.Contains(logs.String(), "get_live_streams: the provider failed (HTTP 502)") {
		t.Errorf("not logged:\n%s", logs.String())
	}

	// nothing kept for this one: the provider's error is passed on
	if resp, _ := get(t, base+"/player_api.php?"+creds+"&action=get_vod_streams"); resp.StatusCode != http.StatusBadGateway {
		t.Errorf("nothing kept: status = %d, want the provider's 502", resp.StatusCode)
	}

	// a maintenance page with a 200 is no answer either
	p.down.Store(false)
	p.garbage.Store(true)
	if _, body := get(t, base+"/player_api.php?"+creds+"&action=get_live_streams"); body != live {
		t.Errorf("maintenance page: got\n%s", body)
	}
	if resp, body := get(t, base+"/player_api.php?"+creds+"&action=get_vod_streams"); resp.StatusCode != http.StatusOK || body != "<html>Down for maintenance</html>" {
		t.Errorf("nothing kept: status %d, body %q, want the provider's page as before", resp.StatusCode, body)
	}
	// and it does not replace the last good answer
	p.garbage.Store(false)
	p.down.Store(true)
	if _, body := get(t, base+"/player_api.php?"+creds+"&action=get_vod_streams"); body == "<html>Down for maintenance</html>" {
		t.Error("a maintenance page was kept as a good answer")
	}
}

// Answers differ by their parameters, not by who asked.
func TestLastGoodAnswersByParameters(t *testing.T) {
	p := newProvider(t)
	base := proxy(t, p, nil)

	_, epg := get(t, base+"/player_api.php?"+creds+"&action=get_short_epg&stream_id=1")
	p.down.Store(true)
	if _, body := get(t, base+"/player_api.php?action=get_short_epg&stream_id=1&password="+pass+"&username="+user); body != epg {
		t.Errorf("same parameters in another order: got %s", body)
	}
	if resp, _ := get(t, base+"/player_api.php?"+creds+"&action=get_short_epg&stream_id=2"); resp.StatusCode != http.StatusBadGateway {
		t.Errorf("another stream: status = %d, want 502", resp.StatusCode)
	}
}

// A refusal says something about the account: it is not hidden.
func TestRefusalIsNotHidden(t *testing.T) {
	if providerFailed(http.StatusForbidden, nil) || providerFailed(http.StatusUnauthorized, nil) || providerFailed(http.StatusOK, nil) {
		t.Error("401, 403 and 200 are not failures to hide")
	}
	if !providerFailed(http.StatusNotFound, nil) || !providerFailed(http.StatusServiceUnavailable, nil) || !providerFailed(0, errProviderGone) {
		t.Error("404, 5xx and no answer are")
	}
}

var errProviderGone = &url.Error{Op: "Get", URL: "x", Err: http.ErrServerClosed}

func TestPlaylistServesTheLastGoodOne(t *testing.T) {
	p := newProvider(t)
	// a playlist kept no time at all: each request asks the provider
	base := proxy(t, p, func(c *config.ProxyConfig) { c.M3UCacheExpiration = 0 })

	_, playlist := get(t, base+"/get.php?"+creds+"&type=m3u_plus")
	p.down.Store(true)
	resp, body := get(t, base+"/get.php?"+creds+"&type=m3u_plus")
	if resp.StatusCode != http.StatusOK || body != playlist {
		t.Errorf("provider down: status %d, playlist:\n%s", resp.StatusCode, body)
	}
	if n := p.count("/get.php"); n != 2 {
		t.Errorf("provider asked %d times, want 2", n)
	}
}

func TestGuideServesTheLastGoodOne(t *testing.T) {
	for name, patterns := range map[string]filter.Patterns{"whole": {}, "filtered": {Group: "News"}} {
		p := newProvider(t)
		base := proxy(t, p, filtered(patterns))

		_, guide := get(t, base+"/xmltv.php?"+creds)
		p.down.Store(true)
		resp, body := get(t, base+"/xmltv.php?"+creds)
		if resp.StatusCode != http.StatusOK || body != guide {
			t.Errorf("%s: provider down: status %d, guide:\n%s\nwant:\n%s", name, resp.StatusCode, body, guide)
		}
		// asked with other parameters, there is nothing to fall back on
		if resp, _ := get(t, base+"/xmltv.php?"+creds+"&days=1"); resp.StatusCode != http.StatusBadGateway {
			t.Errorf("%s: other parameters: status = %d, want 502", name, resp.StatusCode)
		}
	}
}

// A guide that did not arrive in full is not kept.
func TestCutGuideIsNotKept(t *testing.T) {
	p := newProvider(t)
	base := proxy(t, p, nil)

	resp, err := http.Get(base + "/xmltv.php?" + creds + "&cut=1")
	if err == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}

	p.down.Store(true)
	if resp, body := get(t, base+"/xmltv.php?"+creds+"&cut=1"); resp.StatusCode != http.StatusBadGateway {
		t.Errorf("a cut guide was kept: status %d, %q", resp.StatusCode, body)
	}
}

// Answers are kept by what was asked, whoever asked it: the credentials are
// not part of the key, and do not stay in memory with it.
func TestAnswerKey(t *testing.T) {
	a := answerKey("player_api.php", url.Values{"username": {"me"}, "password": {"secret"}, "action": {"get_live_streams"}, "category_id": {"1"}})
	b := answerKey("player_api.php", url.Values{"category_id": {"1"}, "action": {"get_live_streams"}, "username": {"you"}, "password": {"other"}})
	if a != b || strings.Contains(a, "secret") || strings.Contains(a, "me") {
		t.Errorf("keys %q and %q", a, b)
	}
	if answerKey("player_api.php", url.Values{"action": {"get_live_streams"}, "category_id": {"2"}}) == a {
		t.Error("other parameters, same key")
	}
	if answerKey("guide", nil) == answerKey("player_api.php", nil) {
		t.Error("other endpoint, same key")
	}
}

func TestLastGoodIsBounded(t *testing.T) {
	limit := maxLastGoodBytes
	maxLastGoodBytes = 100
	t.Cleanup(func() { maxLastGoodBytes = limit })

	l := newLastGood()
	l.put("a", "x", bytes.Repeat([]byte{1}, 40))
	l.put("b", "x", bytes.Repeat([]byte{1}, 40))
	l.put("a", "x", bytes.Repeat([]byte{1}, 50)) // replaces a: 90 bytes
	if _, ok := l.get("b"); !ok || l.size != 90 {
		t.Fatalf("size = %d", l.size)
	}
	l.put("c", "x", bytes.Repeat([]byte{1}, 30)) // 120: the oldest, b, goes
	if _, ok := l.get("b"); ok {
		t.Error("the oldest answer was not dropped")
	}
	if _, ok := l.get("a"); !ok || l.size != 80 {
		t.Errorf("size = %d", l.size)
	}
	l.put("big", "x", bytes.Repeat([]byte{1}, 101))
	if _, ok := l.get("big"); ok {
		t.Error("an answer larger than the whole room was kept")
	}

	r := newRecorder()
	noise := rand.New(rand.NewPCG(1, 2)) // nothing gzip can shrink
	for range 10 {
		chunk := make([]byte, 64)
		for i := range chunk {
			chunk[i] = byte(noise.Uint32())
		}
		_, _ = r.Write(chunk)
	}
	if _, ok := r.finish(); ok {
		t.Error("a recording larger than the room is not kept")
	}
	small := newRecorder()
	_, _ = small.Write([]byte("tiny"))
	if _, ok := small.finish(); !ok {
		t.Error("a small recording is kept")
	}
}
