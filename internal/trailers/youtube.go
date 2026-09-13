package trailers

import (
	"errors"
	"net/url"
	"regexp"
	"strings"
)

// videoID is YouTube's eleven-character id alphabet.
//
// It doubles as the file-name rule for the cache: every path this package
// touches is <cacheDir>/<id>.<ext> with an id that matched this, so there
// is no way to spell "..", a slash or a drive letter into it. The stream
// route checks its :id against the same rule before it looks at the disk.
var videoID = regexp.MustCompile(`^[A-Za-z0-9_-]{11}$`)

// ValidID reports whether s is a well-formed YouTube video id.
func ValidID(s string) bool { return videoID.MatchString(s) }

// ErrNotYouTube is returned for any URL this package will not fetch.
var ErrNotYouTube = errors.New("not a YouTube video URL")

// ExtractVideoID pulls the video id out of a YouTube link.
//
// This is the ONLY untrusted input the trailer routes take, and it is the
// one thing that ends up on a command line, so the rule is deliberately
// narrow: an http(s) URL on youtube.com (www., m., music.) as /watch?v=,
// /embed/ or /shorts/, or a youtu.be short link — and the id must be
// exactly eleven characters of YouTube's alphabet. Everything else is
// refused. Nothing the caller sent is ever passed on: the service
// rebuilds a canonical watch URL from the id it validated.
func ExtractVideoID(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", ErrNotYouTube
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", ErrNotYouTube
	}

	var id string
	switch strings.ToLower(u.Hostname()) {
	case "youtu.be":
		id = firstSegment(u.Path)
	case "youtube.com", "www.youtube.com", "m.youtube.com", "music.youtube.com":
		p := u.Path
		switch {
		case p == "/watch" || p == "/watch/":
			id = u.Query().Get("v")
		case strings.HasPrefix(p, "/embed/"):
			id = firstSegment(strings.TrimPrefix(p, "/embed/"))
		case strings.HasPrefix(p, "/shorts/"):
			id = firstSegment(strings.TrimPrefix(p, "/shorts/"))
		default:
			return "", ErrNotYouTube
		}
	default:
		return "", ErrNotYouTube
	}

	if !ValidID(id) {
		return "", ErrNotYouTube
	}
	return id, nil
}

// firstSegment returns the first path element of p, without slashes.
func firstSegment(p string) string {
	p = strings.TrimLeft(p, "/")
	if i := strings.IndexByte(p, '/'); i >= 0 {
		p = p[:i]
	}
	return p
}
