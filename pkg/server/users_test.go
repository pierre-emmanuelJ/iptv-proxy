package server

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/pierre-emmanuelJ/iptv-proxy/pkg/config"
	"github.com/pierre-emmanuelJ/iptv-proxy/pkg/filter"
)

// Two users: one who sees everything and may watch one stream at a time,
// and one who only sees the news.
func twoUsers(c *config.ProxyConfig) {
	c.Users = []config.User{
		{Name: "family", Password: "fampass", MaxConnections: 1},
		{Name: "kids", Password: "kidspass", Filter: filter.Patterns{Group: "^News$"}},
	}
}

const (
	family = "username=family&password=fampass"
	kids   = "username=kids&password=kidspass"
)

func TestEachUserLogsInWithTheirOwnCredentials(t *testing.T) {
	p := newProvider(t)
	base := proxy(t, p, twoUsers)

	for who, want := range map[string][2]string{family: {`"username":"family"`, `"password":"fampass"`}, kids: {`"username":"kids"`, `"password":"kidspass"`}} {
		resp, body := get(t, base+"/player_api.php?"+who)
		if resp.StatusCode != http.StatusOK || !strings.Contains(body, want[0]) || !strings.Contains(body, want[1]) {
			t.Errorf("%s: status %d, %s", who, resp.StatusCode, body)
		}
	}
	for _, refused := range []string{"username=family&password=kidspass", "username=nobody&password=fampass", creds} {
		if resp, _ := get(t, base+"/player_api.php?"+refused); resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s: status %d, want 401", refused, resp.StatusCode)
		}
	}
	// the flags' user is not one of them any more
	if resp, _ := get(t, base+"/live/me/secret/1.ts"); resp.StatusCode != http.StatusNotFound {
		t.Errorf("the flags' user still streams: status %d", resp.StatusCode)
	}
	if resp, _ := get(t, base+"/live/kids/fampass/1.ts"); resp.StatusCode != http.StatusNotFound {
		t.Errorf("a user with another's password: status %d", resp.StatusCode)
	}
}

// The login answer gives the user's limit and the streams they watch.
func TestLoginGivesTheUsersLimit(t *testing.T) {
	p := newProvider(t)
	base := proxy(t, p, twoUsers)

	_, body := get(t, base+"/player_api.php?"+family)
	// as the provider writes them: max_connections a string, active_cons a number
	if !strings.Contains(body, `"max_connections":"1"`) || !strings.Contains(body, `"active_cons":0`) {
		t.Errorf("family's login: %s", body)
	}
	_, body = get(t, base+"/player_api.php?"+kids)
	if !strings.Contains(body, `"max_connections":"2"`) {
		t.Errorf("without a limit, the account's: %s", body)
	}
}

func TestEachUserSeesTheirOwnChannels(t *testing.T) {
	p := newProvider(t)
	base := proxy(t, p, twoUsers)

	_, all := get(t, base+"/player_api.php?"+family+"&action=get_live_categories")
	_, news := get(t, base+"/player_api.php?"+kids+"&action=get_live_categories")
	if !strings.Contains(all, "Sport") || strings.Contains(news, "Sport") || !strings.Contains(news, "News") {
		t.Errorf("categories:\nfamily %s\nkids %s", all, news)
	}

	// each user's playlist holds their own credentials and channels
	_, familyList := get(t, base+"/get.php?"+family+"&type=m3u_plus&output=ts")
	_, kidsList := get(t, base+"/get.php?"+kids+"&type=m3u_plus&output=ts")
	if !strings.Contains(familyList, "/live/family/fampass/1.ts") || !strings.Contains(familyList, "A film") {
		t.Errorf("family's playlist:\n%s", familyList)
	}
	if !strings.Contains(kidsList, "/live/kids/kidspass/1.ts") || strings.Contains(kidsList, "A film") || strings.Contains(kidsList, "fampass") {
		t.Errorf("kids' playlist:\n%s", kidsList)
	}

	_, guide := get(t, base+"/xmltv.php?"+kids)
	if strings.Contains(guide, "sport.fr") || !strings.Contains(guide, "one.fr") {
		t.Errorf("kids' guide:\n%s", guide)
	}
	_, fullGuide := get(t, base+"/xmltv.php?"+family)
	if fullGuide != providerGuide {
		t.Errorf("family's guide:\n%s", fullGuide)
	}
}

// A user's filters are not only what they are shown: a channel left out
// does not play, even asked by its id.
func TestUsersFiltersHoldForStreams(t *testing.T) {
	p := newProvider(t)
	base := proxy(t, p, twoUsers)

	if resp, body := get(t, base+"/live/kids/kidspass/1.ts"); resp.StatusCode != http.StatusOK || body != "live-one" {
		t.Errorf("a kept channel: status %d, %q", resp.StatusCode, body)
	}
	for _, path := range []string{"/live/kids/kidspass/3.m3u8", "/kids/kidspass/3", "/timeshift/kids/kidspass/60/2025-10-02:20-00/2.ts"} {
		if resp, _ := get(t, base+path); resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s: status %d, want 404", path, resp.StatusCode)
		}
	}
	if n := p.count("/live/xuser/xpass/3.m3u8") + p.count("/xuser/xpass/3") + p.count("/timeshift/xuser/xpass/60/2025-10-02:20-00/2.ts"); n != 0 {
		t.Errorf("the provider was asked %d times for channels left out", n)
	}
	// movies are not filtered
	if resp, body := get(t, base+"/movie/kids/kidspass/12.mkv"); resp.StatusCode != http.StatusPartialContent || body != "film" {
		t.Errorf("movie: status %d, %q", resp.StatusCode, body)
	}
	// the family sees everything
	if resp, _ := get(t, base+"/live/family/fampass/3.m3u8"); resp.StatusCode != http.StatusOK {
		t.Errorf("family: status %d", resp.StatusCode)
	}
}

// The channel list is read once, not at every stream.
func TestUsersFiltersAreNotReadAtEveryStream(t *testing.T) {
	p := newProvider(t)
	base := proxy(t, p, twoUsers)
	for range 3 {
		get(t, base+"/live/kids/kidspass/1.ts")
	}
	// the categories and the streams, once
	if n := p.count("/player_api.php"); n != 2 {
		t.Errorf("provider API asked %d times, want 2", n)
	}
}

// opened is a stream a client watches until it ends or the client leaves.
type opened struct {
	ended chan struct{}
	leave func()
}

func open(t *testing.T, rawURL string, header ...string) (*opened, int) {
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
	o := &opened{ended: make(chan struct{}), leave: func() { cancel(); _ = resp.Body.Close() }}
	go func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		close(o.ended)
	}()
	t.Cleanup(o.leave)
	return o, resp.StatusCode
}

func endsSoon(o *opened) bool {
	select {
	case <-o.ended:
		return true
	case <-time.After(2 * time.Second):
		return false
	}
}

// At the limit, a player switching channels gets its new channel: the
// oldest stream from the same address gives its place. Another device is
// refused.
func TestConnectionLimit(t *testing.T) {
	p := newProvider(t)
	base := proxy(t, p, twoUsers)
	tv := []string{"X-Forwarded-For", "10.0.0.5"}
	phone := []string{"X-Forwarded-For", "10.0.0.9"}

	first, status := open(t, base+"/live/family/fampass/8.ts", tv...)
	if status != http.StatusOK {
		t.Fatalf("first stream: status %d", status)
	}
	if _, status := open(t, base+"/live/family/fampass/9.ts", phone...); status != http.StatusForbidden {
		t.Errorf("another device at the limit: status %d, want 403", status)
	}
	second, status := open(t, base+"/live/family/fampass/9.ts", tv...)
	if status != http.StatusOK {
		t.Fatalf("switching channels: status %d", status)
	}
	if !endsSoon(first) {
		t.Error("the first stream was not stopped")
	}
	eventually(t, "the provider's first stream closed", func() bool { return p.leftCount("/live/xuser/xpass/8.ts") == 1 })

	// once the TV stops, the phone may watch
	second.leave()
	eventually(t, "the place given back", func() bool {
		o, status := open(t, base+"/live/family/fampass/8.ts", phone...)
		o.leave()
		return status == http.StatusOK
	})
	// kids have no limit
	for range 3 {
		if _, status := open(t, base+"/live/kids/kidspass/1.ts", phone...); status != http.StatusOK {
			t.Errorf("kids: status %d", status)
		}
	}
}

func TestSlots(t *testing.T) {
	var s slots
	stopped := map[string]bool{}
	stop := func(name string) context.CancelFunc { return func() { stopped[name] = true } }

	a, ok := s.acquire(2, "tv", stop("a"))
	if !ok {
		t.Fatal("a")
	}
	if _, ok := s.acquire(2, "phone", stop("b")); !ok {
		t.Fatal("b")
	}
	if _, ok := s.acquire(2, "laptop", stop("c")); ok || s.count() != 2 {
		t.Error("a third address at the limit is refused")
	}
	if _, ok := s.acquire(2, "tv", stop("d")); !ok || !stopped["a"] || stopped["b"] || s.count() != 2 {
		t.Errorf("the tv's oldest stream gives its place: %v, %d", stopped, s.count())
	}
	s.release(a) // already given away: nothing happens
	if s.count() != 2 {
		t.Errorf("count = %d", s.count())
	}
	for range 5 {
		if _, ok := s.acquire(0, "any", stop("x")); !ok {
			t.Error("no limit")
		}
	}
}

func TestM3UUsers(t *testing.T) {
	p := newProvider(t)
	list := "#EXTM3U\n" +
		"#EXTINF:-1 group-title=\"News\",One\n" + p.URL + "/stream/a.ts\n" +
		"#EXTINF:-1 group-title=\"Sport\",Two\n" + p.URL + "/hls/b.m3u8\n"
	file := t.TempDir() + "/list.m3u"
	if err := os.WriteFile(file, []byte(list), 0o600); err != nil {
		t.Fatal(err)
	}
	base := m3uProxy(t, p, func(c *config.ProxyConfig) {
		c.RemoteURL, _ = url.Parse(file)
		twoUsers(c)
	})

	_, familyList := get(t, base+"/iptv.m3u?"+family)
	_, kidsList := get(t, base+"/iptv.m3u?"+kids)
	if !strings.Contains(familyList, "/family/fampass/") || !strings.Contains(familyList, ",Two") {
		t.Errorf("family:\n%s", familyList)
	}
	if !strings.Contains(kidsList, "/kids/kidspass/") || strings.Contains(kidsList, ",Two") || strings.Contains(kidsList, "fampass") {
		t.Errorf("kids:\n%s", kidsList)
	}

	two := trackKey(p.URL+"/hls/b.m3u8") + "/b.m3u8"
	if resp, _ := get(t, base+"/tracks/family/fampass/"+two); resp.StatusCode != http.StatusOK {
		t.Errorf("family's track: status %d", resp.StatusCode)
	}
	if resp, _ := get(t, base+"/tracks/kids/kidspass/"+two); resp.StatusCode != http.StatusNotFound {
		t.Errorf("a track left out of kids': status %d", resp.StatusCode)
	}
}

func TestAccessLogHidesEveryUsersCredentials(t *testing.T) {
	logs := captureLogs(t)
	p := newProvider(t)
	base := proxy(t, p, twoUsers)
	get(t, base+"/live/family/fampass/1.ts")
	get(t, base+"/live/kids/kidspass/1.ts")
	get(t, base+"/player_api.php?"+kids)
	for _, secret := range []string{"fampass", "kidspass", "/family/", "/kids/"} {
		if strings.Contains(logs.String(), secret) {
			t.Errorf("%q in the logs:\n%s", secret, logs.String())
		}
	}
}

func TestInvalidUsers(t *testing.T) {
	for name, users := range map[string][]config.User{
		"no password":     {{Name: "a"}},
		"no name":         {{Password: "a"}},
		"twice":           {{Name: "a", Password: "x"}, {Name: "a", Password: "y"}},
		"a path's name":   {{Name: "live", Password: "x"}},
		"invalid filters": {{Name: "a", Password: "x", Filter: filter.Patterns{Channel: "("}}},
	} {
		_, err := NewServer(&config.ProxyConfig{HostConfig: &config.HostConfiguration{Hostname: "proxy.example", Port: 8080}, Users: users})
		if err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

// A channel the provider adds is found: a list older than a minute is read
// again when a stream is not in it.
func TestUsersFiltersFollowTheProvider(t *testing.T) {
	stale := lineupStale
	lineupStale = 0
	t.Cleanup(func() { lineupStale = stale })

	p := newProvider(t)
	base := proxy(t, p, twoUsers)
	get(t, base+"/live/kids/kidspass/1.ts") // the list is read

	p.late.Store(true) // a news channel appears
	if resp, _ := get(t, base+"/live/kids/kidspass/4.ts"); resp.StatusCode == http.StatusNotFound && p.count("/live/xuser/xpass/4.ts") == 0 {
		t.Error("a channel added by the provider is refused")
	}
}

// The answers kept for when the provider fails are each user's: one user
// never gets another's, which holds their credentials and channels.
func TestLastGoodAnswersAreEachUsers(t *testing.T) {
	p := newProvider(t)
	base := proxy(t, p, twoUsers)

	get(t, base+"/player_api.php?"+family+"&action=get_live_streams")
	p.down.Store(true)
	resp, body := get(t, base+"/player_api.php?"+kids+"&action=get_live_streams")
	if resp.StatusCode != http.StatusBadGateway || strings.Contains(body, "Two") {
		t.Errorf("kids got family's answer: status %d, %s", resp.StatusCode, body)
	}
}
