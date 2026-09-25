// Package pathutil validates and normalizes vault-relative file paths.
package pathutil

import (
	"errors"
	"strings"

	"golang.org/x/text/unicode/norm"
)

var ErrInvalidPath = errors.New("invalid path")

// Normalize returns the canonical form of a vault-relative path: Unicode NFC,
// forward slashes, no leading slash, no empty, "." or ".." segments.
// macOS/iOS often hand out NFD file names, so normalizing is required for
// the same note to have the same key on every device.
func Normalize(p string) (string, error) {
	p = norm.NFC.String(p)
	if p == "" || len(p) > 1024 || strings.ContainsRune(p, 0) {
		return "", ErrInvalidPath
	}
	if strings.HasPrefix(p, "/") || strings.Contains(p, "\\") {
		return "", ErrInvalidPath
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return "", ErrInvalidPath
		}
	}
	return p, nil
}

// Fold returns the case-folded key used to detect paths that differ only by
// letter case (which collide on Windows and macOS file systems).
func Fold(p string) string {
	return strings.ToLower(norm.NFC.String(p))
}
