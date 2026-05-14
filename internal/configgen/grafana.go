package configgen

import (
	"os"
	"path/filepath"
	"text/template"

	"github.com/errorprobe/errorprobe/assets"
	"github.com/errorprobe/errorprobe/internal/config"
)

// GenerateGrafanaDatasource writes the Loki datasource provisioning file
// into outputDir/grafana/provisioning/datasources/loki.yaml, creating
// subdirectories as needed. The file is always overwritten.
func GenerateGrafanaDatasource(cfg *config.Config, outputDir string) error {
	tmpl, err := template.ParseFS(templateFS, "templates/grafana-datasource.yaml.tmpl")
	if err != nil {
		return wrapErr("parsing grafana-datasource template", err)
	}

	data := struct {
		LokiPort int
	}{
		LokiPort: cfg.Stack.Loki.Port,
	}

	destDir := filepath.Join(outputDir, "grafana", "provisioning", "datasources")
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return wrapErr("creating grafana provisioning dir", err)
	}

	outPath := filepath.Join(destDir, "loki.yaml")
	f, err := os.Create(outPath)
	if err != nil {
		return wrapErr("creating loki.yaml datasource", err)
	}
	defer f.Close()

	if err := tmpl.Execute(f, data); err != nil {
		return wrapErr("rendering grafana-datasource template", err)
	}
	return nil
}

// providerYAML is the Grafana dashboard provisioning file that tells Grafana
// where to find dashboard JSON files inside the container.
const providerYAML = `apiVersion: 1
providers:
  - name: ErrorProbe
    folder: ErrorProbe
    type: file
    disableDeletion: false
    updateIntervalSeconds: 30
    options:
      path: /etc/grafana/provisioning/dashboards
`

// GenerateGrafanaDashboards writes the dashboard provisioning YAML and the two
// pre-built dashboard JSON files into outputDir/grafana/provisioning/dashboards/.
func GenerateGrafanaDashboards(outputDir string) error {
	destDir := filepath.Join(outputDir, "grafana", "provisioning", "dashboards")
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return wrapErr("creating grafana dashboards dir", err)
	}

	if err := os.WriteFile(filepath.Join(destDir, "provider.yaml"), []byte(providerYAML), 0o644); err != nil {
		return wrapErr("writing dashboards provider.yaml", err)
	}

	for _, name := range []string{
		"errorprobe-overview.json",
		"errorprobe-detail.json",
		"errorprobe-watch.json",
		"errorprobe-restarts.json",
		"errorprobe-trends.json",
		"errorprobe-noise.json",
		"errorprobe-silent.json",
		"errorprobe-namespaces.json",
	} {
		data, err := assets.DashboardsFS.ReadFile("dashboards/" + name)
		if err != nil {
			return wrapErr("reading embedded dashboard "+name, err)
		}
		if err := os.WriteFile(filepath.Join(destDir, name), data, 0o644); err != nil {
			return wrapErr("writing dashboard "+name, err)
		}
	}
	return nil
}
