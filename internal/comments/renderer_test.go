package comments

import (
	"bytes"
	"image"
	"image/png"
	"testing"
)

func TestRender_BasicShape(t *testing.T) {
	cs := []Comment{
		{Name: "sukuna", Level: "Giới Chủ", Body: "Hay quá!", LikeCount: 5},
		{Name: "Ann", Level: "Cấp 7", Body: "Tuyệt vời, mình rất thích chương này.", LikeCount: 0},
	}
	var buf bytes.Buffer
	if err := Render(cs, &buf); err != nil {
		t.Fatalf("Render: %v", err)
	}
	img, err := png.Decode(&buf)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	b := img.Bounds()
	if b.Dx() != 1000 {
		t.Errorf("width = %d, want 1000", b.Dx())
	}
	if b.Dy() < 200 {
		t.Errorf("height = %d, want >= 200", b.Dy())
	}
}

func TestRender_EmptyCommentsNoOp(t *testing.T) {
	var buf bytes.Buffer
	if err := Render(nil, &buf); err == nil {
		t.Fatal("Render(nil) should error — callers must not invoke on empty input")
	}
}

func TestRender_VietnameseShapes(t *testing.T) {
	cs := []Comment{{Name: "n", Body: "Tiếng Việt: ế ề ệ á à"}}
	var buf bytes.Buffer
	if err := Render(cs, &buf); err != nil {
		t.Fatal(err)
	}
	img, _ := png.Decode(&buf)
	if !hasNonWhitePixels(img, 30, 60, 400, 100) {
		t.Fatal("no rendered text in expected region")
	}
}

func hasNonWhitePixels(img image.Image, x0, y0, x1, y1 int) bool {
	for y := y0; y < y1; y++ {
		for x := x0; x < x1; x++ {
			r, g, b, _ := img.At(x, y).RGBA()
			if r < 0xff00 || g < 0xff00 || b < 0xff00 {
				return true
			}
		}
	}
	return false
}
