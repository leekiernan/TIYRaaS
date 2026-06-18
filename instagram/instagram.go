package instagram

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/charmbracelet/log"
)

const (
	baseURL     = "https://instagram120.p.rapidapi.com/api/instagram"
	defaultHost = "instagram120.p.rapidapi.com"
)

// Client calls the instagram120 RapidAPI endpoints.
type Client struct {
	host string
	key  string
	http *http.Client
	log  *log.Logger
}

// New returns an Instagram client. The key is the user's RapidAPI key.
func New(key string, logger *log.Logger) *Client {
	return &Client{
		host: defaultHost,
		key:  key,
		http: &http.Client{Timeout: 30 * time.Second},
		log:  logger,
	}
}

// Media is a single downloadable item resolved from Instagram.
type Media struct {
	URL      string
	Filename string
	IsVideo  bool
}

type storyCandidate struct {
	URL    string `json:"url"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

// StoryItem is one element of an Instagram stories tray.
type StoryItem struct {
	TakenAt int64 `json:"taken_at"`
	PK      any   `json:"pk"`

	VideoVersions []storyCandidate `json:"video_versions"`
	ImageVersions2 struct {
		Candidates []storyCandidate `json:"candidates"`
	} `json:"image_versions2"`
}

type storiesResponse struct {
	Result []StoryItem `json:"result"`

	// Error fields (present when success=false).
	Success      bool   `json:"success"`
	Message      string `json:"message"`
	ResponseType string `json:"response_type"`
}

// FetchStories returns all active stories for the given username.
func (c *Client) FetchStories(username string) ([]StoryItem, error) {
	body, _ := json.Marshal(map[string]string{"username": username})

	c.log.Info("instagram fetch stories", "username", username)
	out, err := c.post("/stories", body)
	if err != nil {
		return nil, err
	}

	var resp storiesResponse
	if err := json.Unmarshal(out, &resp); err != nil {
		return nil, fmt.Errorf("instagram: decode stories: %w", err)
	}
	if !resp.Success && resp.Message != "" {
		return nil, fmt.Errorf("instagram: %s", resp.Message)
	}
	return resp.Result, nil
}

// ShortcodeMedia is one element of a mediaByShortcode response (carousel
// posts produce multiple entries).
type ShortcodeMedia struct {
	URLs []struct {
		URL       string `json:"url"`
		Name      string `json:"name"`
		SubName   string `json:"subName"`
		Extension string `json:"extension"`
		Quality   int    `json:"quality"`
	} `json:"urls"`
	Meta struct {
		Title     string `json:"title"`
		SourceURL string `json:"sourceUrl"`
		Shortcode string `json:"shortcode"`
		Username  string `json:"username"`
		TakenAt   int64  `json:"takenAt"`
	} `json:"meta"`
	PictureURL string `json:"pictureUrl"`
}

// FetchByShortcode resolves a /p/, /reel/, or /tv/ shortcode to its media.
func (c *Client) FetchByShortcode(shortcode string) ([]ShortcodeMedia, error) {
	body, _ := json.Marshal(map[string]string{"shortcode": shortcode})

	c.log.Info("instagram fetch shortcode", "shortcode", shortcode)
	out, err := c.post("/mediaByShortcode", body)
	if err != nil {
		return nil, err
	}

	var resp []ShortcodeMedia
	if err := json.Unmarshal(out, &resp); err != nil {
		// Upstream sometimes returns an error object instead of an array.
		var errResp storiesResponse
		if err2 := json.Unmarshal(out, &errResp); err2 == nil && errResp.Message != "" {
			return nil, fmt.Errorf("instagram: %s", errResp.Message)
		}
		return nil, fmt.Errorf("instagram: decode media: %w", err)
	}
	if len(resp) == 0 {
		return nil, fmt.Errorf("instagram: no media for shortcode %s", shortcode)
	}
	return resp, nil
}

func (c *Client) post(endpoint string, body []byte) ([]byte, error) {
	req, err := http.NewRequest(http.MethodPost, baseURL+endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-rapidapi-host", c.host)
	req.Header.Set("x-rapidapi-key", c.key)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	out, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	// Surface RapidAPI-level errors (quota, auth, etc.) which come back as
	// {"message": "..."} regardless of endpoint.
	var topLevel struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(out, &topLevel) == nil && topLevel.Message != "" {
		return nil, fmt.Errorf("instagram: %s", topLevel.Message)
	}
	return out, nil
}

// StoryMedia converts a tray of StoryItem into downloadable Media.
// For video stories we pick the first (highest-quality) rendition; for
// image stories we pick the largest candidate by width.
func StoryMedia(items []StoryItem) []Media {
	out := make([]Media, 0, len(items))
	for i, item := range items {
		if len(item.VideoVersions) > 0 {
			out = append(out, Media{
				URL:      item.VideoVersions[0].URL,
				Filename: fmt.Sprintf("story_%03d.mp4", i+1),
				IsVideo:  true,
			})
			continue
		}
		best := bestImage(item.ImageVersions2.Candidates)
		if best.URL == "" {
			continue
		}
		out = append(out, Media{
			URL:      best.URL,
			Filename: fmt.Sprintf("story_%03d.jpg", i+1),
		})
	}
	return out
}

// ShortcodeMedia converts a mediaByShortcode response into downloadable Media.
// Each carousel entry yields one file (video if URLS present, else the image).
func ShortcodeToMedia(items []ShortcodeMedia) []Media {
	out := make([]Media, 0, len(items))
	for i, item := range items {
		if len(item.URLs) > 0 && item.URLs[0].URL != "" {
			ext := normalizeExt(item.URLs[0].Extension)
			if ext == "" {
				ext = normalizeExt(extFromURL(item.URLs[0].URL))
			}
			if ext == "" {
				ext = ".mp4"
			}
			out = append(out, Media{
				URL:      item.URLs[0].URL,
				Filename: fmt.Sprintf("media_%03d%s", i+1, ext),
				IsVideo:  ext == ".mp4" || ext == ".mov" || ext == ".webm" || ext == ".m4v",
			})
			continue
		}
		if item.PictureURL != "" {
			ext := normalizeExt(extFromURL(item.PictureURL))
			if ext == "" {
				ext = ".jpg"
			}
			out = append(out, Media{
				URL:      item.PictureURL,
				Filename: fmt.Sprintf("media_%03d%s", i+1, ext),
			})
		}
	}
	return out
}

func bestImage(candidates []storyCandidate) storyCandidate {
	var best storyCandidate
	for _, c := range candidates {
		if c.Width > best.Width {
			best = c
		}
	}
	return best
}

// URLKind classifies an instagram.com URL so the caller knows which
// upstream endpoint to hit.
type URLKind int

const (
	URLUnknown URLKind = iota
	URLProfile  // instagram.com/{username}
	URLPost     // instagram.com/p/{shortcode}
	URLReel     // instagram.com/reel/{shortcode} or /reels/{shortcode}
)

// ParsedURL holds the classification of an Instagram URL.
type ParsedURL struct {
	Kind     URLKind
	Username string
	Shortcode string
}

var reservedPaths = map[string]bool{
	"p": true, "reel": true, "reels": true, "tv": true, "stories": true,
	"explore": true, "accounts": true, "about": true, "developer": true,
	"directory": true, "legal": true, "press": true, "tags": true,
	"locations": true, "emails": true,
}

// ParseURL classifies an Instagram URL.
func ParseURL(in string) ParsedURL {
	parsed, err := url.Parse(strings.TrimSpace(in))
	if err != nil {
		return ParsedURL{Kind: URLUnknown}
	}
	host := strings.ToLower(parsed.Hostname())
	if !strings.HasSuffix(host, "instagram.com") && !strings.HasSuffix(host, "instagr.am") {
		return ParsedURL{Kind: URLUnknown}
	}

	segments := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if len(segments) == 0 || segments[0] == "" {
		return ParsedURL{Kind: URLUnknown}
	}

	// Post / reel markers can appear at any depth — Instagram serves both
	// /p/{shortcode} and /{username}/p/{shortcode}. Scan for them first.
	for i, seg := range segments {
		next := ""
		if i+1 < len(segments) {
			next = segments[i+1]
		}
		if next == "" {
			continue
		}
		if seg == "p" {
			return ParsedURL{Kind: URLPost, Shortcode: next}
		}
		if seg == "reel" || seg == "reels" || seg == "tv" {
			return ParsedURL{Kind: URLReel, Shortcode: next}
		}
	}

	first := segments[0]
	if reservedPaths[first] {
		return ParsedURL{Kind: URLUnknown}
	}
	// No post/reel marker found — treat the first segment as a username.
	return ParsedURL{Kind: URLProfile, Username: first}
}

var mediaExts = []string{".jpg", ".jpeg", ".png", ".gif", ".webp", ".mp4", ".webm", ".mov"}

// normalizeExt ensures an extension is dot-prefixed and lowercase; an empty
// input yields an empty output.
func normalizeExt(ext string) string {
	ext = strings.ToLower(strings.TrimSpace(ext))
	if ext == "" {
		return ""
	}
	if !strings.HasPrefix(ext, ".") {
		ext = "." + ext
	}
	return ext
}

func extFromURL(u string) string {
	parsed, err := url.Parse(u)
	if err != nil {
		return ""
	}
	ext := strings.ToLower(path.Ext(parsed.Path))
	for _, ok := range mediaExts {
		if ext == ok {
			return ext
		}
	}
	return ""
}
