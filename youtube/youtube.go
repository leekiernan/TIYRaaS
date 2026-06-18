package youtube

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/charmbracelet/log"
)

const (
	baseURL     = "https://youtube-data16.p.rapidapi.com/files/video"
	defaultHost = "youtube-data16.p.rapidapi.com"
)

// Client calls the youtube-data16 RapidAPI file endpoint.
type Client struct {
	host string
	key  string
	http *http.Client
	log  *log.Logger
}

// New returns a YouTube client. The key is the user's RapidAPI key.
func New(key string, logger *log.Logger) *Client {
	return &Client{
		host: defaultHost,
		key:  key,
		http: &http.Client{Timeout: 30 * time.Second},
		log:  logger,
	}
}

// Stream is one rendition from the YouTube file list.
type Stream struct {
	Itag          int    `json:"itag"`
	URL           string `json:"url"`
	MimeType      string `json:"mimeType"`
	Bitrate       int    `json:"bitrate"`
	Width         int    `json:"width"`
	Height        int    `json:"height"`
	ContentLength string `json:"contentLength"`
	Quality       string `json:"quality"`
	QualityLabel  string `json:"qualityLabel"`
	// Muxed (audio+video) streams carry these; video-only streams omit them.
	AudioQuality    string `json:"audioQuality,omitempty"`
	AudioSampleRate string `json:"audioSampleRate,omitempty"`
	AudioChannels   int    `json:"audioChannels,omitempty"`
	// human-readable size, e.g. "5.6 MiB"
	ParsedContentLength string `json:"parsedContentLength,omitempty"`
}

// hasAudio reports whether the stream is muxed with an audio track.
// Higher-resolution YouTube streams (itag 136/137/248/313/...) are
// video-only and would be silent if downloaded directly.
func (s Stream) hasAudio() bool {
	return s.AudioSampleRate != "" || s.AudioQuality != "" || s.AudioChannels > 0
}

// FetchShorts resolves a YouTube Shorts video ID to a single muxed stream.
// We require a muxed (audio+video) rendition; the upstream API typically
// only returns itag 18 (360p mp4) for that, which keeps bandwidth low.
func (c *Client) FetchShorts(videoID string) (Stream, error) {
	videoID = strings.TrimSpace(videoID)
	if videoID == "" {
		return Stream{}, fmt.Errorf("youtube: empty video ID")
	}

	endpoint := baseURL + "/" + videoID
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return Stream{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-rapidapi-host", c.host)
	req.Header.Set("x-rapidapi-key", c.key)

	c.log.Info("youtube fetch", "video_id", videoID)
	resp, err := c.http.Do(req)
	if err != nil {
		return Stream{}, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return Stream{}, err
	}

	// Surface RapidAPI-level errors (quota, auth, etc.) which come back as
	// {"message": "..."} regardless of endpoint.
	var topLevel struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(body, &topLevel) == nil && topLevel.Message != "" {
		return Stream{}, fmt.Errorf("youtube: %s", topLevel.Message)
	}

	var streams []Stream
	if err := json.Unmarshal(body, &streams); err != nil {
		return Stream{}, fmt.Errorf("youtube: decode streams: %w", err)
	}

	return pickBestMuxed(streams, videoID)
}

// pickBestMuxed returns the highest-resolution muxed (audio+video) stream.
// Falls back through a series of safe choices so we never hand back a
// silent video stream.
func pickBestMuxed(streams []Stream, videoID string) (Stream, error) {
	if len(streams) == 0 {
		return Stream{}, fmt.Errorf("youtube: no streams returned for %s", videoID)
	}

	var best Stream
	var found bool
	for _, s := range streams {
		if !s.hasAudio() || s.URL == "" {
			continue
		}
		// Highest muxed stream by height; ties broken by bitrate.
		if !found ||
			s.Height > best.Height ||
			(s.Height == best.Height && s.Bitrate > best.Bitrate) {
			best = s
			found = true
		}
	}
	if !found {
		return Stream{}, fmt.Errorf("youtube: no muxed (audio+video) stream for %s — all renditions are video-only", videoID)
	}
	return best, nil
}

// URLKind classifies a youtube.com URL.
type URLKind int

const (
	URLUnknown URLKind = iota
	URLShorts // youtube.com/shorts/{videoID}
)

// ParsedURL holds the classification of a YouTube URL.
type ParsedURL struct {
	Kind    URLKind
	VideoID string
}

// ParseURL extracts the Shorts video ID from a youtube.com URL.
// Non-shorts URLs (watch?v=, youtu.be/, /embed/) are rejected since we
// only handle Shorts to keep bandwidth bounded.
func ParseURL(in string) ParsedURL {
	parsed, err := url.Parse(strings.TrimSpace(in))
	if err != nil {
		return ParsedURL{Kind: URLUnknown}
	}
	host := strings.ToLower(parsed.Hostname())
	if !strings.HasSuffix(host, "youtube.com") && !strings.HasSuffix(host, "youtube-nocookie.com") {
		return ParsedURL{Kind: URLUnknown}
	}

	segments := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	for i, seg := range segments {
		next := ""
		if i+1 < len(segments) {
			next = segments[i+1]
		}
		if seg == "shorts" && next != "" {
			return ParsedURL{Kind: URLShorts, VideoID: next}
		}
	}
	return ParsedURL{Kind: URLUnknown}
}
