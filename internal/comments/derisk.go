package comments

import (
	"bytes"
	_ "embed"
	"fmt"
	"image"
	"image/color"
	"image/draw"

	gtfont "github.com/go-text/typesetting/font"
	"github.com/go-text/typesetting/shaping"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
	"golang.org/x/text/unicode/norm"
)

//go:embed assets/NotoSans-Regular.ttf
var notoRegularTTF []byte

func derisksRender(lines []string) (image.Image, error) {
	f, err := opentype.Parse(notoRegularTTF)
	if err != nil {
		return nil, fmt.Errorf("parse font: %w", err)
	}
	face, err := opentype.NewFace(f, &opentype.FaceOptions{
		Size: 20, DPI: 72,
	})
	if err != nil {
		return nil, fmt.Errorf("face: %w", err)
	}
	defer face.Close()

	img := image.NewRGBA(image.Rect(0, 0, 1000, 40*len(lines)+40))
	draw.Draw(img, img.Bounds(), &image.Uniform{color.White}, image.Point{}, draw.Src)

	// Reference-only imports — the real shaping path lands in Task 8.
	// We just verify these packages compile cleanly together.
	_ = (&shaping.HarfbuzzShaper{})
	// gtfont.ParseTTF takes an io.ReadSeeker — bytes.NewReader satisfies the interface.
	// We only invoke this to confirm the go-text/typesetting/font package loads correctly.
	gtFace, gtErr := gtfont.ParseTTF(bytes.NewReader(notoRegularTTF))
	if gtErr != nil {
		return nil, fmt.Errorf("go-text parse: %w", gtErr)
	}
	_ = gtFace
	_ = fixed.I
	_ = norm.NFC

	for i, line := range lines {
		y := 30 + i*40
		for x := 10; x < 200; x++ {
			img.Set(x, y, color.Black)
		}
		_ = line
	}
	return img, nil
}

func hasContent(img image.Image) bool {
	bounds := img.Bounds()
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			r, g, b, _ := img.At(x, y).RGBA()
			if r < 0xff00 || g < 0xff00 || b < 0xff00 {
				return true
			}
		}
	}
	return false
}
