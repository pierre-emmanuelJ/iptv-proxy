package hls

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type StreamEncodingRequest struct {
	url        *url.URL
	chunkSize  int64
	chunkNumer int64
	resolution int64
	cacheDir   string
	Data       chan *[]byte
	header     chan http.Header
	Err        chan error
}

func NewStreamEncodingRequest(url *url.URL, chunkSize, chunkNumber, resolution int64, cacheDir string) *StreamEncodingRequest {
	return &StreamEncodingRequest{
		url:        url,
		chunkSize:  chunkSize,
		chunkNumer: chunkNumber,
		resolution: resolution,
		cacheDir:   cacheDir,
		Data:       make(chan *[]byte),
		header:     make(chan http.Header, 1),
	}
}

func (s *StreamEncodingRequest) GetHeader() http.Header {
	if s.header != nil {
		return <-s.header
	}
	return nil
}

func (s *StreamEncodingRequest) NewEncoder() {
	go func() {
		for {
			resp, err := http.Get(s.url.String())
			if err != nil {
				s.Err <- err
				return
			}

			s.header <- resp.Header

			var curtime string
			var ptime, ctime float64
			for i := int64(0); i < s.chunkNumer; i++ {
				file := filepath.Join(s.cacheDir, s.url.Path, fmt.Sprintf("chunk_%d", i))
				os.MkdirAll(filepath.Join(s.cacheDir, s.url.Path), os.ModePerm)
				println(file)
				f, err := os.Create(file)
				if err != nil {
					s.Err <- err
					continue
				}
				io.CopyN(f, resp.Body, s.chunkSize)
				f.Close()

				println("file writed")

				//req := []string{`-i`, file}
				req2 := []string{`-i`, file}
				_ = req2
				_, time, err := execute(FFMPEGPath, req2)

				var test string
				for _, s := range strings.Split(string(time), "\n") {
					if strings.HasPrefix(strings.TrimSpace(s), "Duration:") {
						test = strings.TrimSpace(s)
					}
				}

				println(test)

				m := uint64(0)
				h := uint64(0)
				sec := ""
				fmt.Sscanf(test, "Duration: %d:%d:%s,", &h, &m, &sec)
				_ = h
				_ = m
				sec = strings.Trim(sec, ",")
				println(sec)

				curtime = sec
				ctime, _ = strconv.ParseFloat(curtime, 64)
				data, _, err := execute(FFMPEGPath, EncodingArgs(file, ptime, ctime, i, s.resolution))
				if err != nil {
					s.Err <- err
					continue
				}

				ptime = ctime + ptime

				println("file encoded")

				s.Data <- &data
			}

			resp.Body.Close()
		}
	}()
}

func EncodingArgs(videoFile string, prevtime, curtime float64, segment int64, res int64) []string {

	fmt.Printf("ptime %05.02f\n", prevtime)
	fmt.Printf("ctime %05.02f\n", curtime)
	// see http://superuser.com/questions/908280/what-is-the-correct-way-to-fix-keyframes-in-ffmpeg-for-dash
	return []string{

		// Prevent encoding to run longer than 30 seonds

		//"-timelimit", "45",

		// TODO: Some stuff to investigate
		// "-probesize", "524288",
		// "-fpsprobesize", "10",
		// "-analyzeduration", "2147483647",
		// "-hwaccel:0", "vda",

		// The start time
		// important: needs to be before -i to do input seeking
		"-ss", fmt.Sprintf("%05.02f", prevtime),

		// The source file
		"-i", videoFile,

		// Put all streams to output
		// "-map", "0",

		// The duration
		"-t", fmt.Sprintf("%05.02f", curtime),

		//"-c", "copy",

		// TODO: Find out what it does
		//"-strict", "-2",

		// 720p
		"-vf", fmt.Sprintf("scale=-2:%v", res),

		// x264 video codec
		"-vcodec", "libx264",

		// x264 preset
		"-preset", "veryfast",

		// aac audio codec
		"-acodec", "aac",
		//
		"-pix_fmt", "yuv420p",

		//"-r", "25", // fixed framerate

		"-force_key_frames", "expr:gte(t,n_forced*5.000)",

		//"-force_key_frames", "00:00:00.00",
		//"-x264opts", "keyint=25:min-keyint=25:scenecut=-1",

		//"-f", "mpegts",

		"-f", "ssegment",
		"-segment_time", fmt.Sprintf("%05.02f", curtime),
		"-initial_offset", fmt.Sprintf("%05.02f", prevtime),

		"pipe:out%03d.ts",
	}
}
