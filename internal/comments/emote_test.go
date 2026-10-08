package comments

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/gif"
	"image/png"
	"net/url"
	"testing"

	"github.com/anhpham/downloader/internal/fetcher"
)

// routeFetcher serves bodies keyed by URL and counts GETs per URL.
// Unknown URLs fail like a dead image host.
type routeFetcher struct {
	routes map[string][]byte
	gets   map[string]int
}

func (f *routeFetcher) Get(_ context.Context, req fetcher.Request) (*fetcher.Response, error) {
	if f.gets == nil {
		f.gets = map[string]int{}
	}
	f.gets[req.URL]++
	b, ok := f.routes[req.URL]
	if !ok {
		return nil, errors.New("unexpected status 404")
	}
	return &fetcher.Response{Body: b}, nil
}
func (f *routeFetcher) Post(_ context.Context, _ fetcher.Request, _ url.Values) (*fetcher.Response, error) {
	return &fetcher.Response{}, nil
}

func solidGIF(t *testing.T, c color.Color, w, h int) []byte {
	t.Helper()
	pal := image.NewPaletted(image.Rect(0, 0, w, h), color.Palette{color.White, c})
	for i := range pal.Pix {
		pal.Pix[i] = 1
	}
	var buf bytes.Buffer
	if err := gif.Encode(&buf, pal, nil); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func emoteChapter(bodies ...string) []byte {
	s := `<html><body><div id="comment_list">`
	for _, b := range bodies {
		s += `<article class="info-comment comment-main-level child_1 parent_0">
      <strong class="level name_5">user</strong>
      <div class="content-comment">` + b + `</div>
    </article>`
	}
	return []byte(s + `</div></body></html>`)
}

const chapURL = "https://example.com/chap-1"

func TestScrape_EmoteBecomesInlineImage(t *testing.T) {
	resetEmoteCache()
	f := &routeFetcher{routes: map[string][]byte{
		chapURL:                         emoteChapter(`hello <img class="lazy-image" src="data:image/gif;base64,R0lGODlhAQABAAAAACw=" data-src="https://img.example.com/a.gif" alt="emo"/> world`),
		"https://img.example.com/a.gif": solidGIF(t, color.RGBA{0xff, 0, 0, 0xff}, 50, 50),
	}}
	cs, err := Scrape(context.Background(), chapURL, f)
	if err != nil {
		t.Fatal(err)
	}
	if len(cs) != 1 {
		t.Fatalf("len = %d", len(cs))
	}
	if want := "hello " + string(emoteRune(0)) + " world"; cs[0].Body != want {
		t.Errorf("Body = %q, want %q", cs[0].Body, want)
	}
	if len(cs[0].Emotes) != 1 || cs[0].Emotes[0] == nil {
		t.Fatalf("Emotes = %v, want one decoded image", cs[0].Emotes)
	}
	if b := cs[0].Emotes[0].Bounds(); b.Dx() != 50 || b.Dy() != 50 {
		t.Errorf("emote bounds = %v", b)
	}
}

func TestScrape_EmoteFetchedOncePerURL(t *testing.T) {
	resetEmoteCache()
	img := `<img class="lazy-image" data-src="https://img.example.com/a.gif" alt="emo"/>`
	f := &routeFetcher{routes: map[string][]byte{
		chapURL:                         emoteChapter(img+img, img),
		"https://img.example.com/a.gif": solidGIF(t, color.Black, 10, 10),
	}}
	if _, err := Scrape(context.Background(), chapURL, f); err != nil {
		t.Fatal(err)
	}
	if _, err := Scrape(context.Background(), chapURL, f); err != nil {
		t.Fatal(err)
	}
	if n := f.gets["https://img.example.com/a.gif"]; n != 1 {
		t.Errorf("emote fetched %d times, want 1 across comments and chapters", n)
	}
}

func TestScrape_DeadEmoteDoesNotFailComments(t *testing.T) {
	resetEmoteCache()
	f := &routeFetcher{routes: map[string][]byte{
		chapURL: emoteChapter(`hi <img class="lazy-image" data-src="https://img.example.com/gone.gif" alt="emo"/>`),
	}}
	cs, err := Scrape(context.Background(), chapURL, f)
	if err != nil {
		t.Fatal(err)
	}
	if len(cs) != 1 || len(cs[0].Emotes) != 1 || cs[0].Emotes[0] != nil {
		t.Fatalf("want one comment with a nil emote slot, got %+v", cs)
	}
}

func TestScrape_NonEmoteImagesAndStrayPUAIgnored(t *testing.T) {
	resetEmoteCache()
	f := &routeFetcher{routes: map[string][]byte{
		// An <img> with no data-src/src URL carries nothing to fetch, and a
		// literal private-use rune in the text must not alias an emote slot.
		chapURL: emoteChapter("a <img alt=\"emo\"/> b c"),
	}}
	cs, err := Scrape(context.Background(), chapURL, f)
	if err != nil {
		t.Fatal(err)
	}
	if cs[0].Body != "a  b c" || len(cs[0].Emotes) != 0 {
		t.Errorf("Body = %q, Emotes = %d", cs[0].Body, len(cs[0].Emotes))
	}
}

func TestRender_EmoteCompositesPixelsAndGrowsLine(t *testing.T) {
	red := image.NewRGBA(image.Rect(0, 0, 50, 50))
	for i := 0; i < len(red.Pix); i += 4 {
		red.Pix[i], red.Pix[i+3] = 0xff, 0xff
	}
	plain := []Comment{{Name: "n", Body: "look"}}
	withEmote := []Comment{{Name: "n", Body: "look " + string(emoteRune(0)), Emotes: []image.Image{red}}}

	render := func(cs []Comment) image.Image {
		var buf bytes.Buffer
		if err := Render(cs, &buf); err != nil {
			t.Fatal(err)
		}
		img, err := png.Decode(&buf)
		if err != nil {
			t.Fatal(err)
		}
		return img
	}
	a, b := render(plain), render(withEmote)
	if b.Bounds().Dy() <= a.Bounds().Dy() {
		t.Errorf("emote line height %d not taller than text-only %d", b.Bounds().Dy(), a.Bounds().Dy())
	}
	found := false
	for y := headerHeight; y < b.Bounds().Dy() && !found; y++ {
		for x := 0; x < b.Bounds().Dx() && !found; x++ {
			r, g, bl, _ := b.At(x, y).RGBA()
			found = r > 0xf000 && g < 0x1000 && bl < 0x1000
		}
	}
	if !found {
		t.Fatal("no red emote pixel in body region")
	}
}

func TestRender_NilEmoteSlotRendersNothing(t *testing.T) {
	cs := []Comment{{Name: "n", Body: string(emoteRune(0)), Emotes: []image.Image{nil}}}
	var buf bytes.Buffer
	if err := Render(cs, &buf); err != nil {
		t.Fatal(err)
	}
	img, _ := png.Decode(&buf)
	// The body row must stay blank: no tofu box for the placeholder.
	if hasNonWhitePixels(img, sideMargin, headerHeight+50, sideMargin+60, headerHeight+80) {
		t.Fatal("nil emote slot drew something")
	}
}
