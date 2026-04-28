// export_test.go exposes internal functions for use in package tests.
// It is compiled only when running tests.
package configgen

import "io/fs"

// WrapErr exposes the internal wrapErr function for testing.
func WrapErr(context string, err error) error {
	return wrapErr(context, err)
}

// SetTemplateFS replaces the filesystem used for template loading.
// Returns a restore function that resets to the original FS.
func SetTemplateFS(f fs.FS) func() {
	orig := templateFS
	templateFS = f
	return func() { templateFS = orig }
}
