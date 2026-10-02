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

// Package restream shares one provider connection between every client
// watching the same live stream, and keeps it alive.
//
// An IPTV account allows a few connections, often a single one. Without
// sharing, two devices on the same channel take two of them, and a provider
// dropping the connection ends the stream for the player. Here the first
// client opens the provider's stream, the next ones join it where it is
// (live television has no beginning), and a dropped connection is opened
// again while clients are still watching. The provider's stream is closed as
// soon as the last client leaves.
package restream

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net/http"
	"sync"
	"time"
)

const (
	tsPacket   = 188
	tsSyncByte = 0x47

	readBytes = 64 << 10
	// enough to see two packet starts
	sniffBytes = tsPacket + 1
)

// Source opens the provider's stream. Its context is cancelled when the
// stream is no longer needed.
type Source func(ctx context.Context) (*http.Response, error)

// Hub holds the live streams being watched.
type Hub struct {
	// Shareable tells whether a provider answer is a live stream to share,
	// given its first bytes. Anything else (an error, a file, a playlist) is
	// handed to the client that asked for it, alone. Nil means: a 200 answer
	// of unknown length.
	Shareable func(resp *http.Response, start []byte) bool
	// Retries are the waits before each attempt to open again a stream the
	// provider dropped. After the last one, the stream ends for its clients.
	Retries []time.Duration
	// Healthy is how long a connection must last for the attempts to start
	// again from the first one.
	Healthy time.Duration
	// Stall is how long a provider may send nothing before its connection
	// is taken for dead and opened again. Zero waits forever.
	Stall time.Duration
	// Backlog is how many chunks may wait for a client. A client that falls
	// further behind is dropped: it must not hold the others back, nor
	// grow the proxy's memory.
	Backlog int

	mu      sync.Mutex
	streams map[string]*stream
}

// NewHub returns a hub with sensible defaults.
func NewHub() *Hub {
	return &Hub{
		Retries: []time.Duration{0, 500 * time.Millisecond, time.Second, 2 * time.Second, 4 * time.Second},
		Healthy: 10 * time.Second,
		Stall:   20 * time.Second,
		Backlog: 256, // about 16 MB at most: a few seconds of a high bitrate stream
		streams: map[string]*stream{},
	}
}

// ErrEnded is what a client of a stream gets when the provider could not be
// reached again.
var ErrEnded = errors.New("the provider's stream ended")

// Subscription is one client's place on a shared stream.
type Subscription struct {
	// Header is the provider's response header, as it was when the stream
	// was opened.
	Header http.Header
	// C delivers the stream. It is closed when the stream ends for this
	// client: the provider is gone for good, or the client fell too far
	// behind.
	C <-chan []byte

	hub    *Hub
	stream *stream
	ch     chan []byte
}

// Close leaves the stream. The provider's stream is closed with its last
// client.
func (s *Subscription) Close() {
	s.hub.mu.Lock()
	defer s.hub.mu.Unlock()
	s.stream.remove(s)
}

type stream struct {
	hub  *Hub
	key  string
	open Source

	// ready is closed once the first answer of the provider is known.
	ready chan struct{}
	// shared tells whether that answer was a live stream.
	shared bool

	header http.Header
	body   io.ReadCloser
	reader *bufio.Reader
	// ts: the stream is MPEG-TS, forwarded by whole packets so that a client
	// joining or a connection opened again never starts mid-packet.
	ts bool

	// ctx lives as long as the stream has clients; connCancel drops the
	// current connection to the provider only.
	ctx        context.Context
	cancel     context.CancelFunc
	connCancel context.CancelFunc

	// guarded by hub.mu
	subs  map[*Subscription]struct{}
	ended bool
}

// Streams is the number of provider streams currently open.
func (h *Hub) Streams() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.streams)
}

// Join attaches a client to the live stream named by key, opening it with
// open if nobody is watching it yet.
//
// It returns a subscription, or, when the provider's answer is not a live
// stream to share, that answer for this client alone (closing its body
// releases it).
func (h *Hub) Join(key string, open Source) (*Subscription, *http.Response, error) {
	for {
		h.mu.Lock()
		st, exists := h.streams[key]
		if !exists {
			st = &stream{hub: h, key: key, open: open, ready: make(chan struct{}), subs: map[*Subscription]struct{}{}}
			h.streams[key] = st
			h.mu.Unlock()
			return st.start()
		}
		h.mu.Unlock()

		<-st.ready
		if !st.shared {
			// Whoever opened it found something that is not shared (or
			// failed): this client asks for itself.
			return h.alone(open)
		}

		h.mu.Lock()
		sub := st.add()
		h.mu.Unlock()
		if sub != nil {
			return sub, nil, nil
		}
		// The stream ended in the meantime: start over.
	}
}

// alone opens the provider's answer for a single client.
func (h *Hub) alone(open Source) (*Subscription, *http.Response, error) {
	ctx, cancel := context.WithCancel(context.Background())
	resp, err := open(ctx)
	if err != nil {
		cancel()
		return nil, nil, err
	}
	resp.Body = &cancelOnClose{ReadCloser: resp.Body, cancel: cancel}
	return nil, resp, nil
}

type cancelOnClose struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (c *cancelOnClose) Close() error {
	err := c.ReadCloser.Close()
	c.cancel()
	return err
}

type peeked struct {
	io.Reader
	io.Closer
}

// start opens the provider's stream for the first client.
func (st *stream) start() (*Subscription, *http.Response, error) {
	h := st.hub
	// The stream outlives the request of the client that opened it.
	st.ctx, st.cancel = context.WithCancel(context.Background())

	giveUp := func() {
		h.mu.Lock()
		delete(h.streams, st.key)
		h.mu.Unlock()
		close(st.ready)
	}

	connCtx, connCancel := context.WithCancel(st.ctx)
	st.connCancel = connCancel
	resp, err := st.open(connCtx)
	if err != nil {
		st.cancel()
		giveUp()
		return nil, nil, err
	}

	reader := bufio.NewReaderSize(resp.Body, readBytes)
	begin, _ := reader.Peek(sniffBytes) // a short answer is judged on what it has

	if !h.shareable(resp, begin) {
		giveUp()
		resp.Body = &cancelOnClose{ReadCloser: peeked{reader, resp.Body}, cancel: st.cancel}
		return nil, resp, nil
	}

	st.header = resp.Header.Clone()
	st.body, st.reader = resp.Body, reader
	st.ts = isTS(begin)
	st.shared = true

	h.mu.Lock()
	sub := st.add()
	h.mu.Unlock()
	close(st.ready)

	go st.run()
	return sub, nil, nil
}

func (h *Hub) shareable(resp *http.Response, begin []byte) bool {
	if h.Shareable != nil {
		return h.Shareable(resp, begin)
	}
	return resp.StatusCode == http.StatusOK && resp.ContentLength < 0
}

func isTS(begin []byte) bool {
	return len(begin) > tsPacket && begin[0] == tsSyncByte && begin[tsPacket] == tsSyncByte
}

// add subscribes a client. hub.mu must be held. It returns nil when the
// stream has ended.
func (st *stream) add() *Subscription {
	if st.ended {
		return nil
	}
	ch := make(chan []byte, st.hub.Backlog)
	sub := &Subscription{Header: st.header.Clone(), C: ch, hub: st.hub, stream: st, ch: ch}
	st.subs[sub] = struct{}{}
	return sub
}

// remove unsubscribes a client, and closes the provider's stream with the
// last one. hub.mu must be held.
func (st *stream) remove(sub *Subscription) {
	if _, ok := st.subs[sub]; !ok {
		return
	}
	delete(st.subs, sub)
	close(sub.ch)
	if len(st.subs) == 0 {
		st.end()
	}
}

// end closes the stream for everyone. hub.mu must be held.
func (st *stream) end() {
	if st.ended {
		return
	}
	st.ended = true
	for sub := range st.subs {
		delete(st.subs, sub)
		close(sub.ch)
	}
	if st.hub.streams[st.key] == st {
		delete(st.hub.streams, st.key)
	}
	st.cancel() // releases the provider's connection
}

// broadcast hands a chunk to every client. It reports whether anyone is
// still watching.
func (st *stream) broadcast(chunk []byte) bool {
	st.hub.mu.Lock()
	defer st.hub.mu.Unlock()

	for sub := range st.subs {
		select {
		case sub.ch <- chunk:
		default:
			st.remove(sub) // too far behind
		}
	}
	return !st.ended
}

// run reads the provider's stream and feeds the clients until none is left,
// opening the stream again when the provider drops it.
func (st *stream) run() {
	defer func() {
		st.hub.mu.Lock()
		st.end()
		st.hub.mu.Unlock()
		_ = st.body.Close()
	}()

	var (
		buf     = make([]byte, readBytes)
		partial []byte // the beginning of a packet whose end is not read yet
		attempt int
		opened  = time.Now()
	)
	// A connection that sends nothing for too long is dropped, which makes
	// the read below fail like any other loss of the provider.
	silent := st.watch()
	defer func() { silent.Stop() }()
	for {
		n, err := st.reader.Read(buf)
		if n > 0 {
			silent.Reset(st.hub.Stall)
			chunk := make([]byte, 0, len(partial)+n)
			chunk = append(append(chunk, partial...), buf[:n]...)
			partial = nil
			if st.ts {
				whole := len(chunk) - len(chunk)%tsPacket
				partial = append(partial, chunk[whole:]...)
				chunk = chunk[:whole]
			}
			if len(chunk) > 0 && !st.broadcast(chunk) {
				return
			}
		}
		if err == nil {
			continue
		}

		// The provider dropped the stream (or it ended): while clients are
		// watching, it is opened again.
		_ = st.body.Close()
		partial = nil // the new connection starts on a packet
		if time.Since(opened) >= st.hub.Healthy {
			attempt = 0
		}
		silent.Stop()
		if !st.reopen(&attempt) {
			return
		}
		opened = time.Now()
		silent = st.watch()
	}
}

// watch returns the timer that drops the current connection once the
// provider has been silent for too long.
func (st *stream) watch() *time.Timer {
	if st.hub.Stall <= 0 {
		return time.NewTimer(0) // nothing to drop: Reset(0) and Stop are harmless
	}
	return time.AfterFunc(st.hub.Stall, st.connCancel)
}

// reopen opens the provider's stream again, waiting longer before each
// attempt. It reports false when the clients left or every attempt failed.
func (st *stream) reopen(attempt *int) bool {
	for *attempt < len(st.hub.Retries) {
		wait := time.NewTimer(st.hub.Retries[*attempt])
		*attempt++
		select {
		case <-wait.C:
		case <-st.ctx.Done():
			wait.Stop()
		}
		// Nobody is watching any more: the provider is not asked again.
		if st.ctx.Err() != nil {
			return false
		}

		st.connCancel() // releases what the previous connection held
		connCtx, connCancel := context.WithCancel(st.ctx)
		st.connCancel = connCancel
		resp, err := st.open(connCtx)
		if err != nil {
			continue
		}
		if resp.StatusCode != http.StatusOK {
			_ = resp.Body.Close()
			continue
		}
		st.body = resp.Body
		st.reader = bufio.NewReaderSize(resp.Body, readBytes)
		return true
	}
	return false
}
