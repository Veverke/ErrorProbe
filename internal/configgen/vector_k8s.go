package configgen

import (
	"bytes"
	"text/template"

	"github.com/errorprobe/errorprobe/internal/config"
)

// VectorK8sConfig renders the vector-k8s.toml template and returns the
// content as a string, ready to be placed into a K8s ConfigMap.
func VectorK8sConfig(cfg *config.Config) (string, error) {
	funcMap := template.FuncMap{
		"escapeVRLPattern": escapeVRLPattern,
	}
	tmpl, err := template.New("vector-k8s.toml.tmpl").Funcs(funcMap).ParseFS(templateFS, "templates/vector-k8s.toml.tmpl")
	if err != nil {
		return "", wrapErr("parsing vector-k8s template", err)
	}

	data := struct {
		LokiPort      int
		IngestPort    int
		ErrorPatterns []string
		WarnPatterns  []string
	}{
		LokiPort:      cfg.Stack.Loki.Port,
		IngestPort:    cfg.Stack.Ingest.Port,
		ErrorPatterns: cfg.Detection.SeverityPatterns.Error,
		WarnPatterns:  cfg.Detection.SeverityPatterns.Warn,
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", wrapErr("rendering vector-k8s template", err)
	}
	return buf.String(), nil
}
