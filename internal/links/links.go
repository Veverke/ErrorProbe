// Package links provides URL builders for ErrorProbe's external integrations.
package links

import (
	"encoding/json"
	"fmt"
	"net/url"
	"time"
)

// BuildExploreURL returns a Grafana Explore URL pre-filtered to the given
// container's logs over the provided time range.
//
// grafanaBaseURL is the full scheme+host+port of the Grafana instance,
// e.g. "http://localhost:3000".  from/to are encoded as Unix milliseconds
// in Grafana's ?left=[...] format.
//
// If from.IsZero() or to.IsZero(), the function uses "now-15m" / "now" as
// the time range (Grafana's relative syntax).
func BuildExploreURL(grafanaBaseURL string, containerName string, from time.Time, to time.Time) string {
	var rangeFrom, rangeTo string
	if from.IsZero() || to.IsZero() {
		rangeFrom = "now-15m"
		rangeTo = "now"
	} else {
		rangeFrom = fmt.Sprintf("%d", from.UnixMilli())
		rangeTo = fmt.Sprintf("%d", to.UnixMilli())
	}

	// Build the LogQL query stream selector.
	query := fmt.Sprintf(`{container="%s"}`, containerName)

	// Build the Grafana Explore state object that goes inside ?left=[...].
	// Grafana expects JSON: ["<from>","<to>","<datasource>",{"expr":"<query>"}]
	stateSlice := []interface{}{
		rangeFrom,
		rangeTo,
		"Loki",
		map[string]string{"expr": query},
	}
	stateJSON, err := json.Marshal(stateSlice)
	if err != nil {
		// Should never happen for this simple structure.
		stateJSON = []byte("[]")
	}

	return fmt.Sprintf("%s/explore?left=%s", grafanaBaseURL, url.QueryEscape(string(stateJSON)))
}
