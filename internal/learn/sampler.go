package learn

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/errorprobe/errorprobe/internal/ingest"
	"github.com/errorprobe/errorprobe/internal/loki"
)

// LogQuerier is the minimal Loki interface required by the Sampler.
// *loki.Client satisfies this interface automatically.
type LogQuerier interface {
	QueryRange(ctx context.Context, query string, start, end time.Time, limit int, direction string) ([]loki.LogLine, error)
}

// Sampler wraps a Loki client and provides higher-level log-sampling helpers
// used by the learning pipeline.
type Sampler struct {
	loki LogQuerier
}

// NewSampler creates a Sampler backed by q.
func NewSampler(q LogQuerier) *Sampler {
	return &Sampler{loki: q}
}

// QueryWindow queries all log lines for containerKey between start and end.
// containerKey follows the HealthKey convention: bare name for Docker,
// "namespace/container" for K8s.
// Returns up to limit lines in chronological order.
func (s *Sampler) QueryWindow(ctx context.Context, containerKey string, start, end time.Time, limit int) ([]ingest.LogEvent, error) {
	query := buildContainerQuery(containerKey)
	lines, err := s.loki.QueryRange(ctx, query, start, end, limit, "forward")
	if err != nil {
		return nil, fmt.Errorf("sampler QueryWindow: %w", err)
	}
	return linesToEvents(lines, containerKey), nil
}

// QueryPreRestart returns log lines from the window immediately preceding a
// restart event. It queries the [at-window, at] range so that crash-causing
// log lines are captured before the container restarted.
func (s *Sampler) QueryPreRestart(ctx context.Context, containerKey string, at time.Time, window time.Duration, limit int) ([]ingest.LogEvent, error) {
	start := at.Add(-window)
	return s.QueryWindow(ctx, containerKey, start, at, limit)
}

// SampleWindows divides [start, end] into windowCount equal sub-windows and
// queries each one independently. It returns:
//   - all events concatenated across all windows
//   - a map from raw message text to the number of windows in which it appeared
func (s *Sampler) SampleWindows(ctx context.Context, containerKey string, start, end time.Time, windowCount int, limitPerWindow int) ([]ingest.LogEvent, map[string]int, error) {
	if windowCount <= 0 {
		windowCount = 1
	}
	total := end.Sub(start)
	step := total / time.Duration(windowCount)

	var allEvents []ingest.LogEvent
	windowHits := make(map[string]int)

	for i := 0; i < windowCount; i++ {
		wStart := start.Add(step * time.Duration(i))
		wEnd := wStart.Add(step)
		events, err := s.QueryWindow(ctx, containerKey, wStart, wEnd, limitPerWindow)
		if err != nil {
			// Log query errors are non-fatal: skip this window.
			continue
		}
		seen := make(map[string]struct{}, len(events))
		for _, ev := range events {
			allEvents = append(allEvents, ev)
			if _, already := seen[ev.Message]; !already {
				windowHits[ev.Message]++
				seen[ev.Message] = struct{}{}
			}
		}
	}

	return allEvents, windowHits, nil
}

// buildContainerQuery returns a LogQL stream selector for the given container key.
// containerKey: "namespace/container" → filter by both labels;
// bare name      → filter by container label only.
func buildContainerQuery(containerKey string) string {
	if idx := strings.Index(containerKey, "/"); idx >= 0 {
		ns := containerKey[:idx]
		name := containerKey[idx+1:]
		return fmt.Sprintf(`{container=%q,namespace=%q}`, name, ns)
	}
	return fmt.Sprintf(`{container=%q}`, containerKey)
}

// linesToEvents converts Loki LogLine results to ingest.LogEvent values.
func linesToEvents(lines []loki.LogLine, containerKey string) []ingest.LogEvent {
	events := make([]ingest.LogEvent, 0, len(lines))
	var container, namespace string
	if idx := strings.Index(containerKey, "/"); idx >= 0 {
		namespace = containerKey[:idx]
		container = containerKey[idx+1:]
	} else {
		container = containerKey
	}
	for _, l := range lines {
		name := l.Container
		if name == "" {
			name = container
		}
		events = append(events, ingest.LogEvent{
			Timestamp: l.Timestamp,
			Container: name,
			Namespace: namespace,
			Level:     l.Level,
			Message:   l.Message,
			Runtime:   "unknown",
		})
	}
	return events
}
