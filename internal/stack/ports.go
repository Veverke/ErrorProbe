package stack

import (
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/errorprobe/errorprobe/internal/config"
)

// CheckPorts verifies that the ports required by the stack are not already in
// use on the host. It returns an error listing every conflicting port.
//
// On Windows / Docker Desktop, container port bindings are released
// asynchronously after the container is removed. CheckPorts retries for up to
// retryDuration before returning a conflict error, so that 'ep restart' does
// not fail on a transient port hold.
func CheckPorts(cfg *config.Config) error {
	ports := map[string]int{
		"loki":    cfg.Stack.Loki.Port,
		"grafana": cfg.Stack.Grafana.Port,
		"ingest":  cfg.Stack.Ingest.Port,
	}

	const retryInterval = 500 * time.Millisecond
	const retryDuration = 10 * time.Second
	deadline := time.Now().Add(retryDuration)

	var conflicts []string
	for {
		conflicts = conflicts[:0]
		for name, port := range ports {
			addr := fmt.Sprintf("127.0.0.1:%d", port)
			ln, err := net.Listen("tcp", addr)
			if err != nil {
				conflicts = append(conflicts, fmt.Sprintf("%s (port %d)", name, port))
				continue
			}
			_ = ln.Close()
		}
		if len(conflicts) == 0 {
			return nil
		}
		if time.Now().After(deadline) {
			break
		}
		time.Sleep(retryInterval)
	}

	return fmt.Errorf("port conflict: the following ports are already in use: %s", strings.Join(conflicts, ", "))
}
