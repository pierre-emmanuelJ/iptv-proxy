package m3u

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

const sample = "\xEF\xBB\xBF#EXTM3U url-tvg=\"http://guide.example/epg.xml\"\r\n" +
	"#EXTINF:-1 tvg-id=\"\" tvg-name=\"News, 24/7\" tvg-logo=\"data:image/png;base64,AAAA\" group-title=\"News\",News, 24/7\r\n" +
	"#EXTGRP:News\r\n" +
	"#EXTVLCOPT:http-user-agent=Player\r\n" +
	"http://provider.example/live/u/p/1.ts\r\n" +
	"\r\n" +
	"#EXTINF:0,Radio\r\n" +
	"http://provider.example/radio.mp3\r\n" +
	"http://provider.example/bare.ts\r\n"

func TestParseKeepsEveryLineOfATrack(t *testing.T) {
	p, err := Parse(strings.NewReader(sample))
	if err != nil {
		t.Fatal(err)
	}
	if p.Header != `#EXTM3U url-tvg="http://guide.example/epg.xml"` {
		t.Errorf("header = %q", p.Header)
	}
	if len(p.Tracks) != 3 {
		t.Fatalf("tracks = %d, want 3", len(p.Tracks))
	}

	news := p.Tracks[0]
	if !strings.Contains(news.ExtInf, `tvg-id=""`) || !strings.Contains(news.ExtInf, "base64,AAAA") {
		t.Errorf("attributes were altered: %q", news.ExtInf)
	}
	if got := news.Name(); got != "News, 24/7" {
		t.Errorf("name = %q: a comma inside the quoted attributes or the name must not cut it", got)
	}
	if len(news.Extra) != 2 || news.Extra[0] != "#EXTGRP:News" {
		t.Errorf("extra lines = %q", news.Extra)
	}
	if news.URI != "http://provider.example/live/u/p/1.ts" {
		t.Errorf("uri = %q", news.URI)
	}
	if bare := p.Tracks[2]; bare.ExtInf != "" || bare.URI != "http://provider.example/bare.ts" {
		t.Errorf("a bare address is a track of its own, got %+v", bare)
	}
}

func TestWriteRoundTrip(t *testing.T) {
	p, err := Parse(strings.NewReader(sample))
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if _, err := p.WriteTo(&out); err != nil {
		t.Fatal(err)
	}
	want := strings.TrimPrefix(strings.ReplaceAll(strings.ReplaceAll(sample, "\r\n\r\n", "\r\n"), "\r\n", "\n"), "\xEF\xBB\xBF")
	if out.String() != want {
		t.Errorf("round trip differs:\n got: %q\nwant: %q", out.String(), want)
	}

	again, err := Parse(&out)
	if err != nil || len(again.Tracks) != len(p.Tracks) {
		t.Fatalf("written playlist does not parse back: %v", err)
	}
}

func TestParseRejectsWhatIsNotAPlaylist(t *testing.T) {
	for name, doc := range map[string]string{
		"empty":      "",
		"html error": "<html><body>403 Forbidden</body></html>",
		"json error": `{"user_info":{"auth":0}}`,
	} {
		if _, err := Parse(strings.NewReader(doc)); !errors.Is(err, ErrNoHeader) {
			t.Errorf("%s: err = %v, want ErrNoHeader", name, err)
		}
	}
}

func TestParseLongLine(t *testing.T) {
	logo := strings.Repeat("A", 1<<20) // far above bufio.Scanner's default
	doc := "#EXTM3U\n#EXTINF:-1 tvg-logo=\"" + logo + "\",Big\nhttp://x/1.ts\n"
	p, err := Parse(strings.NewReader(doc))
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Tracks) != 1 || p.Tracks[0].Name() != "Big" {
		t.Fatalf("tracks = %+v", len(p.Tracks))
	}
}

func TestExtInfLine(t *testing.T) {
	got := ExtInfLine("One", [2]string{"tvg-id", "one.fr"}, [2]string{"tvg-logo", ""}, [2]string{"group-title", `The "A" list`})
	want := `#EXTINF:-1 tvg-id="one.fr" group-title="The 'A' list",One`
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func FuzzParse(f *testing.F) {
	f.Add(sample)
	f.Add("#EXTM3U\n#EXTINF:-1,\n")
	f.Add("#EXTM3U\n#EXTINF:-1 a=\"b,c\n\n\nhttp://x")
	f.Fuzz(func(t *testing.T, doc string) {
		p, err := Parse(strings.NewReader(doc))
		if err != nil {
			return
		}
		var out bytes.Buffer
		if _, err := p.WriteTo(&out); err != nil {
			t.Fatal(err)
		}
		again, err := Parse(&out)
		if err != nil {
			t.Fatalf("written playlist does not parse back: %v", err)
		}
		if len(again.Tracks) != len(p.Tracks) {
			t.Fatalf("tracks: %d written, %d read back", len(p.Tracks), len(again.Tracks))
		}
		for i := range p.Tracks {
			_ = p.Tracks[i].Name()
		}
	})
}
