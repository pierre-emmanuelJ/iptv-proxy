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

func TestGroup(t *testing.T) {
	for want, track := range map[string]Track{
		"News":    {ExtInf: `#EXTINF:-1 tvg-name="A, B" group-title="News",group-title="Not this"`},
		"Sport":   {ExtInf: `#EXTINF:-1,Sport`, Extra: []string{"#EXTVLCOPT:x=y", "#EXTGRP: Sport "}},
		"Kept":    {ExtInf: `#EXTINF:-1 group-title="Kept"`, Extra: []string{"#EXTGRP:Other"}},
		"":        {ExtInf: `#EXTINF:-1,No group`},
		"Unnamed": {ExtInf: `#EXTINF:-1 group-title="Unnamed"`},
	} {
		if got := track.Group(); got != want {
			t.Errorf("Group(%+v) = %q, want %q", track, got, want)
		}
	}
}

func TestAttribute(t *testing.T) {
	line := `#EXTINF:-1 tvg-id="" catchup-source="http://h/a?b=1,c" group-title="G",Name group-title="no"`
	for name, want := range map[string]string{"tvg-id": "", "catchup-source": "http://h/a?b=1,c", "group-title": "G"} {
		if got, ok := Attribute(line, name); !ok || got != want {
			t.Errorf("Attribute(%q) = %q, %v; want %q", name, got, ok, want)
		}
	}
	if got, ok := Attribute(`#EXTINF:-1 tvg-ID="one.fr" Group-Title="G",One`, "tvg-id"); !ok || got != "one.fr" {
		t.Errorf("names regardless of case: %q, %v", got, ok)
	}
	if got := (Track{ExtInf: `#EXTINF:-1 GROUP-TITLE="G",One`}).Group(); got != "G" {
		t.Errorf("group regardless of case: %q", got)
	}
	if _, ok := Attribute(line, "tvg-logo"); ok {
		t.Error("an absent attribute was found")
	}
	if got, ok := Attribute(`#EXTM3U url-tvg="http://g/e.xml" x-tvg-url="y"`, "x-tvg-url"); !ok || got != "y" {
		t.Errorf("header attribute = %q, %v", got, ok)
	}
}

func TestEditAttributes(t *testing.T) {
	edit := func(name, value string) (string, bool) {
		switch name {
		case "catchup-source":
			return "", false
		case "tvg-logo":
			return `new "logo"`, true
		}
		return value, true
	}
	for line, want := range map[string]string{
		`#EXTINF:-1 catchup-source="x" tvg-logo="old" group-title="G",Name catchup-source="y"`: `#EXTINF:-1 tvg-logo="new 'logo'" group-title="G",Name catchup-source="y"`,
		`#EXTINF:-1  tvg-id="i"   catchup-source="x",Name`:                                     `#EXTINF:-1  tvg-id="i",Name`,
		`#EXTM3U catchup-source="x" url-tvg="g"`:                                               `#EXTM3U url-tvg="g"`,
		`#EXTM3U catchup-source="x"`:                                                           `#EXTM3U`,
		`#EXTINF:-1,No attributes`:                                                             `#EXTINF:-1,No attributes`,
	} {
		if got := EditAttributes(line, edit); got != want {
			t.Errorf("EditAttributes(%q)\n got %q\nwant %q", line, got, want)
		}
	}
	unchanged := `#EXTINF:-1 tvg-id="i" group-title="G",Name`
	if got := EditAttributes(unchanged, func(_, v string) (string, bool) { return v, true }); got != unchanged {
		t.Errorf("an edit that changes nothing changed %q into %q", unchanged, got)
	}
}

func FuzzEditAttributes(f *testing.F) {
	f.Add(`#EXTINF:-1 a="b" c="d,e",Name f="g"`)
	f.Add(`#EXTM3U url-tvg="x"`)
	f.Add(`#EXTINF:-1 a="`)
	f.Fuzz(func(t *testing.T, line string) {
		// keeping every value as it is keeps the line as it is
		if got := EditAttributes(line, func(_, v string) (string, bool) { return v, true }); got != line {
			t.Fatalf("identity edit changed %q into %q", line, got)
		}
		// dropping attributes shortens the line and keeps the display name
		dropped := EditAttributes(line, func(string, string) (string, bool) { return "", false })
		if len(dropped) > len(line) || !strings.HasSuffix(dropped, line[len(attributes(line)):]) {
			t.Fatalf("dropping attributes of %q gave %q", line, dropped)
		}
	})
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
