package xtreamproxy

import (
	xtream "github.com/tellytv/go.xtream-codes"
)

const (
	GetLiveCategories   = "get_live_categories"
	GetLiveStreams      = "get_live_streams"
	GetVodCategories    = "get_vod_categories"
	GetVodStreams       = "get_vod_streams"
	GetVodInfo          = "get_vod_info"
	GetSeriesCategories = "get_series_categories"
	GetSeries           = "get_series"
	GetSerieInfo        = "get_series_info"
	GetShortEPG         = "get_short_epg"
)

type Client struct {
	*xtream.XtreamClient
}

func New(user, password, baseURL string) (*Client, error) {
	cli, err := xtream.NewClient(user, password, baseURL)
	if err != nil {
		return nil, err
	}

	return &Client{cli}, nil
}

type Login struct {
	UserInfo   xtream.UserInfo   `json:"user_info"`
	ServerInfo xtream.ServerInfo `json:"server_info"`
}

func (c *Client) Login(proxyUser, proxyPassword, proxyURL string, proxyPort int, protocol string) (Login, error) {
	req := Login{
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
