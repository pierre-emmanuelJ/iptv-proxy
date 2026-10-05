package xmltv

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
	"testing/iotest"
)

const guide = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE tv SYSTEM "xmltv.dtd">
<tv generator-info-name="provider">
  <!-- <channel id="commented"> is not a channel -->
  <channel id="one.fr">
    <display-name>One &amp; Co</display-name>
    <icon src="http://logos.example/one.png"/>
  </channel>
  <channel id='two.fr'><display-name>Two</display-name></channel>
  <channel id="a&amp;b"/>
  <channelx id="not.a.channel"/>
  <programme start="20251002200000 +0200" stop="20251002210000 +0200" channel="one.fr">
    <title lang="fr">News at "8" &gt; 7</title>
    <desc><![CDATA[a </programme> that is not one]]></desc>
  </programme>
  <programme channel="two.fr" start="20251002200000 +0200"
      stop="20251002210000 +0200"><title>Two's show</title></programme  >
  <programme start="20251002210000 +0200" channel="one.fr"><title>Late</title></programme>
</tv>
`

func filter(t *testing.T, doc string, keep func(string) bool) string {
	t.Helper()
	var out bytes.Buffer
	if err := Filter(&out, strings.NewReader(doc), keep); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

func TestKeepingEverythingChangesNothing(t *testing.T) {
	if got := filter(t, guide, func(string) bool { return true }); got != guide {
		t.Errorf("got:\n%s", got)
	}
	// read a byte at a time: element boundaries fall anywhere
	var out bytes.Buffer
	if err := Filter(&out, iotest.OneByteReader(strings.NewReader(guide)), func(string) bool { return true }); err != nil || out.String() != guide {
		t.Errorf("one byte at a time: %v\n%s", err, out.String())
	}
}

func TestChannelsLeftOut(t *testing.T) {
	var asked []string
	got := filter(t, guide, func(channel string) bool {
		asked = append(asked, channel)
		return channel == "two.fr" || channel == "a&b"
	})
	want := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE tv SYSTEM "xmltv.dtd">
<tv generator-info-name="provider">
  <!-- <channel id="commented"> is not a channel -->
  <channel id='two.fr'><display-name>Two</display-name></channel>
  <channel id="a&amp;b"/>
  <channelx id="not.a.channel"/>
  <programme channel="two.fr" start="20251002200000 +0200"
      stop="20251002210000 +0200"><title>Two's show</title></programme  >
  </tv>
`
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
	if strings.Join(asked, ",") != "one.fr,two.fr,a&b,one.fr,two.fr,one.fr" {
		t.Errorf("channels asked about: %q", asked)
	}
}

func TestMap(t *testing.T) {
	doc := `<tv>
  <channel id="one.fr"><display-name>One</display-name></channel>
  <channel id='two.fr'/>
  <channel id="gone.fr"/>
  <channel id="three.fr"><display-name>Three</display-name><display-name>Trois</display-name><icon src="3.png"/></channel>
  <programme start="1" channel="one.fr"><title>A</title></programme>
  <programme channel='two.fr' start="2"/>
  <programme channel="gone.fr" start="3"/>
</tv>`
	numbers := map[string][]string{"one.fr": {"101", "102"}, "two.fr": {"2 & \"b\""}, "three.fr": {"3"}}
	var out bytes.Buffer
	if err := Map(&out, strings.NewReader(doc), func(c string) []string { return numbers[c] }); err != nil {
		t.Fatal(err)
	}
	// the id is a name of the channel too, after its own: Plex shows the
	// first, and names come before the icon
	want := `<tv>
  <channel id="101"><display-name>One</display-name><display-name>101</display-name></channel>
<channel id="102"><display-name>One</display-name><display-name>102</display-name></channel>
  <channel id='2 &amp; &#34;b&#34;'><display-name>2 &amp; &#34;b&#34;</display-name></channel>
  <channel id="3"><display-name>Three</display-name><display-name>Trois</display-name><display-name>3</display-name><icon src="3.png"/></channel>
  <programme start="1" channel="101"><title>A</title></programme>
<programme start="1" channel="102"><title>A</title></programme>
  <programme channel='2 &amp; &#34;b&#34;' start="2"/>
  </tv>`
	if out.String() != want {
		t.Errorf("got:\n%s\nwant:\n%s", out.String(), want)
	}
}

func TestElementWithoutItsChannel(t *testing.T) {
	got := filter(t, `<tv><programme start="1"><title>x</title></programme></tv>`, func(c string) bool { return c != "" })
	if got != `<tv></tv>` {
		t.Errorf("got %q", got)
	}
}

func TestTruncatedGuide(t *testing.T) {
	for _, doc := range []string{
		`<tv><channel id="a"><display-name>A`,
		`<tv><channel id="a`,
		`<tv><programme channel="a"><title>x</title></programm`,
	} {
		err := Filter(io.Discard, strings.NewReader(doc), func(string) bool { return true })
		if !errors.Is(err, io.ErrUnexpectedEOF) {
			t.Errorf("%q: err = %v, want io.ErrUnexpectedEOF", doc, err)
		}
	}
}

func TestElementTooLarge(t *testing.T) {
	doc := `<tv><channel id="a"><icon src="data:` + strings.Repeat("A", maxElement) + `"/></channel></tv>`
	if err := Filter(io.Discard, strings.NewReader(doc), func(string) bool { return true }); !errors.Is(err, ErrElementTooLarge) {
		t.Errorf("err = %v", err)
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("client gone") }

func TestClientGone(t *testing.T) {
	big := strings.Repeat(guide, 2000)
	if err := Filter(failingWriter{}, strings.NewReader(big), func(string) bool { return true }); err == nil {
		t.Error("a failing writer must stop the filter")
	}
}

func FuzzFilter(f *testing.F) {
	f.Add(guide)
	f.Add(`<tv><channel id="a"/><programme channel="b">x</programme></tv>`)
	f.Add(`<channel<channel id="a">`)
	f.Fuzz(func(t *testing.T, doc string) {
		var all bytes.Buffer
		if err := Filter(&all, strings.NewReader(doc), func(string) bool { return true }); err == nil && all.String() != doc {
			t.Fatalf("keeping everything changed the guide:\n%q\n%q", doc, all.String())
		}
		// renaming and copying every channel never breaks on any input
		_ = Map(io.Discard, strings.NewReader(doc), func(string) []string { return []string{"1", "2 & 3"} })
		// nor adding a guide's elements to another
		var merged bytes.Buffer
		err := Merge(&merged, strings.NewReader(doc), func(c string) []string { return []string{c} }, func(w io.Writer) error {
			return Elements(w, strings.NewReader(doc), func(string) []string { return nil })
		})
		if err == nil && merged.String() != doc {
			t.Fatalf("adding no element changed the guide:\n%q\n%q", doc, merged.String())
		}
		var none bytes.Buffer
		if err := Filter(&none, strings.NewReader(doc), func(string) bool { return false }); err == nil && none.Len() > len(doc) {
			t.Fatalf("leaving channels out made the guide larger:\n%q\n%q", doc, none.String())
		}
	})
}

func TestElements(t *testing.T) {
	var out bytes.Buffer
	err := Elements(&out, strings.NewReader(guide), func(channel string) []string {
		if channel == "two.fr" {
			return []string{"two.fr"}
		}
		return nil
	})
	want := `<channel id='two.fr'><display-name>Two</display-name></channel>
<programme channel="two.fr" start="20251002200000 +0200"
      stop="20251002210000 +0200"><title>Two's show</title></programme  >
`
	if err != nil || out.String() != want {
		t.Errorf("err %v, got:\n%s\nwant:\n%s", err, out.String(), want)
	}
}

func TestMerge(t *testing.T) {
	other := `<?xml version="1.0"?><tv><!-- another provider -->
<channel id="three.fr"><display-name>Three</display-name></channel>
<programme channel="three.fr" start="20251002200000 +0200"><title>Three's show</title></programme>
<programme channel="one.fr" start="20251002200000 +0200"><title>Also on one</title></programme>
</tv>`
	var out bytes.Buffer
	err := Merge(&out, iotest.OneByteReader(strings.NewReader(guide)), func(channel string) []string { return []string{channel} }, func(w io.Writer) error {
		return Elements(w, strings.NewReader(other), func(channel string) []string {
			if channel == "three.fr" {
				return []string{channel}
			}
			return nil // already in the first guide
		})
	})
	want := strings.Replace(guide, "</tv>", `<channel id="three.fr"><display-name>Three</display-name></channel>
<programme channel="three.fr" start="20251002200000 +0200"><title>Three's show</title></programme>
</tv>`, 1)
	if err != nil || out.String() != want {
		t.Errorf("err %v, got:\n%s\nwant:\n%s", err, out.String(), want)
	}

	// what fails while the other guides are added fails the whole
	broken := errors.New("broken")
	if err := Merge(io.Discard, strings.NewReader(guide), func(channel string) []string { return nil }, func(io.Writer) error { return broken }); !errors.Is(err, broken) {
		t.Errorf("err = %v", err)
	}
}

func TestIcons(t *testing.T) {
	doc := `<tv>
  <channel id="a"><display-name>A</display-name><icon src="http://img/a.png?x=1&amp;y=2" /></channel>
  <channel id="b"><icon width="1" src='http://img/same.png?a=&#38;'/><icon src="http://img/b.png"/></channel>
  <programme start="1" channel="a"><title>T</title><icon src="http://img/p.jpg"/></programme>
  <programme start="2" channel="b"><title>&lt;icon src="http://not/an/icon"&gt;</title></programme>
</tv>`
	var asked []string
	icon := func(address string) string {
		asked = append(asked, address)
		if strings.Contains(address, "same.png") {
			return address
		}
		return "https://proxy/" + strings.TrimPrefix(address, "http://") + "&'"
	}
	var out bytes.Buffer
	if err := Map(&out, strings.NewReader(doc), func(c string) []string { return []string{c} }, Icons(icon)); err != nil {
		t.Fatal(err)
	}
	want := `<tv>
  <channel id="a"><display-name>A</display-name><icon src="https://proxy/img/a.png?x=1&amp;y=2&amp;&#39;" /></channel>
  <channel id="b"><icon width="1" src='http://img/same.png?a=&#38;'/><icon src="https://proxy/img/b.png&amp;&#39;"/></channel>
  <programme start="1" channel="a"><title>T</title><icon src="https://proxy/img/p.jpg&amp;&#39;"/></programme>
  <programme start="2" channel="b"><title>&lt;icon src="http://not/an/icon"&gt;</title></programme>
</tv>`
	// an address left as it is keeps its bytes
	if out.String() != want {
		t.Errorf("got:\n%s\nwant:\n%s", out.String(), want)
	}
	if strings.Join(asked, " ") != "http://img/a.png?x=1&y=2 http://img/same.png?a=& http://img/b.png http://img/p.jpg" {
		t.Errorf("asked: %q", asked)
	}

	// the other guides' elements too
	out.Reset()
	if err := Elements(&out, strings.NewReader(doc), func(c string) []string { return []string{c} }, Icons(func(string) string { return "x" })); err != nil {
		t.Fatal(err)
	}
	if strings.Count(out.String(), `src="x"`)+strings.Count(out.String(), `src='x'`) != 4 {
		t.Errorf("elements:\n%s", out.String())
	}
}
