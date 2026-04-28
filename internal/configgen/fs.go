package configgen

import (
	"io/fs"

	"github.com/errorprobe/errorprobe/assets"
)

// templateFS is the filesystem used to load embedded templates.
// It defaults to the production embedded FS but may be replaced in tests.
var templateFS fs.FS = assets.FS
