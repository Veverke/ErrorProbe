package configgen

import (
	"os"
	"path/filepath"
	"text/template"

	"github.com/errorprobe/errorprobe/internal/config"
)

// GenerateVector writes vector.toml to outputDir using the embedded template.
// The containers parameter will be used in Phase 2 to inject sources;
// for Phase 1 it is always empty. The file is always overwritten.
func GenerateVector(cfg *config.Config, outputDir string, containers []string) error {
	tmpl, err := template.ParseFS(templateFS, "templates/vector.toml.tmpl")
	if err != nil {
		return wrapErr("parsing vector template", err)
	}

	data := struct {
		Containers []string
		LokiPort   int
	}{
		Containers: containers,
		LokiPort:   cfg.Stack.Loki.Port,
	}

	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return wrapErr("creating output dir", err)
	}

	outPath := filepath.Join(outputDir, "vector.toml")
	f, err := os.Create(outPath)
	if err != nil {
		return wrapErr("creating vector.toml", err)
	}
	defer f.Close()

	if err := tmpl.Execute(f, data); err != nil {
		return wrapErr("rendering vector template", err)
	}
	return nil
}
