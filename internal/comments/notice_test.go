package comments

import (
	"bytes"
	"image/png"
	"testing"
)

func TestRenderNotice_ProducesDecodablePNG(t *testing.T) {
	var buf bytes.Buffer
	if err := RenderNotice([]string{"Image 21 missing", "host: i59.tinypic.com"}, &buf); err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(&buf)
	if err != nil {
		t.Fatalf("output is not a PNG: %v", err)
	}
	if img.Bounds().Dx() != canvasWidth || img.Bounds().Dy() < 100 {
		t.Fatalf("unexpected bounds %v", img.Bounds())
	}
}

func TestRenderNotice_RejectsEmpty(t *testing.T) {
	if err := RenderNotice(nil, &bytes.Buffer{}); err == nil {
		t.Fatal("expected error for no lines")
	}
}
