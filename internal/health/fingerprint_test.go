package health

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFingerprint_TimestampStripped(t *testing.T) {
	msg1 := "2024-01-15T10:30:00Z connection refused to db"
	msg2 := "2024-06-20T22:15:59.123Z connection refused to db"
	assert.Equal(t, Fingerprint(msg1), Fingerprint(msg2))
}

func TestFingerprint_MemoryAddressStripped(t *testing.T) {
	msg1 := "panic: segfault at 0x7f3a2c01b940"
	msg2 := "panic: segfault at 0xdeadbeef"
	assert.Equal(t, Fingerprint(msg1), Fingerprint(msg2))
}

func TestFingerprint_LineNumberStripped(t *testing.T) {
	msg1 := "null pointer exception at line 42"
	msg2 := "null pointer exception at line 99"
	assert.Equal(t, Fingerprint(msg1), Fingerprint(msg2))
}

func TestFingerprint_UUIDStripped(t *testing.T) {
	msg1 := "request 550e8400-e29b-41d4-a716-446655440000 failed"
	msg2 := "request aaaabbbb-cccc-dddd-eeee-ffffffffffff failed"
	assert.Equal(t, Fingerprint(msg1), Fingerprint(msg2))
}

func TestFingerprint_DifferentMessages_DifferentFingerprints(t *testing.T) {
	msg1 := "connection refused to postgres"
	msg2 := "null pointer exception in handler"
	assert.NotEqual(t, Fingerprint(msg1), Fingerprint(msg2))
}

func TestFingerprint_Deterministic(t *testing.T) {
	msg := "error: failed to connect to redis on port 6379"
	fp1 := Fingerprint(msg)
	fp2 := Fingerprint(msg)
	assert.Equal(t, fp1, fp2)
}

func TestFingerprint_Returns16HexChars(t *testing.T) {
	fp := Fingerprint("some error message")
	assert.Len(t, fp, 16)
	for _, c := range fp {
		assert.True(t, (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f'),
			"fingerprint should be lowercase hex, got char %q", c)
	}
}
