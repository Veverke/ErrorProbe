package configgen

import (
	"os"
	"path/filepath"
	"text/template"

	"github.com/errorprobe/errorprobe/internal/config"
)

// VectorGenerator is the interface for generating vector.toml.
// It allows callers (including the reconciler) to inject a fake in tests.
type VectorGenerator interface {
	GenerateVector(cfg *config.Config, outputDir string, containers []string) error
}

// GenerateVector writes vector.toml to outputDir using the embedded template.
// containers is the list of approved container names to include in the source.
func GenerateVector(cfg *config.Config, outputDir string, containers []string) error {
	tmpl, err := template.ParseFS(templateFS, "templates/vector.toml.tmpl")
	if err != nil {
		return wrapErr("parsing vector template", err)
	}

	data := struct {
		Containers    []string
		LokiPort      int
		IngestBind    string
		IngestPort    int
		ErrorPatterns []string
		WarnPatterns  []string
	}{
		Containers:    containers,
		LokiPort:      cfg.Stack.Loki.Port,
		IngestBind:    cfg.Stack.Ingest.Bind,
		IngestPort:    cfg.Stack.Ingest.Port,
		ErrorPatterns: cfg.Detection.SeverityPatterns.Error,
		WarnPatterns:  cfg.Detection.SeverityPatterns.Warn,
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
