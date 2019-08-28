package hls

import (
	"os"
	"path/filepath"
)

var HomeDir = ".gohls"
var FFProbePath = "ffprobe"
var FFMPEGPath = "ffmpeg"

const cacheDirName = "cache"
const hlsSegmentLength = 5.0 // Seconds

func ClearCache() error {
	var cacheDir = filepath.Join(HomeDir, cacheDirName)
	return os.RemoveAll(cacheDir)
}
