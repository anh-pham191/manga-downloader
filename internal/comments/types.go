package comments

import "image"

// Comment is one parent-level reader comment.
type Comment struct {
	Name  string // Username, e.g. "sukuna"
	Level string // Level chip text, e.g. "Giới Chủ" — may be empty
	// Body is the NFC-normalised plain text. Each emote <img> is
	// replaced by the private-use rune emoteRune(i), which indexes
	// Emotes.
	Body      string
	LikeCount int
	// Emotes holds the still (first-frame) emote images referenced by
	// Body. A nil entry is an emote that couldn't be fetched; the
	// renderer leaves its slot empty.
	Emotes []image.Image

	// ReplyTo is the name a reply answers (the site's "@name"); empty
	// on top-level comments.
	ReplyTo string
	// Replies are the thread under a top-level comment, oldest first.
	// Always empty on a reply: the site threads replies one level deep.
	Replies []Comment

	emoteURLs  []string // parse → fetch hand-off inside Scrape
	id         string   // site comment id, from class child_<id>
	replyCount int      // from the "N phản hồi" expander; 0 = no thread
}
