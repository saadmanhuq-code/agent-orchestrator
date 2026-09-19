//go:build windows

package sessionmanager

import (
	"os"
	"strings"

	"golang.org/x/sys/windows"
)

// resolveProviderPath returns the OS-canonical physical path for an existing
// file or directory, following NTFS junctions the way Windows itself does
// when opening a handle — not by manually walking reparse points the way
// filepath.EvalSymlinks does. EvalSymlinks can fail with "path not found"
// while walking a path that descends through a directory junction (for
// example a ~/.claude or ~/.codex directory relocated onto another volume,
// a common disk-space fix), even though ordinary file APIs open the exact
// same path without issue. GetFinalPathNameByHandle asks the OS directly,
// from an open handle, so it resolves exactly the way os.Open/os.Stat
// already do. Any failure is returned rather than degraded to the
// unresolved literal path: callers use this for allowed-root containment
// and must stay fail-closed. Mirrors
// backend/internal/service/usage/provider_path_windows.go.
func resolveProviderPath(path string) (string, error) {
	file, err := os.Open(path) //nolint:gosec // caller-validated provider path; the handle is read-only and used only to ask the OS for its canonical form.
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()

	handle := windows.Handle(file.Fd())
	buf := make([]uint16, 260) // MAX_PATH; grown below for longer targets.
	n, err := windows.GetFinalPathNameByHandle(handle, &buf[0], uint32(len(buf)), 0)
	if err != nil {
		return "", err
	}
	if int(n) > len(buf) {
		buf = make([]uint16, n)
		if n, err = windows.GetFinalPathNameByHandle(handle, &buf[0], uint32(len(buf)), 0); err != nil {
			return "", err
		}
	}
	return trimProviderPathPrefix(windows.UTF16ToString(buf[:n])), nil
}

// trimProviderPathPrefix strips the \\?\ extended-length prefix that
// GetFinalPathNameByHandle always returns, so the result matches the plain
// drive-letter form filepath.EvalSymlinks and the rest of this package use.
func trimProviderPathPrefix(path string) string {
	if rest, ok := strings.CutPrefix(path, `\\?\UNC\`); ok {
		return `\\` + rest
	}
	return strings.TrimPrefix(path, `\\?\`)
}
