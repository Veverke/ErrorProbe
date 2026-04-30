package ingest

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseBatch_ValidArray(t *testing.T) {
	data := []byte(`[
		{"timestamp":"2024-01-01T12:00:00Z","container":"api","level":"error","message":"boom","raw":"ERROR boom"},
		{"timestamp":"2024-01-01T12:00:01Z","container":"db","level":"warn","message":"slow","raw":"WARN slow"}
	]`)

	events, err := ParseBatch(data)
	require.NoError(t, err)
	require.Len(t, events, 2)

	assert.Equal(t, "api", events[0].Container)
	assert.Equal(t, "error", events[0].Level)
	assert.Equal(t, "boom", events[0].Message)
	assert.Equal(t, "ERROR boom", events[0].Raw)

	assert.Equal(t, "db", events[1].Container)
	assert.Equal(t, "warn", events[1].Level)
}

func TestParseBatch_SingleEvent(t *testing.T) {
	data := []byte(`[{"timestamp":"2024-06-01T10:00:00Z","container":"svc","level":"error","message":"oops","raw":"ERROR oops"}]`)

	events, err := ParseBatch(data)
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, "svc", events[0].Container)

	expected := time.Date(2024, 6, 1, 10, 0, 0, 0, time.UTC)
	assert.True(t, events[0].Timestamp.Equal(expected))
}

func TestParseBatch_MalformedJSON(t *testing.T) {
	_, err := ParseBatch([]byte("{not json}"))
	assert.Error(t, err)
}

func TestParseBatch_MissingFields(t *testing.T) {
	// An event with no "level" field: should parse without panic, defaulting to empty string.
	data := []byte(`[{"container":"svc","message":"hello"}]`)

	events, err := ParseBatch(data)
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, "svc", events[0].Container)
	assert.Equal(t, "", events[0].Level, "missing level should default to empty string")
}
