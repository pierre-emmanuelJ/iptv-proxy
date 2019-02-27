package m3u

import (
	"fmt"
	"net/url"

	"github.com/jamesnetherton/m3u"
	"github.com/pierre-emmanuelJ/iptv-proxy/pkg/config"
)

// Marshall m3u.playlist struct to m3u file
func Marshall(p *m3u.Playlist) (string, error) {
	result := "#EXTM3U\n"
	for _, track := range p.Tracks {
		result += "#EXTINF:"
		result += fmt.Sprintf("%d ", track.Length)
		for i := range track.Tags {
			if i == len(track.Tags)-1 {
				result += fmt.Sprintf("%s=%q,", track.Tags[i].Name, track.Tags[i].Value)
				continue
			}
			result += fmt.Sprintf("%s=%q ", track.Tags[i].Name, track.Tags[i].Value)
		}

		result += fmt.Sprintf("%s\n%s\n", track.Name, track.URI)
	}
	return result, nil
}

// ReplaceURL replace original playlist url by proxy url
func ReplaceURL(playlist *m3u.Playlist, config *config.HostConfiguration) (*m3u.Playlist, error) {
	result := make([]m3u.Track, 0, len(playlist.Tracks))
	for _, track := range playlist.Tracks {
		oriURL, err := url.Parse(track.URI)
		if err != nil {
			return nil, err
		}

		destURL, err := url.Parse(fmt.Sprintf("http://%s:%d%s", config.Hostname, config.Port, oriURL.RequestURI()))
		if err != nil {
			return nil, err
		}

		track.URI = destURL.String()
		result = append(result, track)
	}

	return &m3u.Playlist{
		Tracks: result,
	}, nil
}
