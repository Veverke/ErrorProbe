package configgen

import (
	"github.com/errorprobe/errorprobe/internal/config"
	"github.com/errorprobe/errorprobe/internal/discovery"
)

// DefaultGenerator is a VectorGenerator that calls the package-level GenerateVector.
// It satisfies the discovery.VectorGenerator interface so callers can use the
// production implementation without importing the configgen package's internals.
type DefaultGenerator struct{}

// GenerateVector implements discovery.VectorGenerator.
func (DefaultGenerator) GenerateVector(cfg *config.Config, outputDir string, dockerContainers []string, k8sContainers []discovery.K8sContainerRef) error {
	return GenerateVector(cfg, outputDir, dockerContainers, k8sContainers)
}
