package comments

import "embed"

//go:embed assets/NotoSans-Regular.ttf
var notoRegularTTF []byte

//go:embed assets/NotoSans-Bold.ttf
var notoBoldTTF []byte

//go:embed assets/twemoji/*.png
var twemojiFS embed.FS

func twemojiPNG(seq string) ([]byte, bool) {
	raw, err := twemojiFS.ReadFile("assets/twemoji/" + seq + ".png")
	if err != nil {
		return nil, false
	}
	return raw, true
}
