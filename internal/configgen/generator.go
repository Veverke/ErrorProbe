package configgen

import "github.com/errorprobe/errorprobe/internal/config"

// DefaultGenerator is a VectorGenerator that calls the package-level GenerateVector.
// It satisfies the discovery.VectorGenerator interface so callers can use the
// production implementation without importing the configgen package's internals.
type DefaultGenerator struct{}

// GenerateVector implements discovery.VectorGenerator.
func (DefaultGenerator) GenerateVector(cfg *config.Config, outputDir string, containers []string) error {
	return GenerateVector(cfg, outputDir, containers)
}
