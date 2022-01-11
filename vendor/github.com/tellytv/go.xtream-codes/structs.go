package xtreamcodes

// TODO: Add more flex types on IDs if needed
// for future potential provider issues.

// ServerInfo describes the state of the Xtream-Codes server.
type ServerInfo struct {
	HTTPSPort    FlexInt   `json:"https_port,string"`
	Port         FlexInt   `json:"port,string"`
	Process      bool      `json:"process"`
	RTMPPort     FlexInt   `json:"rtmp_port,string"`
	Protocol     string    `json:"server_protocol"`
	TimeNow      string    `json:"time_now"`
	TimestampNow Timestamp `json:"timestamp_now,string"`
	Timezone     string    `json:"timezone"`
	URL          string    `json:"url"`
}

// UserInfo is the current state of the user as it relates to the Xtream-Codes server.
type UserInfo struct {
	ActiveConnections    FlexInt            `json:"active_cons,string"`
	AllowedOutputFormats []string           `json:"allowed_output_formats"`
	Auth                 ConvertibleBoolean `json:"auth"`
	CreatedAt            Timestamp          `json:"created_at"`
	ExpDate              *Timestamp         `json:"exp_date"`
	IsTrial              ConvertibleBoolean `json:"is_trial,string"`
	MaxConnections       FlexInt            `json:"max_connections,string"`
	Message              string             `json:"message"`
	Password             string             `json:"password"`
	Status               string             `json:"status"`
	Username             string             `json:"username"`
}

// AuthenticationResponse is a container for what the server returns after the initial authentication.
type AuthenticationResponse struct {
	ServerInfo ServerInfo `json:"server_info"`
	UserInfo   UserInfo   `json:"user_info"`
}

// Category describes a grouping of Stream.
type Category struct {
	ID     FlexInt `json:"category_id,string"`
	Name   string  `json:"category_name"`
	Parent FlexInt `json:"parent_id"`

	// Set by us, not Xtream.
	Type string `json:"-"`
}

// Stream is a streamble video source.
type Stream struct {
	Added              *Timestamp `json:"added"`
	CategoryID         FlexInt    `json:"category_id,string"`
	CategoryName       string     `json:"category_name"`
	ContainerExtension string     `json:"container_extension"`
	CustomSid          string     `json:"custom_sid"`
	DirectSource       string     `json:"direct_source,omitempty"`
	EPGChannelID       string     `json:"epg_channel_id"`
	Icon               string     `json:"stream_icon"`
	ID                 FlexInt    `json:"stream_id"`
	Name               string     `json:"name"`
	Number             FlexInt    `json:"num"`
	Rating             FlexFloat  `json:"rating"`
	Rating5based       FlexFloat  `json:"rating_5based"`
	TVArchive          FlexInt    `json:"tv_archive"`
	TVArchiveDuration  *FlexInt   `json:"tv_archive_duration"`
	Type               string     `json:"stream_type"`
}

// SeriesInfo contains information about a TV series.
type SeriesInfo struct {
	BackdropPath   *JSONStringSlice `json:"backdrop_path,omitempty"`
	Cast           string           `json:"cast"`
	CategoryID     *FlexInt         `json:"category_id,string"`
	Cover          string           `json:"cover"`
	Director       string           `json:"director"`
	EpisodeRunTime string           `json:"episode_run_time"`
	Genre          string           `json:"genre"`
	LastModified   *Timestamp       `json:"last_modified,omitempty"`
	Name           string           `json:"name"`
	Num            FlexInt          `json:"num"`
	Plot           string           `json:"plot"`
	Rating         FlexInt          `json:"rating,string"`
	Rating5        FlexFloat        `json:"rating_5based"`
	ReleaseDate    string           `json:"releaseDate"`
	SeriesID       FlexInt          `json:"series_id"`
	StreamType     string           `json:"stream_type"`
	YoutubeTrailer string           `json:"youtube_trailer"`
}

type SeriesEpisode struct {
	Added              string  `json:"added"`
	ContainerExtension string  `json:"container_extension"`
	CustomSid          string  `json:"custom_sid"`
	DirectSource       string  `json:"direct_source"`
	EpisodeNum         FlexInt `json:"episode_num"`
	ID                 string  `json:"id"`
	Info               struct {
		Audio        FFMPEGStreamInfo `json:"audio"`
		Bitrate      FlexInt          `json:"bitrate"`
		Duration     string           `json:"duration"`
		DurationSecs FlexInt          `json:"duration_secs"`
		MovieImage   string           `json:"movie_image"`
		Name         string           `json:"name"`
		Plot         string           `json:"plot"`
		Rating       FlexFloat        `json:"rating"`
		ReleaseDate  string           `json:"releasedate"`
		Video        FFMPEGStreamInfo `json:"video"`
	} `json:"info"`
	Season FlexInt `json:"season"`
	Title  string  `json:"title"`
}

type Series struct {
	Episodes map[string][]SeriesEpisode `json:"episodes"`
	Info     SeriesInfo                 `json:"info"`
	Seasons  []interface{}              `json:"seasons"`
}

// VideoOnDemandInfo contains information about a video on demand stream.
type VideoOnDemandInfo struct {
	Info struct {
		Audio          FFMPEGStreamInfo `json:"audio"`
		BackdropPath   []string         `json:"backdrop_path"`
		Bitrate        FlexInt          `json:"bitrate"`
		Cast           string           `json:"cast"`
		Director       string           `json:"director"`
		Duration       string           `json:"duration"`
		DurationSecs   FlexInt          `json:"duration_secs"`
		Genre          string           `json:"genre"`
		MovieImage     string           `json:"movie_image"`
		Plot           string           `json:"plot"`
		Rating         FlexFloat        `json:"rating"`
		ReleaseDate    string           `json:"releasedate"`
		TmdbID         FlexInt          `json:"tmdb_id"`
		Video          FFMPEGStreamInfo `json:"video"`
		YoutubeTrailer string           `json:"youtube_trailer"`
	} `json:"info"`
	MovieData struct {
		Added              Timestamp `json:"added"`
		CategoryID         FlexInt   `json:"category_id,string"`
		ContainerExtension string    `json:"container_extension"`
		CustomSid          string    `json:"custom_sid"`
		DirectSource       string    `json:"direct_source"`
		Name               string    `json:"name"`
		StreamID           FlexInt   `json:"stream_id"`
	} `json:"movie_data"`
}

type epgContainer struct {
	EPGListings []EPGInfo `json:"epg_listings"`
}

// EPGInfo describes electronic programming guide information of a stream.
type EPGInfo struct {
	ChannelID      string             `json:"channel_id"`
	Description    Base64Value        `json:"description"`
	End            string             `json:"end"`
	EPGID          FlexInt            `json:"epg_id,string"`
	HasArchive     ConvertibleBoolean `json:"has_archive"`
	ID             FlexInt            `json:"id,string"`
	Lang           string             `json:"lang"`
	NowPlaying     ConvertibleBoolean `json:"now_playing"`
	Start          string             `json:"start"`
	StartTimestamp Timestamp          `json:"start_timestamp"`
	StopTimestamp  Timestamp          `json:"stop_timestamp"`
	Title          Base64Value        `json:"title"`
}
