package loki

import (
	"context"
	"fmt"
	"io"
	"time"
)

// Tail polls Loki's query_range endpoint and writes new log lines to out.
// It advances the start timestamp after each batch to avoid printing duplicates,
// and returns nil when ctx is cancelled.
func (c *Client) Tail(ctx context.Context, query string, since time.Duration, out io.Writer) error {
	start := time.Now().Add(-since)

	poll := func() error {
		end := time.Now()
		lines, err := c.QueryRange(ctx, query, start, end, 1000, "forward")
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("polling Loki: %w", err)
		}
		for _, l := range lines {
			fmt.Fprintln(out, l.Line)
		}
		if len(lines) > 0 {
			// Advance past the last seen timestamp to avoid reprinting on the next poll.
			start = lines[len(lines)-1].Timestamp.Add(time.Nanosecond)
		}
		return nil
	}

	// First poll immediately so the user sees existing lines without waiting.
	if err := poll(); err != nil {
		return err
	}

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := poll(); err != nil {
				return err
			}
		}
	}
}
