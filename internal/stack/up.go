package stack

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/errorprobe/errorprobe/internal/config"
	"github.com/errorprobe/errorprobe/internal/configgen"
	"github.com/errorprobe/errorprobe/internal/docker"
)

// pollFn is the health-check implementation used by upCore.
// It may be replaced in tests to avoid real HTTP polling.
var pollFn = PollUntilReady

// Up brings up the full observability stack (Vector, Loki, Grafana).
// onStatus is called with progress messages. It is safe to call against
// an already-running stack — it is fully idempotent.
func Up(ctx context.Context, cfg *config.Config, onStatus func(string)) error {
	cli, err := docker.NewClient()
	if err != nil {
		return fmt.Errorf("connecting to docker: %w", err)
	}
	defer cli.Close()
	return upCore(ctx, cfg, cli, onStatus)
}

// upCore is the testable implementation of Up. It receives an already-created
// Docker client so that tests can inject a mock.
func upCore(ctx context.Context, cfg *config.Config, cli docker.DockerAPI, onStatus func(string)) error {
	// 1. Verify Docker daemon is reachable.
	onStatus("checking docker daemon…")
	if err := cli.Ping(ctx); err != nil {
		return err
	}

	// 2. Check port conflicts before pulling images (fail fast).
	onStatus("checking port availability…")
	if err := CheckPorts(cfg); err != nil {
		return err
	}

	// 3. Idempotency: if all three containers are running, nothing to do.
	allRunning := true
	for _, name := range []string{ContainerLoki, ContainerGrafana, ContainerVector} {
		running, err := cli.ContainerRunning(ctx, name)
		if err != nil {
			return err
		}
		if !running {
			allRunning = false
			break
		}
	}
	if allRunning {
		onStatus("stack already running — use 'errorprobe down' to stop it")
		return nil
	}

	// 4. Pull images.
	images := []struct{ label, image string }{
		{"loki", cfg.Stack.Loki.Image},
		{"grafana", cfg.Stack.Grafana.Image},
		{"vector", cfg.Stack.Vector.Image},
	}
	for _, img := range images {
		onStatus(fmt.Sprintf("pulling %s image (%s)…", img.label, img.image))
		if err := cli.PullImage(ctx, img.image, func(line string) {
			onStatus("  " + line)
		}); err != nil {
			return fmt.Errorf("pulling %s: %w", img.label, err)
		}
	}

	// 5. Generate configs.
	configsDir := cfg.ConfigsDir()
	onStatus("generating configs…")
	if err := configgen.GenerateLoki(cfg, configsDir); err != nil {
		return fmt.Errorf("generating loki config: %w", err)
	}
	if err := configgen.GenerateGrafanaDatasource(cfg, configsDir); err != nil {
		return fmt.Errorf("generating grafana datasource: %w", err)
	}
	if err := configgen.GenerateVector(cfg, configsDir, []string{}); err != nil {
		return fmt.Errorf("generating vector config: %w", err)
	}

	// 6. Create network.
	onStatus("ensuring docker network…")
	if err := cli.CreateNetwork(ctx, NetworkName); err != nil {
		return err
	}

	// 7. Create volumes.
	for _, vol := range []string{VolumeLokiData, VolumeGrafanaData} {
		if err := cli.CreateVolume(ctx, vol); err != nil {
			return err
		}
	}

	// 8. Start Loki.
	onStatus("starting loki…")
	lokiConfigPath := filepath.Join(configsDir, "loki-config.yaml")
	if err := cli.StartContainer(ctx, docker.ContainerSpec{
		Name:  ContainerLoki,
		Image: cfg.Stack.Loki.Image,
		Cmd:   []string{"-config.file=/etc/loki/local-config.yaml"},
		Ports: []docker.PortBinding{
			{HostPort: fmt.Sprintf("%d", cfg.Stack.Loki.Port), ContainerPort: "3100"},
		},
		Mounts: []docker.Mount{
			{Source: lokiConfigPath, Target: "/etc/loki/local-config.yaml", ReadOnly: true},
		},
		Volumes: []docker.VolumeMount{
			{Name: VolumeLokiData, Target: "/loki"},
		},
		Networks: []string{NetworkName},
		Labels:   managedLabel(),
	}); err != nil {
		return fmt.Errorf("starting loki: %w", err)
	}

	// 9. Start Grafana.
	onStatus("starting grafana…")
	grafanaProvisioningDir := filepath.Join(configsDir, "grafana", "provisioning")
	if err := cli.StartContainer(ctx, docker.ContainerSpec{
		Name:  ContainerGrafana,
		Image: cfg.Stack.Grafana.Image,
		Ports: []docker.PortBinding{
			{HostPort: fmt.Sprintf("%d", cfg.Stack.Grafana.Port), ContainerPort: "3000"},
		},
		Mounts: []docker.Mount{
			{Source: grafanaProvisioningDir, Target: "/etc/grafana/provisioning", ReadOnly: true},
		},
		Volumes: []docker.VolumeMount{
			{Name: VolumeGrafanaData, Target: "/var/lib/grafana"},
		},
		Networks: []string{NetworkName},
		Labels:   managedLabel(),
	}); err != nil {
		return fmt.Errorf("starting grafana: %w", err)
	}

	// 10. Start Vector.
	onStatus("starting vector…")
	vectorConfigPath := filepath.Join(configsDir, "vector.toml")
	if err := cli.StartContainer(ctx, docker.ContainerSpec{
		Name:  ContainerVector,
		Image: cfg.Stack.Vector.Image,
		Mounts: []docker.Mount{
			{Source: vectorConfigPath, Target: "/etc/vector/vector.toml", ReadOnly: true},
		},
		Networks: []string{NetworkName},
		Labels:   managedLabel(),
	}); err != nil {
		return fmt.Errorf("starting vector: %w", err)
	}

	// 11. Poll until ready (60 s timeout).
	onStatus("waiting for services to become ready…")
	pollCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	if err := pollFn(pollCtx, cfg, onStatus); err != nil {
		return err
	}

	// 12. Final status.
	onStatus(fmt.Sprintf("stack ready — Grafana: http://localhost:%d", cfg.Stack.Grafana.Port))
	return nil
}

// managedLabel returns the standard label applied to all errorprobe containers.
func managedLabel() map[string]string {
	return map[string]string{"managed-by": "errorprobe"}
}

