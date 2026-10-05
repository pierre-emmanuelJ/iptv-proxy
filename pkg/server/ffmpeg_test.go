package server

import (
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pierre-emmanuelJ/iptv-proxy/pkg/config"
)

// withHLS gives p a short HLS stream made by ffmpeg, at /hlsfiles/index.m3u8,
// and a few broken playlists beside it. The test is skipped without ffmpeg.
func withHLS(t *testing.T, p *provider) string {
	t.Helper()
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not found")
	}
	dir := t.TempDir()
	out, err := exec.Command(ffmpeg, "-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", "testsrc=duration=4:size=96x64:rate=10",
		"-f", "lavfi", "-i", "sine=duration=4",
		"-c:v", "mpeg2video", "-c:a", "mp2",
		"-f", "hls", "-hls_time", "1", "-hls_list_size", "0", filepath.Join(dir, "index.m3u8")).CombinedOutput()
	if err != nil {
		t.Fatalf("making HLS: %v: %s", err, out)
	}
	playlists := map[string]string{
		// segments that are not there
		"broken.m3u8": "#EXTM3U\n#EXT-X-TARGETDURATION:1\n#EXTINF:1,\ngone0.ts\n#EXTINF:1,\ngone1.ts\n#EXT-X-ENDLIST\n",
		// a segment that never comes
		"slow.m3u8": "#EXTM3U\n#EXT-X-TARGETDURATION:1\n#EXTINF:1,\n" + p.URL + "/live/xuser/xpass/slow.ts\n#EXT-X-ENDLIST\n",
	}
	for name, playlist := range playlists {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(playlist), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	p.mu.Lock()
	p.hlsDir = dir
	p.mu.Unlock()
	return ffmpeg
}

// readTS reads the start of a stream and tells whether it is MPEG-TS.
func readTS(t *testing.T, address string) (int, bool) {
	t.Helper()
	resp, err := http.Get(address)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() // nolint: errcheck
	begin := make([]byte, 10*188)
	n, _ := io.ReadFull(resp.Body, begin)
	return resp.StatusCode, n == len(begin) && begin[0] == 0x47 && begin[188] == 0x47 && begin[9*188] == 0x47
}

func m3uTuner(t *testing.T, p *provider, list, ffmpeg string) string {
	t.Helper()
	file := t.TempDir() + "/list.m3u"
	if err := os.WriteFile(file, []byte(list), 0o600); err != nil {
		t.Fatal(err)
	}
	return tunerOf(t, p, func(c *config.ProxyConfig) {
		c.XtreamBaseURL, c.XtreamUser, c.XtreamPassword = "", "", ""
		c.RemoteURL, _ = url.Parse(file)
		c.FFmpeg = ffmpeg
	})
}

// A channel a playlist has only as HLS plays as MPEG-TS on the tuner; when
// ffmpeg fails on an address, the channel's next one is tried.
func TestTunerRemuxesHLS(t *testing.T) {
	logs := captureLogs(t)
	p := newProvider(t)
	ffmpeg := withHLS(t, p)
	list := "#EXTM3U\n" +
		"#EXTINF:-1 tvg-id=\"one.fr\",One\n" + p.URL + "/hlsfiles/broken.m3u8?username=" + xUser + "&password=" + xPass + "\n" +
		"#EXTINF:-1 tvg-id=\"one.fr\",One HD\n" + p.URL + "/hlsfiles/index.m3u8\n"
	tuner := m3uTuner(t, p, list, ffmpeg)

	if status, ts := readTS(t, tuner+"/auto/v1"); status != http.StatusOK || !ts {
		t.Errorf("status %d, MPEG-TS %v", status, ts)
	}
	if p.count("/hlsfiles/broken.m3u8") == 0 || p.count("/hlsfiles/index0.ts") == 0 {
		t.Errorf("ffmpeg did not read the playlists: broken %d, index segment %d", p.count("/hlsfiles/broken.m3u8"), p.count("/hlsfiles/index0.ts"))
	}
	if !strings.Contains(logs.String(), "address 1 of One failed (ffmpeg gave no stream: ") || !strings.Contains(logs.String(), "<address>") {
		t.Errorf("logs:\n%s", logs.String())
	}
	noProviderCredentials(t, "logs", logs.String())

	// without ffmpeg, the channel is the provider's playlist, as before
	_, playlist := get(t, m3uTuner(t, p, list, "none")+"/auto/v1")
	if !strings.HasPrefix(playlist, "#EXTM3U") {
		t.Errorf("without ffmpeg: %.80q", playlist)
	}
}

// An Xtream account that serves HLS only is asked for HLS, which plays as
// MPEG-TS on the tuner.
func TestTunerHLSOnlyAccount(t *testing.T) {
	p := newProvider(t)
	ffmpeg := withHLS(t, p)
	p.hlsOnly.Store(true)
	tuner := tunerOf(t, p, func(c *config.ProxyConfig) { c.FFmpeg = ffmpeg })
	if status, ts := readTS(t, tuner+"/auto/v1"); status != http.StatusOK || !ts {
		t.Errorf("status %d, MPEG-TS %v", status, ts)
	}
	if p.count("/live/xuser/xpass/1.m3u8") == 0 || p.count("/live/xuser/xpass/1.ts") != 0 {
		t.Errorf("asked as HLS %d times, as MPEG-TS %d times", p.count("/live/xuser/xpass/1.m3u8"), p.count("/live/xuser/xpass/1.ts"))
	}
	// an account that serves MPEG-TS is asked for it
	q := newProvider(t)
	withHLS(t, q)
	if _, body := get(t, tunerOf(t, q, func(c *config.ProxyConfig) { c.FFmpeg = ffmpeg })+"/auto/v1"); body != "live-one" || q.count("/live/xuser/xpass/1.m3u8") != 0 {
		t.Errorf("MPEG-TS account: %q", body)
	}
}

// What ffmpeg says when it ends by itself is logged, without addresses.
func TestFFmpegStreamEnds(t *testing.T) {
	logs := captureLogs(t)
	c := &Config{ProxyConfig: &config.ProxyConfig{XtreamBaseURL: "http://provider.example", XtreamUser: xUser, XtreamPassword: xPass}}
	cmd := exec.Command("sh", "-c", "echo 'http://provider.example/live/"+xUser+"/"+xPass+"/1.m3u8: Server returned 404 Not Found' >&2; exit 1")
	stderr := &lastLines{}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	s := &ffmpegStream{Reader: strings.NewReader("ts"), cmd: cmd, cancel: func() {}, stderr: stderr, scrub: c.scrubFFmpeg}
	if got, err := io.ReadAll(s); err != nil || string(got) != "ts" {
		t.Errorf("%q, %v", got, err)
	}
	if !strings.Contains(logs.String(), "[iptv-proxy] ffmpeg: <address> Server returned 404 Not Found") {
		t.Errorf("logs:\n%s", logs.String())
	}
	noProviderCredentials(t, "logs", logs.String())
	_ = s.Close() // already stopped: nothing more
}

func TestFFmpegSlowStart(t *testing.T) {
	start := ffmpegStart
	ffmpegStart = 100 * time.Millisecond
	t.Cleanup(func() { ffmpegStart = start })
	logs := captureLogs(t)
	p := newProvider(t)
	ffmpeg := withHLS(t, p)
	tuner := m3uTuner(t, p, "#EXTM3U\n#EXTINF:-1,Slow\n"+p.URL+"/hlsfiles/slow.m3u8\n", ffmpeg)
	if resp, _ := get(t, tuner+"/auto/v1"); resp.StatusCode != http.StatusBadGateway {
		t.Errorf("status %d, want 502", resp.StatusCode)
	}
	if !strings.Contains(logs.String(), "ffmpeg gave no stream within 100ms") {
		t.Errorf("logs:\n%s", logs.String())
	}
}

func TestFFmpegSetting(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not found")
	}
	for setting, want := range map[string]string{"": "", "none": "", "ffmpeg": ffmpeg, ffmpeg: ffmpeg} {
		c := &Config{ProxyConfig: &config.ProxyConfig{FFmpeg: setting}}
		if err := c.setupFFmpeg(); err != nil || c.ffmpeg != want {
			t.Errorf("%q: %q, %v", setting, c.ffmpeg, err)
		}
	}
	// a path that is not there is an error; the default, not found, is not
	c := &Config{ProxyConfig: &config.ProxyConfig{FFmpeg: "/nowhere/ffmpeg"}}
	if err := c.setupFFmpeg(); err == nil || !strings.Contains(err.Error(), "--ffmpeg") {
		t.Errorf("missing ffmpeg: %v", err)
	}
	t.Setenv("PATH", t.TempDir())
	c = &Config{ProxyConfig: &config.ProxyConfig{FFmpeg: "ffmpeg"}}
	if err := c.setupFFmpeg(); err != nil || c.ffmpeg != "" {
		t.Errorf("default without ffmpeg: %q, %v", c.ffmpeg, err)
	}
}

func TestScrubFFmpeg(t *testing.T) {
	c := &Config{ProxyConfig: &config.ProxyConfig{XtreamBaseURL: "http://provider.example", XtreamUser: xUser, XtreamPassword: xPass}}
	got := c.scrubFFmpeg("[hls @ 0x1] Opening 'http://provider.example/live/xuser/xpass/1.m3u8' for reading\n  Server returned 404 for " + xPass + "\n")
	if strings.Contains(got, xPass) || strings.Contains(got, "provider.example") || !strings.Contains(got, "Opening '<address>' for reading Server returned 404") {
		t.Errorf("%q", got)
	}
}
