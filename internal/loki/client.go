package loki

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// LogLine is a single decoded log entry from Loki.
type LogLine struct {
	Timestamp time.Time
	Container string
	Level     string
	Message   string
}

// Client is a minimal Loki HTTP client.
type Client struct {
	baseURL string
	http    *http.Client
}

// NewClient creates a new Loki client for the given base URL (e.g. "http://127.0.0.1:3100").
// The HTTP client uses a 10-second default timeout; context cancellation is always respected.
func NewClient(baseURL string) *Client {
	return &Client{
		baseURL: baseURL,
		http:    &http.Client{Timeout: 10 * time.Second},
	}
}

// NewClientWithTimeout creates a new Loki client with the given HTTP timeout.
func NewClientWithTimeout(baseURL string, timeout time.Duration) *Client {
	return &Client{
		baseURL: baseURL,
		http:    &http.Client{Timeout: timeout},
	}
}

// lokiQueryRangeResponse is the JSON shape returned by /loki/api/v1/query_range.
type lokiQueryRangeResponse struct {
	Data struct {
		Result []struct {
			Stream map[string]string `json:"stream"`
			Values [][2]string       `json:"values"` // [nanosecond-timestamp-string, log-line]
		} `json:"result"`
	} `json:"data"`
}

// QueryRange calls /loki/api/v1/query_range and returns decoded log lines in
// chronological order.
func (c *Client) QueryRange(ctx context.Context, query string, start, end time.Time, limit int, direction string) ([]LogLine, error) {
	params := url.Values{}
	params.Set("query", query)
	params.Set("start", strconv.FormatInt(start.UnixNano(), 10))
	params.Set("end", strconv.FormatInt(end.UnixNano(), 10))
	params.Set("limit", strconv.Itoa(limit))
	params.Set("direction", direction)

	reqURL := c.baseURL + "/loki/api/v1/query_range?" + params.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("building Loki request: %w", err)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("querying Loki: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Loki returned status %s", resp.Status)
	}

	var result lokiQueryRangeResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding Loki response: %w", err)
	}

	var lines []LogLine
	for _, stream := range result.Data.Result {
		container := stream.Stream["container"]
		level := stream.Stream["level"]
		for _, v := range stream.Values {
			ns, err := strconv.ParseInt(v[0], 10, 64)
			if err != nil {
				continue
			}
			lines = append(lines, LogLine{
				Timestamp: time.Unix(0, ns),
				Message:   v[1],
				Container: container,
				Level:     level,
			})
		}
	}
	return lines, nil
}

// lokiInstantQueryResponse is the JSON shape returned by /loki/api/v1/query for metric queries.
type lokiInstantQueryResponse struct {
	Data struct {
		ResultType string `json:"resultType"`
		Result     []struct {
			Value [2]interface{} `json:"value"` // [float64 unix timestamp, count string]
		} `json:"result"`
	} `json:"data"`
}

// CountErrors queries Loki for the number of error log lines within the given
// time window. containerKey is a health-snapshot key as produced by
// ContainerMeta.HealthKey():
//   - Docker containers: bare container name, e.g. "registry"
//   - K8s containers:    "namespace/container_name", e.g. "nr1-selling/selling-counter"
//
// When a namespace prefix is present the Loki query also filters by the
// `namespace` stream label, so containers with the same name in different
// namespaces are counted separately.
func (c *Client) CountErrors(ctx context.Context, containerKey string, since time.Duration) (int, error) {
	window := durationToPromQL(since)

	var query string
	if idx := strings.Index(containerKey, "/"); idx >= 0 {
		namespace := containerKey[:idx]
		container := containerKey[idx+1:]
		query = fmt.Sprintf(`count_over_time({container=%q,namespace=%q,level="error"}[%s])`, container, namespace, window)
	} else {
		query = fmt.Sprintf(`count_over_time({container=%q,level="error"}[%s])`, containerKey, window)
	}

	params := url.Values{}
	params.Set("query", query)
	params.Set("time", strconv.FormatInt(time.Now().UnixNano(), 10))

	reqURL := c.baseURL + "/loki/api/v1/query?" + params.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return 0, fmt.Errorf("building Loki count request: %w", err)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return 0, fmt.Errorf("querying Loki count: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("Loki returned status %s for count query", resp.Status)
	}

	var result lokiInstantQueryResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0, fmt.Errorf("decoding Loki count response: %w", err)
	}

	total := 0
	for _, r := range result.Data.Result {
		if len(r.Value) >= 2 {
			countStr, ok := r.Value[1].(string)
			if !ok {
				continue
			}
			n, err := strconv.Atoi(countStr)
			if err != nil {
				continue
			}
			total += n
		}
	}
	return total, nil
}

// QueryErrorMessages returns the raw log message strings for error-level events
// for the given container within the given time window (measured from now).
// It is used by the Tier2Evaluator to compute window-scoped fingerprint counts
// without importing the health package.
func (c *Client) QueryErrorMessages(ctx context.Context, containerKey string, since time.Duration) ([]string, error) {
	var query string
	if idx := strings.Index(containerKey, "/"); idx >= 0 {
		namespace := containerKey[:idx]
		container := containerKey[idx+1:]
		query = fmt.Sprintf(`{container=%q,namespace=%q,level="error"}`, container, namespace)
	} else {
		query = fmt.Sprintf(`{container=%q,level="error"}`, containerKey)
	}

	start := time.Now().Add(-since)
	end := time.Now()
	lines, err := c.QueryRange(ctx, query, start, end, 5000, "forward")
	if err != nil {
		return nil, fmt.Errorf("querying Loki for error messages: %w", err)
	}

	msgs := make([]string, len(lines))
	for i, l := range lines {
		msgs[i] = l.Message
	}
	return msgs, nil
}

// durationToPromQL converts a Go duration to a Loki/PromQL duration string.
// Examples: 3*time.Minute → "3m", 30*time.Second → "30s", time.Hour → "1h".
func durationToPromQL(d time.Duration) string {
	if d%time.Hour == 0 {
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	if d%time.Minute == 0 {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	return fmt.Sprintf("%ds", int(d.Seconds()))
}
