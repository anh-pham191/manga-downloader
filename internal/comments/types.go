package comments

// Comment is one parent-level reader comment.
type Comment struct {
	Name      string // Username, e.g. "sukuna"
	Level     string // Level chip text, e.g. "Giới Chủ" — may be empty
	Body      string // Plain text body. Legacy <img class="lazy-image"> emote
	// images are stripped. NFC-normalised.
	LikeCount int
}
