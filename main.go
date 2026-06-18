package main

import (
	"archive/zip"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/charmbracelet/log"
	"github.com/sweepies/tok-dl/cache"
	"github.com/sweepies/tok-dl/tikwm"

	"tiktok-proxy/instagram"
	"tiktok-proxy/reddit"
)

type mediaItem struct {
	URL      string
	Filename string
	IsVideo  bool
}

func main() {
	logger := log.New(os.Stderr)

	secretToken := os.Getenv("SECRET_TOKEN")
	if secretToken == "" {
		logger.Fatal("SECRET_TOKEN environment variable is not set!")
	}

	rapidAPIKey := os.Getenv("RAPIDAPI_KEY")
	if rapidAPIKey == "" {
		logger.Fatal("RAPIDAPI_KEY environment variable is not set!")
	}

	cachePath := os.Getenv("CACHE_PATH")
	if cachePath == "" {
		cachePath = "/app/cache"
	}
	cacheStore := cache.New(cachePath)
	tiktokCaller := tikwm.New(cacheStore, logger)
	igClient := instagram.New(rapidAPIKey, logger)
	redditClient := reddit.New(rapidAPIKey, logger)

	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		logger.Info("Health check")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	http.HandleFunc("/download", func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		if authHeader != "Bearer "+secretToken {
			logger.Warn("Unauthorized access attempt", "ip", r.RemoteAddr)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		rawURL := r.URL.Query().Get("url")
		if rawURL == "" {
			http.Error(w, "Missing url parameter", http.StatusBadRequest)
			return
		}

		logger.Info("Processing request", "url", rawURL)

		switch classify(rawURL) {
		case "instagram":
			handleInstagram(w, r, igClient, rawURL, logger)
		case "reddit":
			handleReddit(w, r, redditClient, rawURL, logger)
		default:
			handleTikTok(w, r, tiktokCaller, rawURL, logger)
		}
	})

	logger.Info("Server starting", "port", 8080)
	log.Fatal(http.ListenAndServe(":8080", nil))
}

// classify routes the request to the right provider based on URL host.
func classify(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	host := strings.ToLower(u.Hostname())
	switch {
	case strings.HasSuffix(host, "instagram.com"), strings.HasSuffix(host, "instagr.am"):
		return "instagram"
	case strings.HasSuffix(host, "reddit.com"), strings.HasSuffix(host, "redd.it"):
		return "reddit"
	}
	return ""
}

func handleInstagram(w http.ResponseWriter, r *http.Request, c *instagram.Client, rawURL string, logger *log.Logger) {
	parsed := instagram.ParseURL(rawURL)
	switch parsed.Kind {
	case instagram.URLProfile:
		items, err := c.FetchStories(parsed.Username)
		if err != nil {
			logger.Error("Instagram stories fetch failed", "username", parsed.Username, "error", err)
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		media := instagram.StoryMedia(items)
		if len(media) == 0 {
			http.Error(w, "No active stories for "+parsed.Username, http.StatusNotFound)
			return
		}
		streamZip(w, igMediaItems(media), "stories_"+parsed.Username+".zip", logger)

	case instagram.URLPost, instagram.URLReel:
		items, err := c.FetchByShortcode(parsed.Shortcode)
		if err != nil {
			logger.Error("Instagram shortcode fetch failed", "shortcode", parsed.Shortcode, "error", err)
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		media := instagram.ShortcodeToMedia(items)
		if len(media) == 0 {
			http.Error(w, "No media found for shortcode "+parsed.Shortcode, http.StatusNotFound)
			return
		}
		if len(media) == 1 {
			streamSingle(w, mediaItem(media[0]), logger)
			return
		}
		streamZip(w, igMediaItems(media), "instagram_"+parsed.Shortcode+".zip", logger)

	default:
		http.Error(w, "Unsupported Instagram URL", http.StatusBadRequest)
	}
}

func handleReddit(w http.ResponseWriter, r *http.Request, c *reddit.Client, rawURL string, logger *log.Logger) {
	post, err := c.FetchPost(rawURL)
	if err != nil {
		logger.Error("Reddit fetch failed", "url", rawURL, "error", err)
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	media, err := c.ExtractMedia(post)
	if err != nil {
		logger.Error("Reddit extract failed", "id", post.ID, "error", err)
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	if len(media) == 1 {
		streamSingle(w, mediaItem(media[0]), logger)
		return
	}
	streamZip(w, toMediaItems(media), "reddit_"+post.ID+".zip", logger)
}

func handleTikTok(w http.ResponseWriter, r *http.Request, caller tikwm.ApiCaller, rawUrl string, logger *log.Logger) {
	metadata, err := caller.FetchMetadata(rawUrl)
	if err != nil {
		logger.Error("Metadata fetch failed", "error", err)
		http.Error(w, "Failed to fetch metadata", http.StatusInternalServerError)
		return
	}

	if len(metadata.Data.Images) > 0 {
		logger.Info("Detected Gallery", "count", len(metadata.Data.Images))
		items := make([]mediaItem, 0, len(metadata.Data.Images))
		for i, imgURL := range metadata.Data.Images {
			items = append(items, mediaItem{
				URL:      imgURL,
				Filename: fmt.Sprintf("image_%d.jpg", i+1),
			})
		}
		streamZip(w, items, "gallery.zip", logger)
		return
	}

	if metadata.Data.Play == "" {
		http.Error(w, "No video URL found", http.StatusNotFound)
		return
	}

	resp, err := http.Get(metadata.Data.Play)
	if err != nil {
		http.Error(w, "CDN error", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	w.Header().Set("Content-Type", "video/mp4")
	w.Header().Set("Content-Disposition", `attachment; filename="video.mp4"`)
	if _, err := io.Copy(w, resp.Body); err != nil {
		logger.Error("Stream interrupted", "error", err)
	}
}

// streamSingle downloads the media URL and streams it back as a single file.
func streamSingle(w http.ResponseWriter, item mediaItem, logger *log.Logger) {
	resp, err := http.Get(item.URL)
	if err != nil {
		logger.Error("CDN fetch failed", "url", item.URL, "err", err)
		http.Error(w, "CDN error", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	contentType := "application/octet-stream"
	if item.IsVideo {
		contentType = "video/mp4"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", item.Filename))
	w.Header().Set("Transfer-Encoding", "chunked")

	if _, err := io.Copy(w, resp.Body); err != nil {
		logger.Error("Stream interrupted", "file", item.Filename, "err", err)
	}
}

// streamZip fetches each media URL and writes them all into a streamed zip.
func streamZip(w http.ResponseWriter, items []mediaItem, name string, logger *log.Logger) {
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", name))
	w.Header().Set("Transfer-Encoding", "chunked")

	zw := zip.NewWriter(w)
	for _, item := range items {
		resp, err := http.Get(item.URL)
		if err != nil {
			logger.Error("Failed to fetch media", "url", item.URL, "err", err)
			continue
		}

		f, err := zw.Create(item.Filename)
		if err != nil {
			logger.Error("Failed to create zip entry", "file", item.Filename, "err", err)
			resp.Body.Close()
			continue
		}

		_, err = io.Copy(f, resp.Body)
		resp.Body.Close()
		if err != nil {
			logger.Error("Error copying media to zip", "file", item.Filename, "err", err)
		}
	}
	if err := zw.Close(); err != nil {
		logger.Error("Error finalizing zip", "err", err)
	}
}

func toMediaItems(rm []reddit.Media) []mediaItem {
	out := make([]mediaItem, len(rm))
	for i, m := range rm {
		out[i] = mediaItem(m)
	}
	return out
}

func igMediaItems(im []instagram.Media) []mediaItem {
	out := make([]mediaItem, len(im))
	for i, m := range im {
		out[i] = mediaItem(m)
	}
	return out
}
