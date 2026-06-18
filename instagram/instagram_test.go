package instagram

import "testing"

func TestParseURL(t *testing.T) {
	cases := []struct {
		in        string
		kind      URLKind
		username  string
		shortcode string
	}{
		{"https://www.instagram.com/natgeo/", URLProfile, "natgeo", ""},
		{"https://instagram.com/natgeo", URLProfile, "natgeo", ""},
		{"https://www.instagram.com/p/DPRcWdvgI4P/", URLPost, "", "DPRcWdvgI4P"},
		{"https://instagr.am/p/ABC123", URLPost, "", "ABC123"},
		{"https://www.instagram.com/reel/DPRcWdvgI4P/", URLReel, "", "DPRcWdvgI4P"},
		{"https://www.instagram.com/reels/DPRcWdvgI4P/", URLReel, "", "DPRcWdvgI4P"},
		{"https://www.instagram.com/tv/ABC123/", URLReel, "", "ABC123"},
		// Username-prefixed post URLs (Instagram serves these too).
		{"https://www.instagram.com/sunsetonthehorizon/p/DW4mq6ijD6D/", URLPost, "", "DW4mq6ijD6D"},
		{"https://www.instagram.com/natgeo/reel/ABC123/", URLReel, "", "ABC123"},
		{"https://www.instagram.com/natgeo/reels/ABC123/", URLReel, "", "ABC123"},
	}
	for _, c := range cases {
		got := ParseURL(c.in)
		if got.Kind != c.kind || got.Username != c.username || got.Shortcode != c.shortcode {
			t.Errorf("ParseURL(%q) = %+v want kind=%d username=%q shortcode=%q",
				c.in, got, c.kind, c.username, c.shortcode)
		}
	}
}

func TestParseURLReservedAndBad(t *testing.T) {
	bad := []string{
		"https://www.instagram.com/explore/tags/foo/",
		"https://www.instagram.com/accounts/login/",
		"https://www.instagram.com/stories/natgeo/123/",
		"https://www.instagram.com/",
		"https://example.com/x",
		"not a url",
	}
	for _, in := range bad {
		if got := ParseURL(in); got.Kind != URLUnknown {
			t.Errorf("ParseURL(%q) = %+v want URLUnknown", in, got)
		}
	}
}

func TestStoryMediaPicksVideo(t *testing.T) {
	items := []StoryItem{
		{VideoVersions: []storyCandidate{{URL: "v1.mp4", Width: 720}, {URL: "v2.mp4", Width: 480}}},
		{ImageVersions2: struct {
			Candidates []storyCandidate `json:"candidates"`
		}{
			Candidates: []storyCandidate{
				{URL: "small.jpg", Width: 480},
				{URL: "big.jpg", Width: 1080},
			},
		}},
	}
	got := StoryMedia(items)
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].URL != "v1.mp4" || !got[0].IsVideo || got[0].Filename != "story_001.mp4" {
		t.Errorf("item 0 = %+v", got[0])
	}
	// Largest image candidate should win.
	if got[1].URL != "big.jpg" || got[1].IsVideo || got[1].Filename != "story_002.jpg" {
		t.Errorf("item 1 = %+v", got[1])
	}
}

func TestShortcodeToMediaNormalizesExt(t *testing.T) {
	items := []ShortcodeMedia{
		{URLs: []struct {
			URL       string `json:"url"`
			Name      string `json:"name"`
			SubName   string `json:"subName"`
			Extension string `json:"extension"`
			Quality   int    `json:"quality"`
		}{{URL: "v.mp4", Extension: "mp4"}}},
		{PictureURL: "https://example.com/x.jpg"},
	}
	got := ShortcodeToMedia(items)
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].Filename != "media_001.mp4" || !got[0].IsVideo {
		t.Errorf("item 0 = %+v, want .mp4 IsVideo=true", got[0])
	}
	if got[1].Filename != "media_002.jpg" || got[1].IsVideo {
		t.Errorf("item 1 = %+v, want .jpg IsVideo=false", got[1])
	}
}
