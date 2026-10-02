package restream

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"sync"
	"testing"
	"time"
)

// provider is a fake provider: each connection is a pipe the test writes
// the stream into.
type provider struct {
	mu    sync.Mutex
	conns []*conn
	// next decides the answer to the n-th opening (from 0); nil means a
	// live stream.
	next func(n int) (*http.Response, error)
}

type conn struct {
	w   *io.PipeWriter
	ctx context.Context
}

func (p *provider) open(ctx context.Context) (*http.Response, error) {
	p.mu.Lock()
	n := len(p.conns)
	r, w := io.Pipe()
	p.conns = append(p.conns, &conn{w: w, ctx: ctx})
	next := p.next
	p.mu.Unlock()

	if next != nil {
		if resp, err := next(n); resp != nil || err != nil {
			return resp, err
		}
	}
	// A real connection ends when its request is cancelled.
	go func() {
		<-ctx.Done()
		_ = r.CloseWithError(ctx.Err())
	}()
	return &http.Response{
		StatusCode:    http.StatusOK,
		Header:        http.Header{"Content-Type": {"video/mp2t"}},
		Body:          r,
		ContentLength: -1,
	}, nil
}

func (p *provider) opened() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.conns)
}

func (p *provider) conn(t *testing.T, n int) *conn {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		p.mu.Lock()
		if n < len(p.conns) {
			c := p.conns[n]
			p.mu.Unlock()
			return c
		}
		p.mu.Unlock()
		if time.Now().After(deadline) {
			t.Fatalf("the provider was not asked a connection #%d", n)
		}
		time.Sleep(time.Millisecond)
	}
}

func (c *conn) closedByHub(t *testing.T) {
	t.Helper()
	select {
	case <-c.ctx.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("the provider's connection is still open")
	}
}

func hub() *Hub {
	h := NewHub()
	h.Retries = []time.Duration{0, time.Millisecond, time.Millisecond}
	h.Healthy = time.Hour
	h.Stall = 0
	return h
}

// packets returns n MPEG-TS packets whose second byte is id.
func packets(id byte, n int) []byte {
	out := make([]byte, 0, n*tsPacket)
	for i := 0; i < n; i++ {
		p := make([]byte, tsPacket)
		p[0], p[1] = tsSyncByte, id
		out = append(out, p...)
	}
	return out
}

func write(t *testing.T, c *conn, data []byte) {
	t.Helper()
	done := make(chan error, 1)
	go func() {
		_, err := c.w.Write(data)
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("writing to the hub: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the hub does not read the provider's stream")
	}
}

// read collects what a client receives until it has n bytes.
func read(t *testing.T, sub *Subscription, n int) []byte {
	t.Helper()
	var got []byte
	timeout := time.After(3 * time.Second)
	for len(got) < n {
		select {
		case chunk, ok := <-sub.C:
			if !ok {
				t.Fatalf("the stream ended after %d of %d bytes", len(got), n)
			}
			got = append(got, chunk...)
		case <-timeout:
			t.Fatalf("got %d of %d bytes", len(got), n)
		}
	}
	return got
}

func ended(t *testing.T, sub *Subscription) {
	t.Helper()
	timeout := time.After(3 * time.Second)
	for {
		select {
		case _, ok := <-sub.C:
			if !ok {
				return
			}
		case <-timeout:
			t.Fatal("the stream did not end for the client")
		}
	}
}

func join(t *testing.T, h *Hub, p *provider) *Subscription {
	t.Helper()
	type result struct {
		sub  *Subscription
		resp *http.Response
		err  error
	}
	done := make(chan result, 1)
	before := p.opened()
	go func() {
		sub, resp, err := h.Join("channel", p.open)
		done <- result{sub, resp, err}
	}()

	// The first client waits for the stream's first bytes, on the
	// connection its arrival opened.
	fed := false
	for {
		select {
		case r := <-done:
			if r.err != nil || r.sub == nil {
				t.Fatalf("join: sub=%v resp=%v err=%v", r.sub, r.resp, r.err)
			}
			return r.sub
		case <-time.After(20 * time.Millisecond):
			if !fed && p.opened() > before {
				fed = true
				c := p.conn(t, before)
				go func() { _, _ = c.w.Write(packets(0, 3)) }()
			}
		}
	}
}

func TestOneProviderConnectionForEveryClient(t *testing.T) {
	h, p := hub(), &provider{}

	first := join(t, h, p)
	read(t, first, 3*tsPacket) // the stream's first packets
	second := join(t, h, p)
	third := join(t, h, p)
	if n := p.opened(); n != 1 {
		t.Fatalf("the provider was asked %d connections for one channel", n)
	}
	if first.Header.Get("Content-Type") != "video/mp2t" || second.Header.Get("Content-Type") != "video/mp2t" {
		t.Error("clients do not get the provider's headers")
	}

	c := p.conn(t, 0)
	write(t, c, packets(1, 4))
	for name, sub := range map[string]*Subscription{"first": first, "second": second, "third": third} {
		if got := read(t, sub, 4*tsPacket); !bytes.Equal(got, packets(1, 4)) {
			t.Errorf("%s client: got %d bytes that are not what the provider sent", name, len(got))
		}
	}

	// The provider's stream lives as long as someone watches.
	first.Close()
	second.Close()
	second.Close() // leaving twice is harmless
	write(t, c, packets(2, 1))
	if got := read(t, third, tsPacket); !bytes.Equal(got, packets(2, 1)) {
		t.Error("the last client stopped receiving when the others left")
	}
	if c.ctx.Err() != nil {
		t.Fatal("the provider's connection was closed while a client was watching")
	}

	third.Close()
	c.closedByHub(t)
	if h.Streams() != 0 {
		t.Errorf("%d streams left in the hub", h.Streams())
	}

	// Watching again later opens a new connection.
	again := join(t, h, p)
	defer again.Close()
	if n := p.opened(); n != 2 {
		t.Errorf("connections = %d, want a second one", n)
	}
}

func TestClientsArrivingTogetherShareOneConnection(t *testing.T) {
	h, p := hub(), &provider{}

	const clients = 20
	subs := make(chan *Subscription, clients)
	for i := 0; i < clients; i++ {
		go func() {
			sub, _, err := h.Join("channel", p.open)
			if err != nil {
				t.Errorf("join: %v", err)
			}
			subs <- sub
		}()
	}
	write(t, p.conn(t, 0), packets(0, 3))

	for i := 0; i < clients; i++ {
		select {
		case sub := <-subs:
			if sub == nil {
				t.Fatal("a client did not get the shared stream")
			}
			defer sub.Close()
		case <-time.After(3 * time.Second):
			t.Fatal("a client is still waiting")
		}
	}
	if n := p.opened(); n != 1 {
		t.Errorf("the provider was asked %d connections", n)
	}
}

func TestStreamIsOpenedAgainWhenTheProviderDropsIt(t *testing.T) {
	h, p := hub(), &provider{}
	sub := join(t, h, p)
	defer sub.Close()
	read(t, sub, 3*tsPacket)

	// dropped in the middle of a packet
	write(t, p.conn(t, 0), append(packets(1, 1), tsSyncByte, 1, 2, 3))
	_ = p.conn(t, 0).w.Close()

	second := p.conn(t, 1)
	write(t, second, packets(2, 2))
	got := read(t, sub, 3*tsPacket)
	if !bytes.Equal(got, append(packets(1, 1), packets(2, 2)...)) {
		t.Errorf("the client got %d bytes: the half packet of the dropped connection must not be sent", len(got))
	}
}

// The connection stays open but nothing comes any more: a frozen picture
// for the player. It is opened again.
func TestSilentProviderIsAskedAgain(t *testing.T) {
	h, p := hub(), &provider{}
	h.Stall = 60 * time.Millisecond
	sub := join(t, h, p)
	defer sub.Close()
	read(t, sub, 3*tsPacket)

	// data keeps the connection: no reopening while it flows
	first := p.conn(t, 0)
	for i := 0; i < 5; i++ {
		time.Sleep(30 * time.Millisecond)
		write(t, first, packets(1, 1))
		read(t, sub, tsPacket)
	}
	if n := p.opened(); n != 1 {
		t.Fatalf("a stream that flows was opened again (%d connections)", n)
	}

	// then silence
	second := p.conn(t, 1)
	first.closedByHub(t)
	write(t, second, packets(2, 1))
	if got := read(t, sub, tsPacket); !bytes.Equal(got, packets(2, 1)) {
		t.Error("the stream did not resume on the new connection")
	}
}

func TestProviderAnswersBadlyBeforeComingBack(t *testing.T) {
	h := hub()
	p := &provider{next: func(n int) (*http.Response, error) {
		switch n {
		case 1:
			return nil, errors.New("connection refused")
		case 2:
			return &http.Response{StatusCode: http.StatusServiceUnavailable, Body: io.NopCloser(bytes.NewReader(nil))}, nil
		}
		return nil, nil
	}}
	sub := join(t, h, p)
	defer sub.Close()
	read(t, sub, 3*tsPacket)

	_ = p.conn(t, 0).w.Close()
	write(t, p.conn(t, 3), packets(5, 1))
	if got := read(t, sub, tsPacket); !bytes.Equal(got, packets(5, 1)) {
		t.Error("the stream did not resume on the connection that worked")
	}
}

func TestStreamEndsWhenTheProviderIsGone(t *testing.T) {
	h := hub()
	p := &provider{next: func(n int) (*http.Response, error) {
		if n > 0 {
			return nil, errors.New("connection refused")
		}
		return nil, nil
	}}
	sub := join(t, h, p)
	read(t, sub, 3*tsPacket)

	_ = p.conn(t, 0).w.Close()
	ended(t, sub)
	if n := p.opened(); n != 1+len(h.Retries) {
		t.Errorf("the provider was asked %d times, want 1 and %d retries", n, len(h.Retries))
	}
	if h.Streams() != 0 {
		t.Errorf("%d streams left in the hub", h.Streams())
	}
}

// A provider that accepts the connection and drops it at once, again and
// again, must not be asked forever.
func TestProviderThatKeepsDroppingIsGivenUp(t *testing.T) {
	h, p := hub(), &provider{}
	sub := join(t, h, p)
	read(t, sub, 3*tsPacket)

	for n := 0; n <= len(h.Retries); n++ {
		c := p.conn(t, n)
		if n > 0 {
			write(t, c, packets(byte(n), 1))
		}
		_ = c.w.Close()
	}
	ended(t, sub)
	if n := p.opened(); n != 1+len(h.Retries) {
		t.Errorf("the provider was asked %d times, want %d", n, 1+len(h.Retries))
	}
}

// ... but one that lasted starts again from the first attempt.
func TestALastingConnectionResetsTheAttempts(t *testing.T) {
	h, p := hub(), &provider{}
	h.Healthy = 0
	sub := join(t, h, p)
	defer sub.Close()
	read(t, sub, 3*tsPacket)

	for n := 0; n < 3*len(h.Retries); n++ {
		_ = p.conn(t, n).w.Close()
		write(t, p.conn(t, n+1), packets(byte(n), 1))
		read(t, sub, tsPacket)
	}
}

func TestSlowClientDoesNotHoldTheOthersBack(t *testing.T) {
	h, p := hub(), &provider{}
	h.Backlog = 4
	fast := join(t, h, p)
	defer fast.Close()
	read(t, fast, 3*tsPacket)
	slow := join(t, h, p)

	c := p.conn(t, 0)
	for i := 0; i < 20; i++ {
		write(t, c, packets(byte(i), 1))
		if got := read(t, fast, tsPacket); got[1] != byte(i) {
			t.Fatalf("fast client: packet %d is %d", i, got[1])
		}
	}
	ended(t, slow) // dropped, after what it had time to get
	slow.Close()
	if c.ctx.Err() != nil {
		t.Error("dropping the slow client closed the stream of the other")
	}
}

func TestWholePacketsOnly(t *testing.T) {
	h, p := hub(), &provider{}
	first := join(t, h, p)
	defer first.Close()
	read(t, first, 3*tsPacket)

	c := p.conn(t, 0)
	stream := packets(7, 5)
	write(t, c, stream[:tsPacket+100]) // one packet and a piece of the next
	late := join(t, h, p)              // joins while a packet is half read
	defer late.Close()
	write(t, c, stream[tsPacket+100:])

	for name, sub := range map[string]*Subscription{"first": first, "late": late} {
		timeout := time.After(3 * time.Second)
		total := 0
		for total < len(stream)-tsPacket {
			select {
			case chunk := <-sub.C:
				if len(chunk)%tsPacket != 0 || chunk[0] != tsSyncByte {
					t.Fatalf("%s client: a chunk of %d bytes starting with %#x is not whole packets", name, len(chunk), chunk[0])
				}
				total += len(chunk)
			case <-timeout:
				t.Fatalf("%s client: got %d bytes", name, total)
			}
		}
	}
}

func TestStreamThatIsNotMPEGTSIsPassedAsItComes(t *testing.T) {
	h, p := hub(), &provider{}
	done := make(chan *Subscription, 1)
	go func() {
		sub, _, _ := h.Join("radio", p.open)
		done <- sub
	}()
	c := p.conn(t, 0)
	audio := bytes.Repeat([]byte("ICY-audio-"), 50)
	write(t, c, audio)
	sub := <-done
	defer sub.Close()

	write(t, c, []byte("tail"))
	if got := read(t, sub, len(audio)+4); !bytes.Equal(got, append(audio, "tail"...)) {
		t.Errorf("got %d bytes that differ from the %d sent", len(got), len(audio)+4)
	}
}

func TestAnswerThatIsNotALiveStreamIsNotShared(t *testing.T) {
	h := hub()
	file := func() *http.Response {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader([]byte("a whole file"))), ContentLength: 12}
	}
	notFound := func() *http.Response {
		return &http.Response{StatusCode: http.StatusNotFound, Body: io.NopCloser(bytes.NewReader([]byte("no such channel"))), ContentLength: -1}
	}
	for name, answer := range map[string]func() *http.Response{"a file": file, "an error": notFound} {
		p := &provider{next: func(int) (*http.Response, error) { return answer(), nil }}

		for client := 0; client < 2; client++ {
			sub, resp, err := h.Join(name, p.open)
			if err != nil || sub != nil || resp == nil {
				t.Fatalf("%s: sub=%v resp=%v err=%v, want the answer itself", name, sub, resp, err)
			}
			body, _ := io.ReadAll(resp.Body)
			if want, _ := io.ReadAll(answer().Body); !bytes.Equal(body, want) {
				t.Errorf("%s: body = %q, the first bytes looked at must not be lost", name, body)
			}
			if p.conn(t, client).ctx.Err() != nil {
				t.Errorf("%s: the answer was cancelled before the client closed it", name)
			}
			_ = resp.Body.Close()
			p.conn(t, client).closedByHub(t)
		}
		if n := p.opened(); n != 2 {
			t.Errorf("%s: %d connections for two clients, want one each", name, n)
		}
	}
	if h.Streams() != 0 {
		t.Errorf("%d streams left in the hub", h.Streams())
	}
}

func TestProviderUnreachable(t *testing.T) {
	h := hub()
	refused := errors.New("connection refused")
	p := &provider{next: func(int) (*http.Response, error) { return nil, refused }}

	if sub, resp, err := h.Join("channel", p.open); !errors.Is(err, refused) || sub != nil || resp != nil {
		t.Errorf("sub=%v resp=%v err=%v", sub, resp, err)
	}
	if h.Streams() != 0 {
		t.Errorf("%d streams left in the hub", h.Streams())
	}
	p.conn(t, 0).closedByHub(t)
}

func TestCustomShareable(t *testing.T) {
	h := hub()
	h.Shareable = func(_ *http.Response, begin []byte) bool { return !bytes.HasPrefix(begin, []byte("#EXTM3U")) }
	p := &provider{}

	done := make(chan *http.Response, 1)
	go func() {
		_, resp, _ := h.Join("hls", p.open)
		done <- resp
	}()
	c := p.conn(t, 0)
	go func() {
		_, _ = c.w.Write([]byte("#EXTM3U\nseg.ts\n"))
		_ = c.w.Close()
	}()
	select {
	case resp := <-done:
		if resp == nil {
			t.Fatal("a playlist was shared as a live stream")
		}
		if body, _ := io.ReadAll(resp.Body); string(body) != "#EXTM3U\nseg.ts\n" {
			t.Errorf("body = %q", body)
		}
		_ = resp.Body.Close()
	case <-time.After(3 * time.Second):
		t.Fatal("no answer")
	}
}
