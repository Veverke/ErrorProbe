package stack

import (
	"fmt"
	"net"
	"strings"

	"github.com/errorprobe/errorprobe/internal/config"
)

// CheckPorts verifies that the ports required by the stack are not already in
// use on the host. It returns an error listing every conflicting port.
func CheckPorts(cfg *config.Config) error {
	ports := map[string]int{
		"loki":    cfg.Stack.Loki.Port,
		"grafana": cfg.Stack.Grafana.Port,
		"ingest":  cfg.Stack.Ingest.Port,
	}

	var conflicts []string
	for name, port := range ports {
		addr := fmt.Sprintf("127.0.0.1:%d", port)
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			conflicts = append(conflicts, fmt.Sprintf("%s (port %d)", name, port))
			continue
		}
		_ = ln.Close()
	}

	if len(conflicts) > 0 {
		return fmt.Errorf("port conflict: the following ports are already in use: %s", strings.Join(conflicts, ", "))
	}
	return nil
}
