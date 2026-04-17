package widget

import "time"

// SpinnerFrames is the braille dot sequence used by RunSpinner. The
// preferred palette for long-running status indicators in sqlgo.
var SpinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// SpinnerInterval is the per-frame delay. ~10fps feels lively without
// hogging a CPU core on redraws.
const SpinnerInterval = 100 * time.Millisecond

// RunSpinner ticks SpinnerFrames until done is closed, calling post
// with the current frame glyph on every tick. post is the caller's
// hook for splicing the frame into whatever status surface it owns --
// typically a closure that enqueues the frame via the app's async
// channel so all UI mutation stays on the main goroutine.
//
// The caller owns post's backpressure behavior. Typical pattern is a
// non-blocking send (drop frames under load rather than stall the
// spinner goroutine or delay the final "done" update).
func RunSpinner(done <-chan struct{}, post func(frame string)) {
	t := time.NewTicker(SpinnerInterval)
	defer t.Stop()
	i := 0
	for {
		select {
		case <-done:
			return
		case <-t.C:
			i++
			post(SpinnerFrames[i%len(SpinnerFrames)])
		}
	}
}
