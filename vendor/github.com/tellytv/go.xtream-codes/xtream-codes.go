// Package xtreamcodes provides a Golang interface to the Xtream-Codes IPTV Server API.
package xtreamcodes

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
)

var defaultUserAgent = "go.xstream-codes (Go-http-client/1.1)"

// XtreamClient is the client used to communicate with a Xtream-Codes server.
type XtreamClient struct {
	Username  string
	Password  string
	BaseURL   string
	UserAgent string

	ServerInfo ServerInfo
	UserInfo   UserInfo

	// Our HTTP client to communicate with Xtream
	HTTP    *http.Client
	Context context.Context

	// We store an internal map of Streams for use with GetStreamURL
	streams map[int]Stream
}

// NewClient returns an initialized XtreamClient with the given values.
func NewClient(username, password, baseURL string) (*XtreamClient, error) {

	_, parseURLErr := url.Parse(baseURL)
	if parseURLErr != nil {
		return nil, fmt.Errorf("error parsing url: %s", parseURLErr.Error())
	}

	client := &XtreamClient{
		Username:  username,
		Password:  password,
		BaseURL:   baseURL,
		UserAgent: defaultUserAgent,

		HTTP:    http.DefaultClient,
		Context: context.Background(),

		streams: make(map[int]Stream),
	}

	authData, authErr := client.sendRequest("", nil)
	if authErr != nil {
		return nil, fmt.Errorf("error sending authentication request: %s", authErr.Error())
	}

	a := &AuthenticationResponse{}

	if jsonErr := json.Unmarshal(authData, &a); jsonErr != nil {
		return nil, fmt.Errorf("error unmarshaling json: %s", jsonErr.Error())
	}

	client.ServerInfo = a.ServerInfo
	client.UserInfo = a.UserInfo

	return client, nil
}

// NewClientWithContext returns an initialized XtreamClient with the given values.
func NewClientWithContext(ctx context.Context, username, password, baseURL string) (*XtreamClient, error) {
	c, err := NewClient(username, password, baseURL)
	if err != nil {
		return nil, err
	}
	c.Context = ctx

	return c, nil
}

// NewClientWithUserAgent returns an initialized XtreamClient with the given values.
func NewClientWithUserAgent(ctx context.Context, username, password, baseURL, userAgent string) (*XtreamClient, error) {
	c, err := NewClient(username, password, baseURL)
	if err != nil {
		return nil, err
	}
	c.UserAgent = userAgent
	c.Context = ctx

	return c, nil
}

// GetStreamURL will return a stream URL string for the given streamID and wantedFormat.
func (c *XtreamClient) GetStreamURL(streamID int, wantedFormat string) (string, error) {

	// For Live Streams the main format is
	// http(s)://domain:port/live/username/password/streamID.ext ( In allowed_output_formats element you have the available ext )
	// For VOD Streams the format is:
	// http(s)://domain:port/movie/username/password/streamID.ext ( In target_container element you have the available ext )
	// For Series Streams the format is
	// http(s)://domain:port/series/username/password/streamID.ext ( In target_container element you have the available ext )

	validFormat := false

	for _, allowedFormat := range c.UserInfo.AllowedOutputFormats {
		if wantedFormat == allowedFormat {
			validFormat = true
		}
	}

	if !validFormat {
		return "", fmt.Errorf("%s is not an allowed output format", wantedFormat)
	}

	if _, ok := c.streams[streamID]; !ok {
		return "", fmt.Errorf("%d is not a valid stream id", streamID)
	}

	stream := c.streams[streamID]

	return fmt.Sprintf("%s/%s/%s/%s/%d.%s", c.BaseURL, stream.Type, c.Username, c.Password, stream.ID, wantedFormat), nil
}

// GetLiveCategories will return a slice of categories for live streams.
func (c *XtreamClient) GetLiveCategories() ([]Category, error) {
	return c.GetCategories("live")
}

// GetVideoOnDemandCategories will return a slice of categories for VOD streams.
func (c *XtreamClient) GetVideoOnDemandCategories() ([]Category, error) {
	return c.GetCategories("vod")
}

// GetSeriesCategories will return a slice of categories for series streams.
func (c *XtreamClient) GetSeriesCategories() ([]Category, error) {
	return c.GetCategories("series")
}

// GetCategories is a helper function used by GetLiveCategories, GetVideoOnDemandCategories and
// GetSeriesCategories to reduce duplicate code.
func (c *XtreamClient) GetCategories(catType string) ([]Category, error) {
	catData, catErr := c.sendRequest(fmt.Sprintf("get_%s_categories", catType), nil)
	if catErr != nil {
		return nil, catErr
	}

	cats := make([]Category, 0)

	jsonErr := json.Unmarshal(catData, &cats)

	for idx := range cats {
		cats[idx].Type = catType
	}

	return cats, jsonErr
}

// GetLiveStreams will return a slice of live streams.
// You can also optionally provide a categoryID to limit the output to members of that category.
func (c *XtreamClient) GetLiveStreams(categoryID string) ([]Stream, error) {
	return c.GetStreams("live", categoryID)
}

// GetVideoOnDemandStreams will return a slice of VOD streams.
// You can also optionally provide a categoryID to limit the output to members of that category.
func (c *XtreamClient) GetVideoOnDemandStreams(categoryID string) ([]Stream, error) {
	return c.GetStreams("vod", categoryID)
}

// GetStreams is a helper function used by GetLiveStreams and GetVideoOnDemandStreams
// to reduce duplicate code.
func (c *XtreamClient) GetStreams(streamAction, categoryID string) ([]Stream, error) {
	var params url.Values
	if categoryID != "" {
		params = url.Values{}
		params.Add("category_id", categoryID)
	}

	// For whatever reason, unlike live and vod, series streams action doesn't have "_streams".
	if streamAction != "series" {
		streamAction = fmt.Sprintf("%s_streams", streamAction)
	}

	streamData, streamErr := c.sendRequest(fmt.Sprintf("get_%s", streamAction), params)
	if streamErr != nil {
		return nil, streamErr
	}

	streams := make([]Stream, 0)

	if jsonErr := json.Unmarshal(streamData, &streams); jsonErr != nil {
		return nil, jsonErr
	}

	for _, stream := range streams {
		c.streams[int(stream.ID)] = stream
	}

	return streams, nil
}

// GetSeries will return a slice of all available Series.
// You can also optionally provide a categoryID to limit the output to members of that category.
func (c *XtreamClient) GetSeries(categoryID string) ([]SeriesInfo, error) {
	var params url.Values
	if categoryID != "" {
		params = url.Values{}
		params.Add("category_id", categoryID)
	}

	seriesData, seriesErr := c.sendRequest("get_series", params)
	if seriesErr != nil {
		return nil, seriesErr
	}

	seriesInfos := make([]SeriesInfo, 0)

	if jsonErr := json.Unmarshal(seriesData, &seriesInfos); jsonErr != nil {
		return nil, jsonErr
	}

	return seriesInfos, nil
}

// GetSeriesInfo will return a series info for the given seriesID.
func (c *XtreamClient) GetSeriesInfo(seriesID string) (*Series, error) {
	if seriesID == "" {
		return nil, fmt.Errorf("series ID can not be empty")
	}

	seriesData, seriesErr := c.sendRequest("get_series_info", url.Values{"series_id": []string{seriesID}})
	if seriesErr != nil {
		return nil, seriesErr
	}

	seriesInfo := &Series{}

	jsonErr := json.Unmarshal(seriesData, &seriesInfo)

	return seriesInfo, jsonErr
}

// GetVideoOnDemandInfo will return VOD info for the given vodID.
func (c *XtreamClient) GetVideoOnDemandInfo(vodID string) (*VideoOnDemandInfo, error) {
	if vodID == "" {
		return nil, fmt.Errorf("vod ID can not be empty")
	}

	vodData, vodErr := c.sendRequest("get_vod_info", url.Values{"vod_id": []string{vodID}})
	if vodErr != nil {
		return nil, vodErr
	}

	vodInfo := &VideoOnDemandInfo{}

	jsonErr := json.Unmarshal(vodData, &vodInfo)

	return vodInfo, jsonErr
}

// GetShortEPG returns a short version of the EPG for the given streamID. If no limit is provided, the next 4 items in the EPG will be returned.
func (c *XtreamClient) GetShortEPG(streamID string, limit int) ([]EPGInfo, error) {
	return c.getEPG("get_short_epg", streamID, limit)
}

// GetEPG returns the full EPG for the given streamID.
func (c *XtreamClient) GetEPG(streamID string) ([]EPGInfo, error) {
	return c.getEPG("get_simple_data_table", streamID, 0)
}

// GetXMLTV will return a slice of bytes for the XMLTV EPG file available from the provider.
func (c *XtreamClient) GetXMLTV() ([]byte, error) {
	xmlTVData, xmlTVErr := c.sendRequest("xmltv.php", nil)
	if xmlTVErr != nil {
		return nil, xmlTVErr
	}

	return xmlTVData, xmlTVErr
}

func (c *XtreamClient) getEPG(action, streamID string, limit int) ([]EPGInfo, error) {
	if streamID == "" {
		return nil, fmt.Errorf("stream ID can not be empty")
	}

	params := url.Values{"stream_id": []string{streamID}}
	if limit > 0 {
		params.Add("limit", strconv.Itoa(limit))
	}

	epgData, epgErr := c.sendRequest(action, params)
	if epgErr != nil {
		return nil, epgErr
	}

	epgContainer := &epgContainer{}

	jsonErr := json.Unmarshal(epgData, &epgContainer)

	return epgContainer.EPGListings, jsonErr
}

func (c *XtreamClient) sendRequest(action string, parameters url.Values) ([]byte, error) {
	file := "player_api.php"
	if action == "xmltv.php" {
		file = action
	}
	url := fmt.Sprintf("%s/%s?username=%s&password=%s", c.BaseURL, file, c.Username, c.Password)
	if action != "" {
		url = fmt.Sprintf("%s&action=%s", url, action)
	}
	if parameters != nil {
		url = fmt.Sprintf("%s&%s", url, parameters.Encode())
	}

	request, httpErr := http.NewRequest("GET", url, nil)
	if httpErr != nil {
		return nil, httpErr
	}

	request.Header.Set("User-Agent", c.UserAgent)

	request = request.WithContext(c.Context)

	response, httpErr := c.HTTP.Do(request)
	if httpErr != nil {
		return nil, fmt.Errorf("cannot reach server. %v", httpErr)
	}

	if response.StatusCode > 399 {
		return nil, fmt.Errorf("status code was %d, expected 2XX-3XX", response.StatusCode)
	}

	buf := &bytes.Buffer{}
	if _, copyErr := io.Copy(buf, response.Body); copyErr != nil {
		return nil, copyErr
	}

	if closeErr := response.Body.Close(); closeErr != nil {
		return nil, fmt.Errorf("cannot read response. %v", closeErr)
	}

	return buf.Bytes(), nil
}
