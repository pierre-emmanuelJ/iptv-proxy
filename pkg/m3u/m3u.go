package m3u

import (
	"fmt"
	"net/url"

	"github.com/jamesnetherton/m3u"
	"github.com/pierre-emmanuelJ/iptv-proxy/pkg/config"
)

// Marshall m3u.playlist struct to m3u file
func Marshall(p *m3u.Playlist, config *config.HostConfiguration) (string, error) {
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
		result += fmt.Sprintf("%s\n", track.Name)

		oriURL, err := url.Parse(track.URI)
		if err != nil {
			return "", err
		}

		destURL, err := url.Parse(fmt.Sprintf("http://%s:%d%s", config.Hostname, config.Port, oriURL.RequestURI()))
		if err != nil {
			return "", err
		}

		result += fmt.Sprintf("%s\n", destURL.String())
	}
	return result, nil
}
