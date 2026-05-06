// export_test.go exposes internal fields for use in tests.
package discovery

import "time"

// SetReconcilerInterval replaces the ticker interval on a Reconciler for faster tests.
func SetReconcilerInterval(r *Reconciler, d time.Duration) {
	r.interval = d
}

// SetReconcilerStatePath replaces the state path used by the Reconciler.
func SetReconcilerStatePath(r *Reconciler, path string) {
	r.statePath = path
}


