//go:build windows

package pid

// SendHUP is a no-op on Windows where SIGHUP is not available.
// PBR rule changes will take effect on the next 'ep up' invocation.
func SendHUP(path string) error {
	return nil
}
