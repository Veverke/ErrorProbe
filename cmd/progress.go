package cmd

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// cmdProgress renders an animated spinner for sequential CLI operations.
//
// Usage:
//
//	prog := newCmdProgress()
//	onStatus := prog.OnStatus()
//	// pass onStatus to stack.Up / stack.Down
//	prog.Done()  // commit the final pending step
//
// Each onStatus(msg) call:
//   - suppresses messages with a leading space (image-pull sub-progress)
//   - treats "X: ready" suffix messages as inline notes on the current step
//   - otherwise ends the previous step (prints ✓ + elapsed) and starts the next
type cmdProgress struct {
	mu     sync.Mutex
	label  string
	note   string
	start  time.Time
	stopCh chan struct{}
	doneCh chan struct{}
	active bool
}

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// newCmdProgress enables ANSI/VTP on Windows and returns a ready progress helper.
func newCmdProgress() *cmdProgress {
	enableListVTP()
	return &cmdProgress{}
}

// OnStatus returns a callback suitable for stack.Up / stack.Down onStatus arg.
func (p *cmdProgress) OnStatus() func(string) {
	return func(msg string) {
		// Suppress indented sub-messages (e.g. image-pull layer progress).
		if strings.HasPrefix(msg, " ") || strings.HasPrefix(msg, "\t") {
			return
		}
		// "⚠ ..." from a non-fatal error: commit the current step as ✗ and
		// print a warning line (only the ✗ is red) without starting a new spinner.
		if strings.HasPrefix(msg, "⚠ ") {
			p.commitCurrent(true)
			fmt.Printf("  \033[91m✗\033[0m  %s\n", strings.TrimPrefix(msg, "⚠ "))
			return
		}
		// "X: ready" from PollUntilReady → update inline note, not a new step.
		if strings.HasSuffix(msg, ": ready") {
			p.mu.Lock()
			p.note = msg
			p.mu.Unlock()
			return
		}
		p.step(msg)
	}
}

// Done commits the last pending step as completed (green ✓).
func (p *cmdProgress) Done() {
	p.commitCurrent(false)
}

// DoneErr commits the last pending step: ✗ (red) if err is non-nil, else ✓ (green).
func (p *cmdProgress) DoneErr(err error) {
	p.commitCurrent(err != nil)
}

// step ends the current step (prints ✓) and starts a new one with a spinner.
func (p *cmdProgress) step(msg string) {
	p.commitCurrent(false)

	p.mu.Lock()
	p.label = msg
	p.note = ""
	p.start = time.Now()
	p.stopCh = make(chan struct{})
	p.doneCh = make(chan struct{})
	p.active = true
	p.mu.Unlock()

	fmt.Printf("  \033[93m%s\033[0m %s", spinnerFrames[0], msg)
	go p.spin()
}

// spin runs in a goroutine, animating the spinner until stopCh is closed.
func (p *cmdProgress) spin() {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	defer close(p.doneCh)

	i := 0
	for {
		select {
		case <-p.stopCh:
			return
		case <-ticker.C:
			i = (i + 1) % len(spinnerFrames)
			p.mu.Lock()
			label := p.label
			note := p.note
			p.mu.Unlock()
			if note != "" {
				fmt.Printf("\r  \033[93m%s\033[0m %-46s \033[2m[%s]\033[0m",
					spinnerFrames[i], label, note)
			} else {
				fmt.Printf("\r  \033[93m%s\033[0m %s", spinnerFrames[i], label)
			}
		}
	}
}

// commitCurrent stops the spinner and prints the completion line.
// If failed is true a red ✗ is shown; otherwise a green ✓.
func (p *cmdProgress) commitCurrent(failed bool) {
	p.mu.Lock()
	if !p.active {
		p.mu.Unlock()
		return
	}
	label := p.label
	elapsed := time.Since(p.start)
	p.active = false
	stopCh := p.stopCh
	doneCh := p.doneCh
	p.mu.Unlock()

	close(stopCh)
	<-doneCh // wait for spinner goroutine to fully exit before writing

	if failed {
		fmt.Printf("\r\033[K  \033[91m✗\033[0m %-50s \033[2m%s\033[0m\n",
			label, fmtElapsed(elapsed))
	} else {
		fmt.Printf("\r\033[K  \033[92m✓\033[0m %-50s \033[2m%s\033[0m\n",
			label, fmtElapsed(elapsed))
	}
}

// fmtElapsed returns a human-friendly elapsed duration string.
func fmtElapsed(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	return fmt.Sprintf("%.1fs", d.Seconds())
}
