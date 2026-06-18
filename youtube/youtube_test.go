package youtube

import "testing"

func TestParseURL(t *testing.T) {
	cases := []struct {
		in      string
		kind    URLKind
		videoID string
	}{
		{"https://www.youtube.com/shorts/xvagLGkal4I", URLShorts, "xvagLGkal4I"},
		{"https://youtube.com/shorts/k769hteV2u0?is=wMG8VAu6JP6w5jmQ", URLShorts, "k769hteV2u0"},
		{"https://youtube.com/shorts/gvmkJVFPcus/", URLShorts, "gvmkJVFPcus"},
		{"https://www.youtube.com/shorts/abc/?feature=share", URLShorts, "abc"},
		{"  https://www.youtube.com/shorts/xyz  ", URLShorts, "xyz"},
	}
	for _, c := range cases {
		got := ParseURL(c.in)
		if got.Kind != c.kind || got.VideoID != c.videoID {
			t.Errorf("ParseURL(%q) = %+v want kind=%d videoID=%q",
				c.in, got, c.kind, c.videoID)
		}
	}
}

func TestParseURLRejectsNonShorts(t *testing.T) {
	bad := []string{
		"https://www.youtube.com/watch?v=xvagLGkal4I",
		"https://youtu.be/xvagLGkal4I",
		"https://www.youtube.com/embed/xvagLGkal4I",
		"https://www.youtube.com/",
		"https://www.youtube.com/@SomeChannel",
		"https://example.com/shorts/abc",
		"not a url",
	}
	for _, in := range bad {
		if got := ParseURL(in); got.Kind != URLUnknown {
			t.Errorf("ParseURL(%q) = %+v want URLUnknown", in, got)
		}
	}
}

func TestPickBestMuxed(t *testing.T) {
	streams := []Stream{
		// 360p muxed (has audio) — the typical itag 18 case
		{Itag: 18, URL: "u18", Height: 360, Bitrate: 500000, AudioSampleRate: "48000"},
		// higher-resolution but video-only (no audio)
		{Itag: 136, URL: "u136", Height: 720, Bitrate: 2000000},
		{Itag: 137, URL: "u137", Height: 1080, Bitrate: 4000000},
	}
	got, err := pickBestMuxed(streams, "abc")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got.Itag != 18 {
		t.Errorf("picked itag=%d want 18 (only stream with audio)", got.Itag)
	}
}

func TestPickBestMuxedPrefersHigherWhenBothHaveAudio(t *testing.T) {
	streams := []Stream{
		{Itag: 18, URL: "u18", Height: 360, Bitrate: 500000, AudioSampleRate: "48000"},
		// hypothetical second muxed stream at higher res
		{Itag: 22, URL: "u22", Height: 720, Bitrate: 1500000, AudioSampleRate: "48000"},
	}
	got, err := pickBestMuxed(streams, "abc")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got.Itag != 22 {
		t.Errorf("picked itag=%d want 22 (higher-res muxed)", got.Itag)
	}
}

func TestPickBestMuxedErrorsWhenAllVideoOnly(t *testing.T) {
	streams := []Stream{
		{Itag: 136, URL: "u136", Height: 720, Bitrate: 2000000},
		{Itag: 137, URL: "u137", Height: 1080, Bitrate: 4000000},
	}
	if _, err := pickBestMuxed(streams, "abc"); err == nil {
		t.Error("expected error when all streams are video-only")
	}
}

func TestPickBestMuxedErrorsOnEmpty(t *testing.T) {
	if _, err := pickBestMuxed(nil, "abc"); err == nil {
		t.Error("expected error for empty stream list")
	}
}

func TestHasAudio(t *testing.T) {
	cases := []struct {
		s    Stream
		want bool
	}{
		{Stream{AudioSampleRate: "48000"}, true},
		{Stream{AudioQuality: "AUDIO_QUALITY_LOW"}, true},
		{Stream{AudioChannels: 2}, true},
		{Stream{}, false},
		{Stream{Height: 720, Bitrate: 2000000}, false},
	}
	for _, c := range cases {
		if got := c.s.hasAudio(); got != c.want {
			t.Errorf("hasAudio()=%v want %v for %+v", got, c.want, c.s)
		}
	}
}
