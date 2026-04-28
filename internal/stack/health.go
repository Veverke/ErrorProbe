package stack

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/errorprobe/errorprobe/internal/config"
)

// PollUntilReady polls the Loki and Grafana health endpoints until both respond
// successfully or the context deadline is exceeded.
// onStatus is called as each service becomes available.
func PollUntilReady(ctx context.Context, cfg *config.Config, onStatus func(string)) error {
	type service struct {
		name string
		url  string
		done bool
	}
	services := []*service{
		{
			name: "loki",
			url:  fmt.Sprintf("http://127.0.0.1:%d/ready", cfg.Stack.Loki.Port),
		},
		{
			name: "grafana",
			url:  fmt.Sprintf("http://127.0.0.1:%d/api/health", cfg.Stack.Grafana.Port),
		},
	}

	client := &http.Client{Timeout: 2 * time.Second}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for {
		allDone := true
		for _, svc := range services {
			if svc.done {
				continue
			}
			allDone = false
			resp, err := client.Get(svc.url)
			if err == nil && resp.StatusCode == http.StatusOK {
				_ = resp.Body.Close()
				svc.done = true
				if onStatus != nil {
					onStatus(svc.name + ": ready")
				}
			} else if resp != nil {
				_ = resp.Body.Close()
			}
		}
		if allDone {
			return nil
		}

		select {
		case <-ctx.Done():
			var missing []string
			for _, svc := range services {
				if !svc.done {
					missing = append(missing, svc.name)
				}
			}
			return fmt.Errorf("timed out waiting for services to become ready: %v", missing)
		case <-ticker.C:
		}
	}
}
