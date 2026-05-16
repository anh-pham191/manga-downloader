package comments

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"io"
	"strings"
	"unicode/utf8"

	gtfont "github.com/go-text/typesetting/font"
	"github.com/go-text/typesetting/di"
	"github.com/go-text/typesetting/language"
	"github.com/go-text/typesetting/shaping"
	"github.com/rivo/uniseg"
	xfont "golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

const (
	canvasWidth   = 1000
	sideMargin    = 32
	contentWidth  = canvasWidth - 2*sideMargin
	headerHeight  = 80
	commentPadTop = 16
	commentPadBot = 16
	sepHeight     = 1
	bodySize      = 20.0
	nameSize      = 24.0
	lineSpacing   = 1.4
)

var (
	bgColor   = color.RGBA{0xff, 0xff, 0xff, 0xff}
	textColor = color.RGBA{0x22, 0x22, 0x22, 0xff}
	metaColor = color.RGBA{0x88, 0x88, 0x88, 0xff}
	sepColor  = color.RGBA{0xdd, 0xdd, 0xdd, 0xff}
)

// Render writes a PNG comment page to w. Callers should not invoke
// Render on an empty []Comment — the function returns an error in
// that case so a chapter with no comments produces no file.
func Render(cs []Comment, w io.Writer) error {
	if len(cs) == 0 {
		return errors.New("Render: no comments to render")
	}
	regular, err := opentype.Parse(notoRegularTTF)
	if err != nil {
		return fmt.Errorf("regular font: %w", err)
	}
	bold, err := opentype.Parse(notoBoldTTF)
	if err != nil {
		return fmt.Errorf("bold font: %w", err)
	}

	// Build go-text fonts for shaping (separate from the opentype.Font
	// used for rasterising). gtfont.ParseTTF returns a *gtfont.Face
	// satisfying the Font interface go-text expects.
	gtRegular, err := gtfont.ParseTTF(bytes.NewReader(notoRegularTTF))
	if err != nil {
		return fmt.Errorf("gt regular: %w", err)
	}

	// 1. Measure each comment by laying out its body.
	blocks := make([]commentBlock, len(cs))
	for i, c := range cs {
		blocks[i] = layoutComment(c, regular, gtRegular)
	}

	// 2. Sum heights, allocate canvas, fill background.
	totalHeight := headerHeight
	for _, b := range blocks {
		totalHeight += b.height
	}
	img := image.NewRGBA(image.Rect(0, 0, canvasWidth, totalHeight))
	draw.Draw(img, img.Bounds(), &image.Uniform{bgColor}, image.Point{}, draw.Src)

	// 3. Header and blocks.
	drawHeader(img, len(cs), bold)
	y := headerHeight
	for _, b := range blocks {
		drawComment(img, b, y, regular, bold)
		y += b.height
	}
	return png.Encode(w, img)
}

type commentBlock struct {
	comment   Comment
	bodyLines []string // already wrapped to contentWidth
	height    int
}

func layoutComment(c Comment, regular *opentype.Font, gtRegular *gtfont.Face) commentBlock {
	var nameRowHeightF float64 = nameSize * lineSpacing
	nameRowHeight := int(nameRowHeightF)
	lines := wrapBody(c.Body, gtRegular, bodySize, contentWidth)
	lineCount := len(lines)
	if lineCount == 0 {
		lineCount = 1
	}
	bodyHeight := int(bodySize*lineSpacing) * lineCount
	return commentBlock{
		comment:   c,
		bodyLines: lines,
		height:    commentPadTop + nameRowHeight + bodyHeight + commentPadBot + sepHeight,
	}
}

// shapeString shapes one short string and returns the total advance
// in fixed-point pixels. Used for wrapping/measuring only.
func shapeString(text string, gtFace *gtfont.Face, size float64) fixed.Int26_6 {
	runes := []rune(text)
	if len(runes) == 0 {
		return 0
	}

	var shaper shaping.HarfbuzzShaper
	inp := shaping.Input{
		Text:      runes,
		RunStart:  0,
		RunEnd:    len(runes),
		Direction: di.DirectionLTR,
		Face:      gtFace,
		Size:      fixed.I(int(size)),
		Script:    language.Latin,
		Language:  language.NewLanguage("vi"),
	}
	out := shaper.Shape(inp)
	return out.Advance
}

func wrapBody(body string, gtRegular *gtfont.Face, size float64, maxWidth int) []string {
	if body == "" {
		return nil
	}
	var out []string
	for _, paragraph := range strings.Split(body, "\n") {
		para := strings.TrimSpace(paragraph)
		if para == "" {
			continue
		}
		out = append(out, wrapParagraph(para, gtRegular, size, maxWidth)...)
	}
	return out
}

func wrapParagraph(text string, gtFace *gtfont.Face, size float64, maxWidth int) []string {
	g := uniseg.NewGraphemes(text)
	var clusters []string
	for g.Next() {
		clusters = append(clusters, g.Str())
	}

	var lines []string
	var current []string
	maxFixed := fixed.I(maxWidth)
	for _, cluster := range clusters {
		candidate := strings.Join(append(append([]string{}, current...), cluster), "")
		adv := shapeString(candidate, gtFace, size)
		if adv > maxFixed && len(current) > 0 {
			lines = append(lines, strings.Join(current, ""))
			current = []string{cluster}
		} else {
			current = append(current, cluster)
		}
	}
	if len(current) > 0 {
		lines = append(lines, strings.Join(current, ""))
	}
	return lines
}

func drawHeader(img *image.RGBA, n int, bold *opentype.Font) {
	title := fmt.Sprintf("Bình Luận (%d)", n)
	drawTextLine(img, title, sideMargin, headerHeight/2+10, bold, nameSize, textColor)
	drawHLine(img, headerHeight-1, sepColor)
}

func drawComment(img *image.RGBA, b commentBlock, y int, regular, bold *opentype.Font) {
	cy := y + commentPadTop
	drawTextLine(img, b.comment.Name, sideMargin, cy+20, bold, nameSize, textColor)
	if b.comment.Level != "" {
		drawTextLine(img, "· "+b.comment.Level, sideMargin+220, cy+20, regular, 16, metaColor)
	}
	if b.comment.LikeCount > 0 {
		like := fmt.Sprintf("♥ %d", b.comment.LikeCount)
		drawTextLine(img, like, canvasWidth-sideMargin-80, cy+20, regular, 16, metaColor)
	}
	var nameLineAdvanceF float64 = nameSize * lineSpacing
	cy += int(nameLineAdvanceF)

	for _, line := range b.bodyLines {
		drawTextLine(img, line, sideMargin, cy+int(bodySize), regular, bodySize, textColor)
		cy += int(bodySize * lineSpacing)
	}

	drawHLine(img, y+b.height-1, sepColor)
}

// emojiKey builds the Twemoji filename key from a grapheme cluster:
// e.g. "👨‍👩‍👧" → "1f468-200d-1f469-200d-1f467". FE0F variation
// selectors are stripped because Twemoji filenames don't include them.
func emojiKey(cluster string) string {
	var parts []string
	for _, r := range cluster {
		if r == 0xFE0F {
			continue
		}
		parts = append(parts, fmt.Sprintf("%x", r))
	}
	return strings.Join(parts, "-")
}

func decodeTwemoji(seq string) (image.Image, bool) {
	raw, ok := twemojiPNG(seq)
	if !ok {
		return nil, false
	}
	img, err := png.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, false
	}
	return img, true
}

// looksLikeEmoji is intentionally conservative: it only triggers on
// grapheme clusters whose leading rune is in the supplementary
// plane emoji range. ASCII and Latin (including Vietnamese composed
// forms) are left to the text path. ♥ (U+2665) is below the
// threshold and renders as text — that's intentional for the
// like-count display.
func looksLikeEmoji(cluster string) bool {
	r, _ := utf8.DecodeRuneInString(cluster)
	return r >= 0x1F000
}

// scaleImage is a tiny nearest-neighbour scaler — good enough for
// dropping a 72×72 Twemoji into a 20×20 body-text slot.
func scaleImage(src image.Image, w, h int) *image.RGBA {
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	sb := src.Bounds()
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			sx := sb.Min.X + x*sb.Dx()/w
			sy := sb.Min.Y + y*sb.Dy()/h
			dst.Set(x, y, src.At(sx, sy))
		}
	}
	return dst
}

// drawTextLine draws one line of text at (x, y). Emoji grapheme clusters
// whose leading rune is >= U+1F000 are composited as Twemoji PNGs inline.
func drawTextLine(img *image.RGBA, text string, x, y int, f *opentype.Font, size float64, col color.Color) {
	face, err := opentype.NewFace(f, &opentype.FaceOptions{Size: size, DPI: 72})
	if err != nil {
		return
	}
	defer face.Close()

	if !strings.ContainsFunc(text, func(r rune) bool { return r >= 0x1F000 }) {
		// Fast path: no emoji in the line.
		(&xfont.Drawer{
			Dst:  img,
			Src:  &image.Uniform{col},
			Face: face,
			Dot:  fixed.P(x, y),
		}).DrawString(text)
		return
	}

	cx := fixed.I(x)
	g := uniseg.NewGraphemes(text)
	for g.Next() {
		cluster := g.Str()
		if !looksLikeEmoji(cluster) {
			d := &xfont.Drawer{
				Dst:  img,
				Src:  &image.Uniform{col},
				Face: face,
				Dot:  fixed.Point26_6{X: cx, Y: fixed.I(y)},
			}
			d.DrawString(cluster)
			cx = d.Dot.X
			continue
		}
		seq := emojiKey(cluster)
		ei, ok := decodeTwemoji(seq)
		if !ok {
			// Twemoji bundle has no match — draw the cluster as
			// text (likely tofu). Accepted per the spec's "emoji
			// are best-effort" rule.
			d := &xfont.Drawer{
				Dst:  img,
				Src:  &image.Uniform{col},
				Face: face,
				Dot:  fixed.Point26_6{X: cx, Y: fixed.I(y)},
			}
			d.DrawString(cluster)
			cx = d.Dot.X
			continue
		}
		// Compose the 72×72 emoji PNG at body-text size into the
		// image, vertically aligned to the text baseline.
		target := int(size)
		scaled := scaleImage(ei, target, target)
		rect := image.Rect(cx.Round(), y-target+2, cx.Round()+target, y+2)
		draw.Draw(img, rect, scaled, image.Point{}, draw.Over)
		cx += fixed.I(target + 2)
	}
}

func drawHLine(img *image.RGBA, y int, col color.Color) {
	if y < 0 || y >= img.Bounds().Dy() {
		return
	}
	for x := 0; x < img.Bounds().Dx(); x++ {
		img.Set(x, y, col)
	}
}
