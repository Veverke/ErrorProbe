package loki

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// LogLine is a single decoded log entry from Loki.
type LogLine struct {
	Timestamp time.Time
	Line      string
}

// Client is a minimal Loki HTTP client.
type Client struct {
	baseURL string
	http    *http.Client
}

// NewClient creates a new Loki client for the given base URL (e.g. "http://127.0.0.1:3100").
func NewClient(baseURL string) *Client {
	return &Client{baseURL: baseURL, http: &http.Client{}}
}

// lokiQueryRangeResponse is the JSON shape returned by /loki/api/v1/query_range.
type lokiQueryRangeResponse struct {
	Data struct {
		Result []struct {
			Values [][2]string `json:"values"` // [nanosecond-timestamp-string, log-line]
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
		for _, v := range stream.Values {
			ns, err := strconv.ParseInt(v[0], 10, 64)
			if err != nil {
				continue
			}
			lines = append(lines, LogLine{
				Timestamp: time.Unix(0, ns),
				Line:      v[1],
			})
		}
	}
	return lines, nil
}
