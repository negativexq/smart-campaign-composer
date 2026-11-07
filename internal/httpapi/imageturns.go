package httpapi

import (
	"context"
	"time"
)

// An image costs far more memory while it is drawn than its file suggests: a 24
// megapixel PNG of one colour is a 0.1 MB upload and takes about 300 MB to decode
// and redraw, 400 MB at 16 bits a channel. Uploads take turns, so a burst of them
// costs a few images' worth of memory instead of one for each request.
const (
	// imagesAtOnce is how many uploads are read and drawn at the same time.
	imagesAtOnce = 2
	// imageTurnWait is how long an upload waits for its turn before it is refused,
	// so a burst is answered instead of queueing past every client's timeout.
	imageTurnWait = 10 * time.Second
	// imageRetryAfter is the number of seconds a refused upload is told to wait.
	imageRetryAfter = "5"
)

// imageTurns hands out turns to draw an image.
type imageTurns struct {
	slots chan struct{}
	wait  time.Duration
}

func newImageTurns(atOnce int, wait time.Duration) *imageTurns {
	return &imageTurns{slots: make(chan struct{}, atOnce), wait: wait}
}

// take waits for a turn, for at most the turn wait and only while ctx lasts. It
// says whether the caller got one; a caller that did gives it back with give.
func (t *imageTurns) take(ctx context.Context) bool {
	select {
	case t.slots <- struct{}{}:
		return true
	default:
	}
	timer := time.NewTimer(t.wait)
	defer timer.Stop()
	select {
	case t.slots <- struct{}{}:
		return true
	case <-timer.C:
		return false
	case <-ctx.Done():
		return false
	}
}

func (t *imageTurns) give() { <-t.slots }
