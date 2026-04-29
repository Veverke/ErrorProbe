package ingest

import (
	"encoding/json"
	"fmt"
	"time"
)

// LogEvent is the normalised schema coming from Vector's VRL transform.
type LogEvent struct {
	Timestamp time.Time `json:"timestamp"`
	Container string    `json:"container"`
	Level     string    `json:"level"`
	Message   string    `json:"message"`
	Raw       string    `json:"raw"`
}

// ParseBatch parses a JSON array of LogEvent from data.
// Missing fields default to their zero values; malformed JSON returns an error.
func ParseBatch(data []byte) ([]LogEvent, error) {
	var events []LogEvent
	if err := json.Unmarshal(data, &events); err != nil {
		return nil, fmt.Errorf("parsing log batch: %w", err)
	}
	return events, nil
}
