package trailers

import "testing"

// The URL is the one untrusted input that reaches a command line, so the
// accept list is short and the reject list is what matters: anything
// that is not plainly a YouTube video must fail, including every
// spelling of "..".
func TestExtractVideoID(t *testing.T) {
	const want = "dQw4w9WgXcQ"
	accept := []string{
		"https://www.youtube.com/watch?v=dQw4w9WgXcQ",
		"http://youtube.com/watch?v=dQw4w9WgXcQ&t=10s",
		"https://m.youtube.com/watch?feature=share&v=dQw4w9WgXcQ",
		"https://music.youtube.com/watch?v=dQw4w9WgXcQ",
		"https://www.youtube.com/watch/?v=dQw4w9WgXcQ",
		"https://youtu.be/dQw4w9WgXcQ",
		"https://youtu.be/dQw4w9WgXcQ?t=42",
		"https://www.youtube.com/embed/dQw4w9WgXcQ",
		"https://www.youtube.com/embed/dQw4w9WgXcQ?autoplay=1",
		"https://www.youtube.com/shorts/dQw4w9WgXcQ",
		"https://WWW.YOUTUBE.COM/watch?v=dQw4w9WgXcQ",
		"  https://www.youtube.com/watch?v=dQw4w9WgXcQ  ",
	}
	for _, u := range accept {
		got, err := ExtractVideoID(u)
		if err != nil || got != want {
			t.Errorf("%q: got (%q, %v), want (%q, nil)", u, got, err, want)
		}
	}

	// The alphabet includes - and _, and an id may start with either.
	if got, err := ExtractVideoID("https://youtu.be/-b_c1234Zz9"); err != nil || got != "-b_c1234Zz9" {
		t.Errorf("dash/underscore id: got (%q, %v)", got, err)
	}

	reject := []string{
		"",
		"not a url",
		"https://vimeo.com/12345",
		"https://www.youtube.com/watch",
		"https://www.youtube.com/watch?v=",
		"https://www.youtube.com/watch?v=short",
		"https://www.youtube.com/watch?v=toolongvideoid1",
		"https://www.youtube.com/watch?v=dQw4w9WgXc!",
		"https://www.youtube.com/watch?v=dQw4w9WgXcQ%0A",
		"https://www.youtube.com/watch?v=../../etc/passwd",
		"https://www.youtube.com/embed/../../../etc/passwd",
		"https://www.youtube.com/embed/..%2F..%2Fetc%2Fpasswd",
		"https://www.youtube.com/embed/",
		"https://www.youtube.com/shorts/",
		"https://youtu.be/",
		"https://youtu.be/../alexandria.db",
		"https://www.youtube.com/playlist?list=PLdQw4w9WgXcQ",
		"https://www.youtube.com/channel/UCdQw4w9WgXcQ",
		"https://www.youtube.com/user/somebody",
		"https://evil.com/watch?v=dQw4w9WgXcQ",
		"https://notyoutube.com/watch?v=dQw4w9WgXcQ",
		"https://www.youtube.com.evil.com/watch?v=dQw4w9WgXcQ",
		"https://youtube.com@evil.com/watch?v=dQw4w9WgXcQ",
		"ftp://www.youtube.com/watch?v=dQw4w9WgXcQ",
		"javascript:alert(1)",
		"youtube.com/watch?v=dQw4w9WgXcQ",
		"//www.youtube.com/watch?v=dQw4w9WgXcQ",
		"file:///etc/passwd",
	}
	for _, u := range reject {
		if got, err := ExtractVideoID(u); err == nil {
			t.Errorf("%q: accepted as %q, want rejection", u, got)
		}
	}
}

func TestValidID(t *testing.T) {
	good := []string{"dQw4w9WgXcQ", "-b_c1234Zz9", "___________"}
	for _, id := range good {
		if !ValidID(id) {
			t.Errorf("%q rejected", id)
		}
	}
	bad := []string{"", "..", "../..", "dQw4w9WgXc", "dQw4w9WgXcQQ", "dQw4w9WgXc/", "dQw4w9WgXc.", "dQw4w9WgXcQ\n", "..\\..\\x.mp4"}
	for _, id := range bad {
		if ValidID(id) {
			t.Errorf("%q accepted", id)
		}
	}
}
