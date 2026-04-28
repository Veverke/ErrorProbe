package configgen

import (
	"os"
	"path/filepath"
	"text/template"

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
