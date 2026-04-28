package configgen

import (
	"os"
	"path/filepath"
	"text/template"

	"github.com/errorprobe/errorprobe/internal/config"
)

// GenerateLoki writes loki-config.yaml to outputDir using the embedded template.
// The file is always overwritten.
func GenerateLoki(cfg *config.Config, outputDir string) error {
	tmpl, err := template.ParseFS(templateFS, "templates/loki.yaml.tmpl")
	if err != nil {
		return wrapErr("parsing loki template", err)
	}

	data := struct {
		Port      int
		Retention string
	}{
		Port:      cfg.Stack.Loki.Port,
		Retention: cfg.Stack.Loki.Retention,
	}

	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return wrapErr("creating output dir", err)
	}

	outPath := filepath.Join(outputDir, "loki-config.yaml")
	f, err := os.Create(outPath)
	if err != nil {
		return wrapErr("creating loki-config.yaml", err)
	}
	defer f.Close()

	if err := tmpl.Execute(f, data); err != nil {
		return wrapErr("rendering loki template", err)
	}
	return nil
}
