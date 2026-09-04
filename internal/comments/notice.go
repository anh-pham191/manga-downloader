package comments

import (
	"errors"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"io"

	"golang.org/x/image/font/opentype"
)

// RenderNotice writes a simple PNG page containing the given lines of
// text, using the same fonts and canvas width as the comments page.
// The downloader uses it to stand in for an image whose source could
// not be fetched, so the chapter can still be archived complete.
func RenderNotice(lines []string, w io.Writer) error {
	if len(lines) == 0 {
		return errors.New("RenderNotice: no lines to render")
	}
	bold, err := opentype.Parse(notoBoldTTF)
	if err != nil {
		return fmt.Errorf("bold font: %w", err)
	}
	regular, err := opentype.Parse(notoRegularTTF)
	if err != nil {
		return fmt.Errorf("regular font: %w", err)
	}

	const (
		titleSize = 36.0
		lineH     = int(bodySize * lineSpacing)
		padTop    = 120
	)
	height := padTop + int(titleSize) + 24 + lineH*(len(lines)-1) + 120
	// Manga pages are portrait; a short strip looks broken in readers.
	if height < 1400 {
		height = 1400
	}
	img := image.NewRGBA(image.Rect(0, 0, canvasWidth, height))
	draw.Draw(img, img.Bounds(), &image.Uniform{bgColor}, image.Point{}, draw.Src)

	y := padTop + int(titleSize)
	drawTextLine(img, lines[0], sideMargin, y, bold, titleSize, textColor)
	y += 24 + lineH
	for _, l := range lines[1:] {
		drawTextLine(img, l, sideMargin, y, regular, bodySize, metaColor)
		y += lineH
	}
	return png.Encode(w, img)
}
