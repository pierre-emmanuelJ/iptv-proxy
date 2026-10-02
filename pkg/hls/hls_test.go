package hls

import (
	"net/url"
	"strings"
	"testing"
)

func mustParse(t testing.TB, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

// wrap marks an address so that tests show what it was resolved to.
func wrap(u *url.URL) string {
	return "/p?u=" + url.QueryEscape(u.String())
}

func TestIsPlaylist(t *testing.T) {
	for doc, want := range map[string]bool{
		"#EXTM3U\n#EXT-X-VERSION:3": true,
		"\xEF\xBB\xBF#EXTM3U\n":     true,
		"\r\n  #EXTM3U":             true,
		"#EXTM3":                    false,
		"G@\x00\x10":                false, // MPEG-TS
		"":                          false,
		"<html>#EXTM3U</html>":      false,
	} {
		if got := IsPlaylist([]byte(doc)); got != want {
			t.Errorf("IsPlaylist(%q) = %v, want %v", doc, got, want)
		}
	}
}

func TestRewriteMediaPlaylist(t *testing.T) {
	base := mustParse(t, "http://cdn.example:8080/hls/tok/chan/index.m3u8?sig=1")
	in := "#EXTM3U\r\n" +
		"#EXT-X-VERSION:3\r\n" +
		"#EXT-X-KEY:METHOD=AES-128,URI=\"key.bin?k=1\",IV=0x01\r\n" +
		"#EXT-X-MAP:URI=\"/init/chan.mp4\",BYTERANGE=\"720@0\"\r\n" +
		"#EXTINF:10.0,\r\n" +
		"seg_1.ts?h=a&r=b\r\n" +
		"#EXTINF:10.0,\r\n" +
		"/play/hls/abc==/chan/seg_2.ts\r\n" +
		"#EXTINF:10.0,\r\n" +
		"https://other.example/seg_3.ts\r\n" +
		"\r\n" +
		"#EXT-X-ENDLIST"
	want := "#EXTM3U\r\n" +
		"#EXT-X-VERSION:3\r\n" +
		"#EXT-X-KEY:METHOD=AES-128,URI=\"/p?u=http%3A%2F%2Fcdn.example%3A8080%2Fhls%2Ftok%2Fchan%2Fkey.bin%3Fk%3D1\",IV=0x01\r\n" +
		"#EXT-X-MAP:URI=\"/p?u=http%3A%2F%2Fcdn.example%3A8080%2Finit%2Fchan.mp4\",BYTERANGE=\"720@0\"\r\n" +
		"#EXTINF:10.0,\r\n" +
		"/p?u=http%3A%2F%2Fcdn.example%3A8080%2Fhls%2Ftok%2Fchan%2Fseg_1.ts%3Fh%3Da%26r%3Db\r\n" +
		"#EXTINF:10.0,\r\n" +
		"/p?u=http%3A%2F%2Fcdn.example%3A8080%2Fplay%2Fhls%2Fabc%3D%3D%2Fchan%2Fseg_2.ts\r\n" +
		"#EXTINF:10.0,\r\n" +
		"/p?u=https%3A%2F%2Fother.example%2Fseg_3.ts\r\n" +
		"\r\n" +
		"#EXT-X-ENDLIST"
	if got := string(Rewrite([]byte(in), base, wrap)); got != want {
		t.Errorf("got:\n%q\nwant:\n%q", got, want)
	}
}

func TestRewriteMasterPlaylist(t *testing.T) {
	base := mustParse(t, "https://d1.cloudfront.example/master.m3u8")
	in := "#EXTM3U\n" +
		"#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID=\"a\",NAME=\"English, stereo\",URI=\"audio/en.m3u8\"\n" +
		"#EXT-X-MEDIA:TYPE=CLOSED-CAPTIONS,GROUP-ID=\"cc\",NAME=\"CC\",INSTREAM-ID=\"CC1\"\n" +
		"#EXT-X-STREAM-INF:BANDWIDTH=640996,CODECS=\"avc1.640015,mp4a.40.2\",AUDIO=\"a\"\n" +
		"master_33.m3u8\n" +
		"#EXT-X-I-FRAME-STREAM-INF:BANDWIDTH=86000,URI=\"iframe.m3u8\"\n"
	want := "#EXTM3U\n" +
		"#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID=\"a\",NAME=\"English, stereo\",URI=\"/p?u=https%3A%2F%2Fd1.cloudfront.example%2Faudio%2Fen.m3u8\"\n" +
		"#EXT-X-MEDIA:TYPE=CLOSED-CAPTIONS,GROUP-ID=\"cc\",NAME=\"CC\",INSTREAM-ID=\"CC1\"\n" +
		"#EXT-X-STREAM-INF:BANDWIDTH=640996,CODECS=\"avc1.640015,mp4a.40.2\",AUDIO=\"a\"\n" +
		"/p?u=https%3A%2F%2Fd1.cloudfront.example%2Fmaster_33.m3u8\n" +
		"#EXT-X-I-FRAME-STREAM-INF:BANDWIDTH=86000,URI=\"/p?u=https%3A%2F%2Fd1.cloudfront.example%2Fiframe.m3u8\"\n"
	if got := string(Rewrite([]byte(in), base, wrap)); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestRewriteLeavesWhatIsNotAnHTTPAddress(t *testing.T) {
	base := mustParse(t, "http://cdn.example/a/index.m3u8")
	in := "#EXTM3U\n" +
		"#EXT-X-KEY:METHOD=AES-128,URI=\"data:text/plain;base64,AAAA\"\n" +
		"#EXT-X-SESSION-KEY:METHOD=SAMPLE-AES,URI=\"skd://key-id\"\n" +
		"#EXT-X-DATERANGE:ID=\"ad\",X-ASSET-URI=\"http://ads.example/ad.m3u8\"\n" +
		"#EXT-X-KEY:METHOD=NONE\n" +
		"#EXT-X-KEY:METHOD=AES-128,URI=\"unterminated\n" +
		"# a comment with URI=\"x\" in the middle, URI=\"\"\n" +
		"http://[broken/seg.ts\n"
	got := string(Rewrite([]byte(in), base, wrap))
	// only the comment's address is one the proxy can serve
	want := strings.Replace(in, `URI="x"`, `URI="/p?u=http%3A%2F%2Fcdn.example%2Fa%2Fx"`, 1)
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestRewriteWhenWrapDeclines(t *testing.T) {
	base := mustParse(t, "http://cdn.example/a/index.m3u8")
	in := "#EXTM3U\nseg_1.ts\n#EXT-X-KEY:URI=\"key\"\n"
	got := string(Rewrite([]byte(in), base, func(*url.URL) string { return "" }))
	if got != in {
		t.Errorf("got %q, want the playlist unchanged", got)
	}
}

func FuzzRewrite(f *testing.F) {
	f.Add("#EXTM3U\n#EXT-X-KEY:METHOD=AES-128,URI=\"k\"\nseg.ts\r\n", "http://h/a/b.m3u8")
	f.Add("#EXTM3U\r\n\r\n#EXT-X-MAP:URI=\"\nx", "https://h/")
	f.Add("URI=\"URI=\"URI=\"", "http://h")
	f.Fuzz(func(t *testing.T, playlist, rawBase string) {
		base, err := url.Parse(rawBase)
		if err != nil {
			return
		}
		// With a wrap that declines, nothing changes: every byte is kept.
		same := Rewrite([]byte(playlist), base, func(*url.URL) string { return "" })
		if string(same) != playlist {
			t.Fatalf("declined rewrite changed the playlist:\n%q\n%q", playlist, same)
		}
		// With a real one, the line structure is kept.
		out := Rewrite([]byte(playlist), base, func(*url.URL) string { return "X" })
		if strings.Count(string(out), "\n") != strings.Count(playlist, "\n") {
			t.Fatalf("line count changed:\n%q\n%q", playlist, out)
		}
	})
}
