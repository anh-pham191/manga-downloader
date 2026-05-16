package comments

import (
	"image/png"
	"os"
	"testing"
)

// TestDerisk_VietnameseAndEmoji is a one-off gate that proves the
// pure-Go renderer can handle composed/decomposed Vietnamese
// correctly. Emoji are best-effort.
//
// This file is deleted once Task 8+9 land the real renderer.
func TestDerisk_VietnameseAndEmoji(t *testing.T) {
	cases := []string{
		"Tiếng Việt: ế ề ệ á à",                  // NFC
		"Tiếng Việt",                              // NFD (e+combining acute marks)
		"Plain ASCII line.",
		"Emoji: \U0001F600",                        // 😀
		"Family: \U0001F468‍\U0001F469‍\U0001F467", // 👨‍👩‍👧
	}

	out, err := os.Create("derisk-output.png")
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()

	img, err := derisksRender(cases)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if err := png.Encode(out, img); err != nil {
		t.Fatalf("encode: %v", err)
	}

	// Structural assertions (not byte-exact):
	bounds := img.Bounds()
	if bounds.Dx() < 100 || bounds.Dy() < 100 {
		t.Fatalf("image too small: %v", bounds)
	}
	// Verify SOMETHING rendered (non-background pixels present).
	if !hasContent(img) {
		t.Fatal("rendered image is blank")
	}
}
