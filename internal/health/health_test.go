package health

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var t0 = time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)
var t1 = time.Date(2024, 1, 1, 12, 0, 1, 0, time.UTC)

func TestSetError_FlipsState(t *testing.T) {
	snap := HealthSnapshot{}
	snap.SetError("svc", "oh no", t0)
	require.Contains(t, snap.Containers, "svc")
	assert.Equal(t, StateHasErrors, snap.Containers["svc"].State)
}

func TestSetError_IncrementsCount(t *testing.T) {
	snap := HealthSnapshot{}
	snap.SetError("svc", "first", t0)
	snap.SetError("svc", "second", t1)
	assert.Equal(t, 2, snap.Containers["svc"].ErrorCount)
}

func TestSetError_TracksFirstAndLast(t *testing.T) {
	snap := HealthSnapshot{}
	snap.SetError("svc", "first", t0)
	snap.SetError("svc", "second", t1)
	ch := snap.Containers["svc"]
	require.NotNil(t, ch.FirstErrorAt)
	require.NotNil(t, ch.LastErrorAt)
	assert.True(t, ch.FirstErrorAt.Equal(t0), "FirstErrorAt should be t0")
	assert.True(t, ch.LastErrorAt.Equal(t1), "LastErrorAt should be t1")
}

func TestSetError_PreservesFirstError(t *testing.T) {
	snap := HealthSnapshot{}
	snap.SetError("svc", "first", t0)
	firstAt := *snap.Containers["svc"].FirstErrorAt

	snap.SetError("svc", "second", t1)
	assert.True(t, snap.Containers["svc"].FirstErrorAt.Equal(firstAt), "FirstErrorAt must not change on second SetError")
}

func TestReset_ClearsState(t *testing.T) {
	snap := HealthSnapshot{}
	snap.SetError("svc", "boom", t0)
	snap.SetError("svc", "boom2", t1)

	snap.Reset("svc")
	ch := snap.Containers["svc"]
	assert.Equal(t, StateOK, ch.State)
	assert.Equal(t, 0, ch.ErrorCount)
	assert.Nil(t, ch.FirstErrorAt)
	assert.Nil(t, ch.LastErrorAt)
}
