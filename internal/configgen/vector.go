package configgen

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/template"

	"github.com/errorprobe/errorprobe/internal/config"
	"github.com/errorprobe/errorprobe/internal/discovery"
)

// escapeVRLPattern escapes characters that would break a VRL regex literal
// delimited by single quotes (r'...').
func escapeVRLPattern(s string) string {
	return strings.ReplaceAll(s, "'", "\\'")
}

// uniqueNamespaces returns a deduplicated, sorted list of namespace names from refs.
func uniqueNamespaces(refs []discovery.K8sContainerRef) []string {
	seen := make(map[string]struct{}, len(refs))
	for _, r := range refs {
		seen[r.Namespace] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for ns := range seen {
		out = append(out, ns)
	}
	sort.Strings(out)
	return out
}

// namespacePods returns a map of namespace → sorted, deduplicated pod names from refs.
func namespacePods(refs []discovery.K8sContainerRef) map[string][]string {
	m := make(map[string]map[string]struct{}, len(refs))
	for _, r := range refs {
		if m[r.Namespace] == nil {
			m[r.Namespace] = make(map[string]struct{})
		}
		m[r.Namespace][r.PodName] = struct{}{}
	}
	out := make(map[string][]string, len(m))
	for ns, pods := range m {
		sorted := make([]string, 0, len(pods))
		for p := range pods {
			sorted = append(sorted, p)
		}
		sort.Strings(sorted)
		out[ns] = sorted
	}
	return out
}

// GenerateVector writes vector.toml to outputDir using the embedded template.
// dockerContainers is the list of approved Docker container names.
// k8sContainers is the list of K8s containers (pod name + namespace).
func GenerateVector(cfg *config.Config, outputDir string, dockerContainers []string, k8sContainers []discovery.K8sContainerRef) error {
	funcMap := template.FuncMap{
		"escapeVRLPattern": escapeVRLPattern,
	}
	tmpl, err := template.New("vector.toml.tmpl").Funcs(funcMap).ParseFS(templateFS, "templates/vector.toml.tmpl")
	if err != nil {
		return wrapErr("parsing vector template", err)
	}

	var sources []string
	if len(dockerContainers) > 0 {
		sources = append(sources, "docker_logs")
	}
	if len(k8sContainers) > 0 {
		sources = append(sources, "k8s_filter")
	}
	if len(sources) == 0 {
		sources = []string{"internal_metrics"}
	}

	data := struct {
		DockerContainers []string
		K8sContainers    []discovery.K8sContainerRef
		UniqueNamespaces []string
		NamespacePods    map[string][]string
		Sources          []string
		LokiHost         string
		LokiPort         int
		IngestEnabled    bool
		IngestHost       string
		IngestPort       int
		ErrorPatterns    []string
		WarnPatterns     []string
	}{
		DockerContainers: dockerContainers,
		K8sContainers:    k8sContainers,
		UniqueNamespaces: uniqueNamespaces(k8sContainers),
		NamespacePods:    namespacePods(k8sContainers),
		Sources:          sources,
		LokiHost:         "errorprobe-loki",
		LokiPort:         cfg.Stack.Loki.Port,
		IngestEnabled:    cfg.Stack.Ingest.Port > 0,
		IngestHost:       ingestHost(cfg.Stack.Ingest.Bind),
		IngestPort:       cfg.Stack.Ingest.Port,
		ErrorPatterns:    cfg.Detection.SeverityPatterns.Error,
		WarnPatterns:     cfg.Detection.SeverityPatterns.Warn,
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

// ingestHost maps the configured bind address to the address Vector (running
// inside Docker) should use to reach the host process.
// 127.0.0.1 / 0.0.0.0 / localhost are loopback addresses that are unreachable
// from inside a container; Docker Desktop on Windows and macOS provides the
// special DNS name host.docker.internal for exactly this purpose.
func ingestHost(bind string) string {
	switch bind {
	case "127.0.0.1", "0.0.0.0", "localhost", "":
		return "host.docker.internal"
	default:
		return bind
	}
}
