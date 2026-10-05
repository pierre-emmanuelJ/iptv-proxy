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
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/pierre-emmanuelJ/iptv-proxy/pkg/hls"
	"github.com/pierre-emmanuelJ/iptv-proxy/pkg/xtream"
)

// Media servers ask a tuner for MPEG-TS. When ffmpeg is at hand (the -ffmpeg
// images have it), the tuner also serves the channels a provider has only
// as HLS: ffmpeg reads the playlist and its segments and writes them as one
// MPEG-TS stream, without encoding them again. Without ffmpeg, such a
// channel is passed on as a playlist, which media servers do not play.

// ffmpegStart is how long ffmpeg may take to give its first bytes.
var ffmpegStart = 20 * time.Second

// tsBytes is how much MPEG-TS ffmpeg must give for a stream to have
// started: two packets.
const tsBytes = 2 * 188

// setupFFmpeg finds the ffmpeg to use. The default, "ffmpeg", is used when
// it is found; another one must be.
func (c *Config) setupFFmpeg() error {
	if c.FFmpeg == "" || c.FFmpeg == "none" {
		return nil
	}
	path, err := exec.LookPath(c.FFmpeg)
	switch {
	case err == nil:
		c.ffmpeg = path
	case c.FFmpeg != "ffmpeg":
		return fmt.Errorf("--ffmpeg: %w", err)
	}
	return nil
}

// remuxHLS turns an HLS playlist the provider answered into MPEG-TS, read
// by ffmpeg. Any other answer is passed on as it is. On an error, the
// provider's answer is closed.
func (c *Config) remuxHLS(ctx context.Context, resp *http.Response, header http.Header) (*http.Response, error) {
	body := bufio.NewReader(resp.Body)
	begin, _ := body.Peek(64)
	if c.ffmpeg == "" || !hls.IsPlaylist(begin) {
		resp.Body = peeked{body, resp.Body}
		return resp, nil
	}
	stream, err := c.ffmpegTS(ctx, resp.Request.URL.String(), header.Get("User-Agent"))
	if err != nil {
		_ = resp.Body.Close()
		return nil, err
	}
	return &http.Response{
		StatusCode:    http.StatusOK,
		Header:        http.Header{"Content-Type": {"video/mp2t"}},
		ContentLength: -1,
		// the playlist's answer is closed with the stream: a source keeps
		// counting it as open
		Body:    &both{ReadCloser: stream, also: resp.Body},
		Request: resp.Request,
	}, nil
}

type peeked struct {
	io.Reader
	io.Closer
}

type both struct {
	io.ReadCloser
	also io.Closer
}

func (b *both) Close() error {
	err := b.ReadCloser.Close()
	_ = b.also.Close()
	return err
}

// ffmpegTS starts ffmpeg on an HLS address and returns its MPEG-TS once it
// gave some. Closing it stops ffmpeg.
func (c *Config) ffmpegTS(ctx context.Context, address, userAgent string) (io.ReadCloser, error) {
	ctx, cancel := context.WithCancel(ctx)
	args := []string{"-hide_banner", "-nostdin", "-loglevel", "error"}
	if userAgent != "" {
		args = append(args, "-user_agent", userAgent)
	}
	args = append(args, "-i", address, "-map", "0:v?", "-map", "0:a?", "-c", "copy", "-f", "mpegts", "pipe:1")
	// the operator's ffmpeg, with no shell, on an address the proxy just
	// fetched over http(s): it cannot pass for an option
	cmd := exec.CommandContext(ctx, c.ffmpeg, args...) // nolint: gosec
	stderr := &lastLines{}
	cmd.Stderr = stderr
	stdout, err := cmd.StdoutPipe()
	if err == nil {
		err = cmd.Start()
	}
	if err != nil {
		cancel()
		return nil, fmt.Errorf("ffmpeg: %w", err)
	}
	s := &ffmpegStream{cmd: cmd, cancel: cancel, stderr: stderr, scrub: c.scrubFFmpeg}

	first := make([]byte, 64<<10)
	type read struct {
		n   int
		err error
	}
	started := make(chan read, 1)
	go func() {
		n, err := io.ReadAtLeast(stdout, first, tsBytes)
		started <- read{n, err}
	}()
	timer := time.NewTimer(ffmpegStart)
	defer timer.Stop()
	select {
	case r := <-started:
		if r.err != nil {
			s.stop()
			return nil, fmt.Errorf("ffmpeg gave no stream: %s", s.why())
		}
		s.Reader = io.MultiReader(bytes.NewReader(first[:r.n]), stdout)
		return s, nil
	case <-timer.C:
		s.stop()
		return nil, fmt.Errorf("ffmpeg gave no stream within %s", ffmpegStart)
	}
}

// ffmpegStream is the MPEG-TS ffmpeg writes.
type ffmpegStream struct {
	io.Reader
	cmd    *exec.Cmd
	cancel context.CancelFunc
	stderr *lastLines
	scrub  func(string) string
	once   sync.Once
}

func (s *ffmpegStream) Read(p []byte) (int, error) {
	n, err := s.Reader.Read(p)
	if errors.Is(err, io.EOF) {
		// ffmpeg ended by itself: the provider ended the stream or failed
		s.stop()
		if why := s.why(); why != "" {
			log.Printf("[iptv-proxy] ffmpeg: %s", why)
		}
	}
	return n, err
}

func (s *ffmpegStream) Close() error {
	s.stop()
	return nil
}

// stop ends ffmpeg and waits for it.
func (s *ffmpegStream) stop() {
	s.once.Do(func() {
		s.cancel()
		_ = s.cmd.Wait()
	})
}

// why is what ffmpeg said, without addresses.
func (s *ffmpegStream) why() string {
	return s.scrub(s.stderr.String())
}

// ffmpegAddresses are what ffmpeg's messages may hold of a provider: its addresses.
var ffmpegAddresses = regexp.MustCompile(`[A-Za-z][A-Za-z0-9+.-]*://[^\s'"]+`)

// scrubFFmpeg removes from ffmpeg's messages the addresses they name, and
// whatever they still hold of the provider's accounts.
func (c *Config) scrubFFmpeg(text string) string {
	text = ffmpegAddresses.ReplaceAllString(strings.TrimSpace(text), "<address>")
	for _, hidden := range c.hiddenAccounts() {
		text = string(xtream.Scrub([]byte(text), hidden, xtream.Account{User: "***", Password: "***"}))
	}
	return strings.Join(strings.Fields(text), " ")
}

// lastLines keeps the end of what a process writes.
type lastLines struct {
	mu  sync.Mutex
	buf []byte
}

const keptBytes = 2048

func (l *lastLines) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.buf = append(l.buf, p...)
	if len(l.buf) > keptBytes {
		l.buf = l.buf[len(l.buf)-keptBytes:]
	}
	return len(p), nil
}

func (l *lastLines) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return string(l.buf)
}
