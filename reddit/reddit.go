package reddit

import (
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/charmbracelet/log"
)

const (
	baseURL     = "https://reddit34.p.rapidapi.com"
	defaultHost = "reddit34.p.rapidapi.com"
)

// Client calls the reddit34 RapidAPI getPostDetails endpoint.
type Client struct {
	host string
	key  string
	http *http.Client
	log  *log.Logger
}

// New returns a Reddit client. The key is the user's RapidAPI key.
func New(key string, logger *log.Logger) *Client {
	return &Client{
		host: defaultHost,
		key:  key,
		http: &http.Client{Timeout: 30 * time.Second},
		log:  logger,
	}
}

// Media is a single downloadable media item extracted from a post.
type Media struct {
	URL      string
	Filename string
	IsVideo  bool
}

// Post is the subset of Reddit's post payload we care about.
type Post struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	IsVideo   bool   `json:"is_video"`
	IsGallery bool   `json:"is_gallery"`
	IsSelf    bool   `json:"is_self"`
	URL       string `json:"url"`
	Domain    string `json:"domain"`
	Media     *struct {
		RedditVideo *struct {
			FallbackURL string `json:"fallback_url"`
		} `json:"reddit_video"`
	} `json:"media"`
	GalleryData *struct {
		Items []struct {
			MediaID string `json:"media_id"`
		} `json:"items"`
	} `json:"gallery_data"`
	MediaMetadata map[string]struct {
		M string `json:"m"`
		S struct {
			U string `json:"u"`
		} `json:"s"`
	} `json:"media_metadata"`
}

// FetchPost resolves any Reddit post URL form to the canonical
// reddit.com/comments/{id} form (the only form the upstream API accepts)
// and fetches its metadata.
//
// Short share URLs (e.g. /r/{sub}/s/{shareId}) are followed via HTTP redirect
// before normalization, since they don't contain the post ID themselves.
func (c *Client) FetchPost(postURL string) (*Post, error) {
	normalized, id, err := NormalizeURL(postURL)
	if err != nil {
		// Could be a share URL — follow redirects and retry.
		resolved, resolveErr := c.resolveShareURL(postURL)
		if resolveErr != nil {
			return nil, err
		}
		c.log.Info("reddit share resolved", "from", postURL, "to", resolved)
		normalized, id, err = NormalizeURL(resolved)
		if err != nil {
			return nil, err
		}
	}

	endpoint, err := url.Parse(baseURL + "/getPostDetails")
	if err != nil {
		return nil, err
	}
	q := endpoint.Query()
	q.Set("post_url", normalized)
	endpoint.RawQuery = q.Encode()

	req, err := http.NewRequest(http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("x-rapidapi-host", c.host)
	req.Header.Set("x-rapidapi-key", c.key)

	c.log.Info("reddit fetch", "url", normalized)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	// RapidAPI-level errors (quota, auth, etc.) come back as {"message": "..."}.
	var topLevel struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(body, &topLevel) == nil && topLevel.Message != "" {
		return nil, fmt.Errorf("reddit: %s", topLevel.Message)
	}

	// "data" is either a string error or the post object.
	var raw struct {
		Success bool            `json:"success"`
		Data    json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("reddit: invalid response: %w", err)
	}

	if len(raw.Data) == 0 || string(raw.Data) == "\"\"" {
		return nil, fmt.Errorf("reddit: empty response for %s", normalized)
	}

	var post Post
	if err := json.Unmarshal(raw.Data, &post); err != nil {
		var msg string
		if err2 := json.Unmarshal(raw.Data, &msg); err2 == nil {
			return nil, fmt.Errorf("reddit: %s", msg)
		}
		return nil, fmt.Errorf("reddit: decode: %w", err)
	}

	post.ID = id
	return &post, nil
}

// ExtractMedia pulls downloadable URLs out of a post.
func (c *Client) ExtractMedia(post *Post) ([]Media, error) {
	if post.IsSelf {
		return nil, fmt.Errorf("reddit: text post has no media")
	}

	// Reddit-hosted video.
	if post.IsVideo && post.Media != nil && post.Media.RedditVideo != nil && post.Media.RedditVideo.FallbackURL != "" {
		return []Media{{
			URL:      post.Media.RedditVideo.FallbackURL,
			Filename: "video.mp4",
			IsVideo:  true,
		}}, nil
	}

	// Image gallery.
	if post.IsGallery && post.GalleryData != nil {
		var out []Media
		for i, item := range post.GalleryData.Items {
			meta, ok := post.MediaMetadata[item.MediaID]
			if !ok || meta.S.U == "" {
				continue
			}
			u := html.UnescapeString(meta.S.U)
			ext := extFromMime(meta.M)
			if ext == "" {
				ext = extFromURL(u)
			}
			out = append(out, Media{
				URL:      u,
				Filename: fmt.Sprintf("image_%03d%s", i+1, ext),
			})
		}
		if len(out) > 0 {
			return out, nil
		}
	}

	// Single image / direct media link.
	if post.URL != "" && looksLikeMedia(post.URL) {
		return []Media{{
			URL:      post.URL,
			Filename: "media" + extFromURL(post.URL),
		}}, nil
	}

	return nil, fmt.Errorf("reddit: no downloadable media in post %s", post.ID)
}

// resolveShareURL follows HTTP redirects on a shortened Reddit share URL
// (e.g. /r/{sub}/s/{shareId}) to its canonical form. The default
// http.Client follows redirects automatically; we just capture where it
// landed.
func (c *Client) resolveShareURL(in string) (string, error) {
	req, err := http.NewRequest(http.MethodHead, in, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "tiktok-proxy/1.0")

	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	final := resp.Request.URL.String()
	if final == "" || final == in {
		return "", fmt.Errorf("reddit: no redirect for %s", in)
	}
	return final, nil
}

// NormalizeURL converts any Reddit URL form to the canonical
// https://www.reddit.com/comments/{id} which is the only form the
// upstream API accepts.
func NormalizeURL(in string) (normalized, id string, err error) {
	parsed, err := url.Parse(strings.TrimSpace(in))
	if err != nil {
		return "", "", fmt.Errorf("reddit: invalid URL: %w", err)
	}
	host := strings.ToLower(parsed.Hostname())
	if !strings.HasSuffix(host, "reddit.com") && !strings.HasSuffix(host, "redd.it") {
		return "", "", fmt.Errorf("reddit: not a reddit URL: %s", in)
	}

	segments := strings.Split(strings.Trim(parsed.Path, "/"), "/")

	if strings.HasSuffix(host, "redd.it") && len(segments) >= 1 && segments[0] != "" {
		id := segments[0]
		return "https://www.reddit.com/comments/" + id, id, nil
	}

	for i, seg := range segments {
		if seg == "comments" && i+1 < len(segments) && segments[i+1] != "" {
			id := segments[i+1]
			return "https://www.reddit.com/comments/" + id, id, nil
		}
	}

	return "", "", fmt.Errorf("reddit: could not extract post ID from %s", in)
}

var mediaExts = []string{".jpg", ".jpeg", ".png", ".gif", ".webp", ".mp4", ".webm", ".mov"}

func looksLikeMedia(u string) bool {
	low := strings.ToLower(u)
	for _, ext := range mediaExts {
		if strings.Contains(low, ext) {
			return true
		}
	}
	return false
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

func extFromMime(m string) string {
	switch strings.ToLower(m) {
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	case "video/mp4":
		return ".mp4"
	}
	return ""
}
