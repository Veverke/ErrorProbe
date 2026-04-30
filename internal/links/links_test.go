package links_test

import (
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/errorprobe/errorprobe/internal/links"
)

func TestBuildExploreURL_ContainerNameEncoded(t *testing.T) {
	u := links.BuildExploreURL("http://localhost:3000", "my/container:special", time.Time{}, time.Time{})
	// The URL must be parseable.
	parsed, err := url.Parse(u)
	if err != nil {
		t.Fatalf("URL not parseable: %v", err)
	}
	// The container name appears encoded inside the ?left= query parameter.
	left := parsed.Query().Get("left")
	if left == "" {
		t.Fatal("expected ?left= parameter to be present")
	}
	if !strings.Contains(left, "my/container:special") {
		t.Errorf("container name not found in left param: %s", left)
	}
}

func TestBuildExploreURL_TimeRangeEncoded(t *testing.T) {
	from := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2024, 1, 1, 1, 0, 0, 0, time.UTC)

	u := links.BuildExploreURL("http://localhost:3000", "myapp", from, to)
	parsed, err := url.Parse(u)
	if err != nil {
		t.Fatalf("URL not parseable: %v", err)
	}
	left := parsed.Query().Get("left")
	if left == "" {
		t.Fatal("expected ?left= parameter")
	}
	// Both Unix millisecond timestamps should appear in the encoded payload.
	wantFrom := "1704067200000"
	wantTo := "1704070800000"
	if !strings.Contains(left, wantFrom) {
		t.Errorf("from timestamp %s not found in: %s", wantFrom, left)
	}
	if !strings.Contains(left, wantTo) {
		t.Errorf("to timestamp %s not found in: %s", wantTo, left)
	}
}

func TestBuildExploreURL_DefaultTimeRange(t *testing.T) {
	u := links.BuildExploreURL("http://localhost:3000", "myapp", time.Time{}, time.Time{})
	// When both times are zero, relative range "now-15m"/"now" should be used.
	parsed, err := url.Parse(u)
	if err != nil {
		t.Fatalf("URL not parseable: %v", err)
	}
	left := parsed.Query().Get("left")
	if !strings.Contains(left, "now-15m") {
		t.Errorf("expected relative range 'now-15m', got: %s", left)
	}
	if !strings.Contains(left, `"now"`) {
		t.Errorf("expected relative range 'now', got: %s", left)
	}
}

func TestBuildExploreURL_CustomPort(t *testing.T) {
	u := links.BuildExploreURL("http://localhost:3001", "myapp", time.Time{}, time.Time{})
	if !strings.HasPrefix(u, "http://localhost:3001") {
		t.Errorf("expected URL to start with http://localhost:3001, got: %s", u)
	}
}
