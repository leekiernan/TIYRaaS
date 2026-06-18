package reddit

import "testing"

func TestNormalizeURL(t *testing.T) {
	cases := []struct{ in, wantID string }{
		{"https://www.reddit.com/r/pics/comments/1fzlnqa/is_this_real/", "1fzlnqa"},
		{"https://reddit.com/r/funny/comments/1abc234", "1abc234"},
		{"https://www.reddit.com/comments/1abc234", "1abc234"},
		{"https://redd.it/1abc234", "1abc234"},
		{"https://old.reddit.com/r/x/comments/1abc234/slug/?ctx=3", "1abc234"},
		{"  https://www.reddit.com/r/aww/comments/1abcxyz/my_title  ", "1abcxyz"},
	}
	for _, c := range cases {
		norm, id, err := NormalizeURL(c.in)
		if err != nil {
			t.Errorf("NormalizeURL(%q) err: %v", c.in, err)
			continue
		}
		if id != c.wantID {
			t.Errorf("NormalizeURL(%q) id=%q want %q", c.in, id, c.wantID)
		}
		wantNorm := "https://www.reddit.com/comments/" + c.wantID
		if norm != wantNorm {
			t.Errorf("NormalizeURL(%q) normalized=%q want %q", c.in, norm, wantNorm)
		}
	}
}

func TestNormalizeURLRejects(t *testing.T) {
	bad := []string{
		"https://example.com/x",
		"https://www.instagram.com/p/abc/",
		"https://www.reddit.com/",
		"https://www.reddit.com/r/pics/",
		"not a url",
	}
	for _, in := range bad {
		if _, _, err := NormalizeURL(in); err == nil {
			t.Errorf("NormalizeURL(%q) expected error, got nil", in)
		}
	}
}

func TestExtFromMime(t *testing.T) {
	cases := map[string]string{
		"image/jpeg": ".jpg",
		"image/png":  ".png",
		"image/gif":  ".gif",
		"video/mp4":  ".mp4",
		"image/webp": ".webp",
		"":           "",
		"weird/odd":  "",
	}
	for in, want := range cases {
		if got := extFromMime(in); got != want {
			t.Errorf("extFromMime(%q)=%q want %q", in, got, want)
		}
	}
}

func TestLooksLikeMedia(t *testing.T) {
	good := []string{
		"https://i.redd.it/abc.jpg",
		"https://preview.redd.it/x.png?width=100",
		"https://v.redd.it/abc.mp4",
	}
	bad := []string{
		"https://example.com/article",
		"https://blog.example.com/post/123",
		"",
	}
	for _, in := range good {
		if !looksLikeMedia(in) {
			t.Errorf("looksLikeMedia(%q)=false want true", in)
		}
	}
	for _, in := range bad {
		if looksLikeMedia(in) {
			t.Errorf("looksLikeMedia(%q)=true want false", in)
		}
	}
}
