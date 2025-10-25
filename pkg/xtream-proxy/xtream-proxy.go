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

package xtreamproxy

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
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
func New(user, password, baseURL, userAgent string) (*Client, error) {
	cli, err := xtream.NewClientWithUserAgent(context.Background(), user, password, baseURL, userAgent)
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
			Port:         xtream.FlexInt(proxyPort),
			HTTPSPort:    xtream.FlexInt(proxyPort),
			Protocol:     protocol,
			RTMPPort:     xtream.FlexInt(proxyPort),
			Timezone:     c.ServerInfo.Timezone,
			TimestampNow: c.ServerInfo.TimestampNow,
			TimeNow:      c.ServerInfo.TimeNow,
		},
	}

	return req, nil
}

// Action execute an xtream action.
func (c *Client) Action(config *config.ProxyConfig, action string, q url.Values) (respBody interface{}, httpcode int, err error) {
	log.Printf("[xtream-proxy] Action called: '%s' with params: %v", action, q)
	protocol := "http"
	if config.HTTPS {
		protocol = "https"
	}

	switch action {
	case getLiveCategories:
		respBody, err = c.GetLiveCategories()
	case getLiveStreams:
		categoryID := ""
		if len(q["category_id"]) > 0 {
			categoryID = q["category_id"][0]
		}
		respBody, err = c.GetLiveStreams(categoryID)
	case getVodCategories:
		respBody, err = c.GetVideoOnDemandCategories()
	case getVodStreams:
		categoryID := ""
		if len(q["category_id"]) > 0 {
			categoryID = q["category_id"][0]
		}
		respBody, err = c.GetVideoOnDemandStreams(categoryID)
	case getVodInfo:
		httpcode, err = validateParams(q, "vod_id")
		if err != nil {
			return
		}
		respBody, err = c.GetVideoOnDemandInfo(q["vod_id"][0])
	case getSeriesCategories:
		log.Printf("[xtream-proxy] Getting series categories...")
		respBody, err = c.GetSeriesCategories()
		if err == nil {
			if categories, ok := respBody.([]xtream.Category); ok {
				log.Printf("[xtream-proxy] Found %d series categories", len(categories))
			}
		}
	case getSeries:
		categoryID := ""
		if len(q["category_id"]) > 0 {
			categoryID = q["category_id"][0]
		}
		log.Printf("[xtream-proxy] Getting series for category: '%s'", categoryID)
		
		// If no category_id is provided, get series from all categories
		if categoryID == "" {
			log.Printf("[xtream-proxy] No category specified, trying to get all series using raw HTTP call...")
			
			// Try to get all series using raw HTTP call to bypass parsing issues
			originalURL := fmt.Sprintf("%s/player_api.php?username=%s&password=%s&action=get_series", 
				c.XtreamClient.BaseURL, c.XtreamClient.Username, c.XtreamClient.Password)
			
			resp, err := http.Get(originalURL)
			if err != nil {
				log.Printf("[xtream-proxy] Error calling original server: %v", err)
			} else {
				defer resp.Body.Close()
				
				if resp.StatusCode == http.StatusOK {
					// Read raw response
					body, err := ioutil.ReadAll(resp.Body)
					if err != nil {
						log.Printf("[xtream-proxy] Error reading response body: %v", err)
					} else {
						// Try to parse as raw JSON with more tolerance
						var rawSeries []map[string]interface{}
						err = json.Unmarshal(body, &rawSeries)
						if err != nil {
							log.Printf("[xtream-proxy] Error parsing raw JSON: %v", err)
						} else {
							log.Printf("[xtream-proxy] Successfully parsed %d series from raw response", len(rawSeries))
							
							// Convert raw data to SeriesInfo structs with error tolerance
							var convertedSeries []xtream.SeriesInfo
							for _, rawSerie := range rawSeries {
								serie := xtream.SeriesInfo{}
								
								// Safely extract fields with fallbacks
								if name, ok := rawSerie["name"].(string); ok {
									serie.Name = name
								}
								if cover, ok := rawSerie["cover"].(string); ok {
									serie.Cover = cover
								}
								if seriesID, ok := rawSerie["series_id"]; ok {
									switch v := seriesID.(type) {
									case float64:
										serie.SeriesID = xtream.FlexInt(int(v))
									case string:
										if v != "" {
											if id, err := strconv.Atoi(v); err == nil {
												serie.SeriesID = xtream.FlexInt(id)
											}
										}
									}
								}
								
								// Extract category_id to preserve category information
								if categoryID, ok := rawSerie["category_id"]; ok {
									switch v := categoryID.(type) {
									case float64:
										flexInt := xtream.FlexInt(int(v))
										serie.CategoryID = &flexInt
									case string:
										if v != "" {
											if id, err := strconv.Atoi(v); err == nil {
												flexInt := xtream.FlexInt(id)
												serie.CategoryID = &flexInt
											}
										}
									}
								}
								
								// Extract other important fields
								if plot, ok := rawSerie["plot"].(string); ok {
									serie.Plot = plot
								}
								if cast, ok := rawSerie["cast"].(string); ok {
									serie.Cast = cast
								}
								if director, ok := rawSerie["director"].(string); ok {
									serie.Director = director
								}
								if genre, ok := rawSerie["genre"].(string); ok {
									serie.Genre = genre
								}
								if releaseDate, ok := rawSerie["releaseDate"].(string); ok {
									serie.ReleaseDate = releaseDate
								}
								if rating, ok := rawSerie["rating"]; ok {
									if ratingStr, ok := rating.(string); ok {
										if ratingInt, err := strconv.Atoi(ratingStr); err == nil {
											serie.Rating = xtream.FlexInt(ratingInt)
										}
									} else if ratingFloat, ok := rating.(float64); ok {
										serie.Rating = xtream.FlexInt(int(ratingFloat))
									}
								}
								
								convertedSeries = append(convertedSeries, serie)
							}
							
							log.Printf("[xtream-proxy] Successfully converted %d series", len(convertedSeries))
							respBody = convertedSeries
							return respBody, 0, nil
						}
					}
				} else {
					log.Printf("[xtream-proxy] Original server returned status: %d", resp.StatusCode)
				}
			}
			
			// Fallback to our category-by-category approach if original server fails
			log.Printf("[xtream-proxy] Original server approach failed, falling back to category-by-category...")
			categories, err := c.GetSeriesCategories()
			if err != nil {
				log.Printf("[xtream-proxy] Error getting series categories: %v", err)
				return nil, http.StatusInternalServerError, err
			}
			
			var allSeries []xtream.SeriesInfo
			successCount := 0
			errorCount := 0
			
			for _, category := range categories {
				categorySeries, err := c.GetSeries(fmt.Sprint(category.ID))
				if err != nil {
					errorCount++
					log.Printf("[xtream-proxy] Error getting series for category %d (%s): %v", category.ID, category.Name, err)
					// Continue with next category instead of failing completely
					continue
				}
				if len(categorySeries) > 0 {
					allSeries = append(allSeries, categorySeries...)
					successCount++
					log.Printf("[xtream-proxy] Added %d series from category: %s", len(categorySeries), category.Name)
				} else {
					log.Printf("[xtream-proxy] No series found in category: %s", category.Name)
				}
			}
			log.Printf("[xtream-proxy] Series loading complete: %d categories successful, %d failed, %d total series", successCount, errorCount, len(allSeries))
			respBody = allSeries
		} else {
			// Category specified, try to get series for that specific category
			log.Printf("[xtream-proxy] Getting series for specific category: %s", categoryID)
			respBody, err = c.GetSeries(categoryID)
			if err != nil {
				log.Printf("[xtream-proxy] Error getting series for category %s: %v", categoryID, err)
				// If specific category fails, try to filter from all series
				log.Printf("[xtream-proxy] Trying to filter from all series...")
				
				allSeriesURL := fmt.Sprintf("%s/player_api.php?username=%s&password=%s&action=get_series", 
					c.XtreamClient.BaseURL, c.XtreamClient.Username, c.XtreamClient.Password)
				
				resp, err := http.Get(allSeriesURL)
				if err == nil {
					defer resp.Body.Close()
					if resp.StatusCode == http.StatusOK {
						body, err := ioutil.ReadAll(resp.Body)
						if err == nil {
							var rawSeries []map[string]interface{}
							err = json.Unmarshal(body, &rawSeries)
							if err == nil {
								var filteredSeries []xtream.SeriesInfo
								for _, rawSerie := range rawSeries {
									if catID, ok := rawSerie["category_id"]; ok {
										catIDStr := fmt.Sprintf("%v", catID)
										if catIDStr == categoryID {
											serie := xtream.SeriesInfo{}
											if name, ok := rawSerie["name"].(string); ok {
												serie.Name = name
											}
											if cover, ok := rawSerie["cover"].(string); ok {
												serie.Cover = cover
											}
											if seriesID, ok := rawSerie["series_id"]; ok {
												switch v := seriesID.(type) {
												case float64:
													serie.SeriesID = xtream.FlexInt(int(v))
												case string:
													if v != "" {
														if id, err := strconv.Atoi(v); err == nil {
															serie.SeriesID = xtream.FlexInt(id)
														}
													}
												}
											}
											filteredSeries = append(filteredSeries, serie)
										}
									}
								}
								log.Printf("[xtream-proxy] Filtered %d series for category %s", len(filteredSeries), categoryID)
								respBody = filteredSeries
								err = nil
							}
						}
					}
				}
			} else {
				if series, ok := respBody.([]xtream.SeriesInfo); ok {
					log.Printf("[xtream-proxy] Found %d series in category %s", len(series), categoryID)
				}
			}
		}
	case getSerieInfo:
		httpcode, err = validateParams(q, "series_id")
		if err != nil {
			return
		}
		respBody, err = c.GetSeriesInfo(q["series_id"][0])
	case getShortEPG:
		limit := 0

		httpcode, err = validateParams(q, "stream_id")
		if err != nil {
			return
		}
		if len(q["limit"]) > 0 && q["limit"][0] != "" {
			limit, err = strconv.Atoi(q["limit"][0])
			if err != nil {
				log.Printf("[xtream-proxy] Error parsing limit '%s': %v", q["limit"][0], err)
				httpcode = http.StatusInternalServerError
				return
			}
		}
		respBody, err = c.GetShortEPG(q["stream_id"][0], limit)
	case getSimpleDataTable:
		httpcode, err = validateParams(q, "stream_id")
		if err != nil {
			return
		}
		respBody, err = c.GetEPG(q["stream_id"][0])
	default:
		respBody, err = c.login(config.User.String(), config.Password.String(), protocol+"://"+config.HostConfig.Hostname, config.AdvertisedPort, protocol)
	}

	return
}

func validateParams(u url.Values, params ...string) (int, error) {
	for _, p := range params {
		if len(u[p]) < 1 {
			return http.StatusBadRequest, fmt.Errorf("missing %q", p)
		}

	}

	return 0, nil
}
