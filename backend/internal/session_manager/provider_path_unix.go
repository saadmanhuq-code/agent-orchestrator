//go:build !windows

package sessionmanager

import "path/filepath"

// resolveProviderPath returns the canonical physical path for an existing
// file or directory. Unix has no analogue to the Windows junction-walking
// failure this package works around (see provider_path_windows.go), so
// filepath.EvalSymlinks already resolves correctly and any error is a real
// one; callers must treat it as fail-closed.
func resolveProviderPath(path string) (string, error) {
	return filepath.EvalSymlinks(path)
}
