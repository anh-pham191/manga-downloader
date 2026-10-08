package comments

import (
	"bytes"
	"context"
	"image"
	_ "image/gif" // emotes are mostly animated GIFs; Decode keeps frame 1
	_ "image/jpeg"
	_ "image/png"
	"strings"
	"sync"

	"github.com/anhpham/downloader/internal/fetcher"
	_ "golang.org/x/image/webp"
	"golang.org/x/net/html"
)

// Site emotes are <img> tags inside the comment body. The scraper
// replaces each with one private-use rune (emoteRune(i)) so the body
// stays a plain string through NFC, wrapping and line splitting, and
// the renderer swaps the rune back for Comment.Emotes[i]. PUA runes
// are never produced by the site's text, and any that do appear are
// stripped at parse time so they can't alias an emote slot.
const (
	emoteBase = 0xE000
	emoteMax  = 0xF8FF - emoteBase + 1

	// maxEmoteSide rejects absurd images before a full decode: the
	// real emotes are 50–120px, and an <img> pointing at a full-size
	// photo would otherwise blow up memory on a 1000px-wide page.
	maxEmoteSide = 1024
)

func emoteRune(i int) rune { return rune(emoteBase + i) }

func emoteIndex(r rune) (int, bool) {
	if r < emoteBase || r >= emoteBase+emoteMax {
		return 0, false
	}
	return int(r - emoteBase), true
}

func isPUA(r rune) bool { _, ok := emoteIndex(r); return ok }

// emoteSrc picks the real image URL. Lazy-loaded emotes carry a 1×1
// data: GIF in src and the real URL in data-src. Anything that isn't
// an absolute http(s) URL can't be fetched and is dropped.
func emoteSrc(n *html.Node) string {
	var dataSrc, src string
	for _, a := range n.Attr {
		switch a.Key {
		case "data-src":
			dataSrc = strings.TrimSpace(a.Val)
		case "src":
			src = strings.TrimSpace(a.Val)
		}
	}
	for _, u := range []string{dataSrc, src} {
		if strings.HasPrefix(u, "http://") || strings.HasPrefix(u, "https://") {
			return u
		}
	}
	return ""
}

// The emote set is small and shared across every chapter of every
// manga, so a process-wide cache turns thousands of fetches during a
// sync-comments backfill into a few dozen. Failures are cached too
// (nil image) so a dead host costs one retry cycle, not one per chapter.
var (
	emoteMu    sync.Mutex
	emoteCache = map[string]image.Image{}
)

func resetEmoteCache() {
	emoteMu.Lock()
	defer emoteMu.Unlock()
	emoteCache = map[string]image.Image{}
}

// loadEmote returns the decoded first frame of the emote at u, or nil
// if it can't be fetched or decoded. An emote is decoration: no error
// here, including a 403 from the image host, may fail the chapter.
func loadEmote(ctx context.Context, u string, f fetcher.Fetcher) image.Image {
	emoteMu.Lock()
	img, ok := emoteCache[u]
	emoteMu.Unlock()
	if ok {
		return img
	}

	resp, err := f.Get(ctx, fetcher.Request{URL: u})
	if err != nil {
		if ctx.Err() != nil {
			return nil // cancelled run: don't poison the cache
		}
	} else {
		img = decodeEmote(resp.Body)
	}

	emoteMu.Lock()
	emoteCache[u] = img
	emoteMu.Unlock()
	return img
}

func decodeEmote(b []byte) image.Image {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(b))
	if err != nil || cfg.Width <= 0 || cfg.Height <= 0 ||
		cfg.Width > maxEmoteSide || cfg.Height > maxEmoteSide {
		return nil
	}
	img, _, err := image.Decode(bytes.NewReader(b))
	if err != nil {
		return nil
	}
	return img
}
