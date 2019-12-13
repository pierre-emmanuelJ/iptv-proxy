package xtreamproxy

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"github.com/pierre-emmanuelJ/iptv-proxy/pkg/config"
	xtream "github.com/tellytv/go.xtream-codes"
)

const (
	getLiveCategories   = "get_live_categories"
	getLiveStreams      = "get_live_streams"
	getVodCategories    = "get_vod_categories"
	getVodStreams       = "get_vod_streams"
	getVodInfo          = "get_vod_info"
	getSeriesCategories = "get_series_categories"
	getSeries           = "get_series"
	getSerieInfo        = "get_series_info"
	getShortEPG         = "get_short_epg"
	getSimpleDataTable  = "get_simple_data_table"
)

// Client represent an xtream client
type Client struct {
	*xtream.XtreamClient
}

// New new xtream client
func New(user, password, baseURL string) (*Client, error) {
	cli, err := xtream.NewClient(user, password, baseURL)
	if err != nil {
		return nil, err
	}

	return &Client{cli}, nil
}

type login struct {
	UserInfo   xtream.UserInfo   `json:"user_info"`
	ServerInfo xtream.ServerInfo `json:"server_info"`
}

// Login xtream login
func (c *Client) login(proxyUser, proxyPassword, proxyURL string, proxyPort int, protocol string) (login, error) {
	req := login{
		UserInfo: xtream.UserInfo{
			Username:             proxyUser,
			Password:             proxyPassword,
			Message:              c.UserInfo.Message,
			Auth:                 c.UserInfo.Auth,
			Status:               c.UserInfo.Status,
			ExpDate:              c.UserInfo.ExpDate,
			IsTrial:              c.UserInfo.IsTrial,
			ActiveConnections:    c.UserInfo.ActiveConnections,
			CreatedAt:            c.UserInfo.CreatedAt,
			MaxConnections:       c.UserInfo.MaxConnections,
			AllowedOutputFormats: c.UserInfo.AllowedOutputFormats,
		},
		ServerInfo: xtream.ServerInfo{
			URL:          proxyURL,
			Port:         proxyPort,
			HTTPSPort:    proxyPort,
			Protocol:     protocol,
			RTMPPort:     proxyPort,
			Timezone:     c.ServerInfo.Timezone,
			TimestampNow: c.ServerInfo.TimestampNow,
			TimeNow:      c.ServerInfo.TimeNow,
		},
	}

	return req, nil
}

// Action execute an xtream action.
func (c *Client) Action(config *config.ProxyConfig, action string, q url.Values) (respBody interface{}, httpcode int, err error) {
	protocol := "http"
	if config.HTTPS {
		protocol = "https"
	}

	switch action {
	case getLiveCategories:
		respBody, err = c.GetLiveCategories()
	case getLiveStreams:
		respBody, err = c.GetLiveStreams("")
	case getVodCategories:
		respBody, err = c.GetVideoOnDemandCategories()
	case getVodStreams:
		respBody, err = c.GetVideoOnDemandStreams("")
	case getVodInfo:
		if len(q["vod_id"]) < 1 {
			err = fmt.Errorf(`bad body url query parameters: missing "vod_id"`)
			httpcode = http.StatusBadRequest
			return
		}
		respBody, err = c.GetVideoOnDemandInfo(q["vod_id"][0])
	case getSeriesCategories:
		respBody, err = c.GetSeriesCategories()
	case getSeries:
		respBody, err = c.GetSeries("")
	case getSerieInfo:
		if len(q["series_id"]) < 1 {
			err = fmt.Errorf(`bad body url query parameters: missing "series_id"`)
			httpcode = http.StatusBadRequest
			return
		}
		respBody, err = c.GetSeriesInfo(q["series_id"][0])
	case getShortEPG:
		if len(q["stream_id"]) < 1 {
			err = fmt.Errorf(`bad body url query parameters: missing "stream_id"`)
			httpcode = http.StatusBadRequest
			return
		}
		limit := 0
		if len(q["limit"]) > 0 {
			limit, err = strconv.Atoi(q["limit"][0])
			if err != nil {
				httpcode = http.StatusInternalServerError
				return
			}
		}
		respBody, err = c.GetShortEPG(q["stream_id"][0], limit)
	case getSimpleDataTable:
		if len(q["stream_id"]) < 1 {
			err = fmt.Errorf(`bad body url query parameters: missing "stream_id"`)
			httpcode = http.StatusBadRequest
			return
		}
		respBody, err = c.GetEPG(q["stream_id"][0])
	default:
		respBody, err = c.login(config.User, config.Password, protocol+"://"+config.HostConfig.Hostname, int(config.HostConfig.Port), protocol)
	}

	return
}
