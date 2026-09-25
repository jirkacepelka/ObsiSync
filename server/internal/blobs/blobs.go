// Package blobs stores file contents on disk, addressed by their SHA-256.
package blobs

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

var (
	ErrHashMismatch = errors.New("content does not match hash")
	ErrTooLarge     = errors.New("content too large")
	ErrInvalidHash  = errors.New("invalid hash")
)

var hashRe = regexp.MustCompile(`^[0-9a-f]{64}$`)

// ValidHash reports whether h is a lowercase hex SHA-256 digest.
func ValidHash(h string) bool { return hashRe.MatchString(h) }

type Store struct {
	dir string
}

func New(dir string) (*Store, error) {
	if err := os.MkdirAll(filepath.Join(dir, "tmp"), 0o755); err != nil {
		return nil, err
	}
	return &Store{dir: dir}, nil
}

func (s *Store) path(hash string) string {
	return filepath.Join(s.dir, hash[:2], hash[2:4], hash)
}

func (s *Store) Has(hash string) bool {
	if !ValidHash(hash) {
		return false
	}
	_, err := os.Stat(s.path(hash))
	return err == nil
}

func (s *Store) Open(hash string) (*os.File, error) {
	if !ValidHash(hash) {
		return nil, ErrInvalidHash
	}
	return os.Open(s.path(hash))
}

// Put streams r into the store, verifying it hashes to hash and is at most
// maxSize bytes. Existing blobs are left untouched.
func (s *Store) Put(hash string, r io.Reader, maxSize int64) (int64, error) {
	if !ValidHash(hash) {
		return 0, ErrInvalidHash
	}
	tmp, err := os.CreateTemp(filepath.Join(s.dir, "tmp"), "up-*")
	if err != nil {
		return 0, err
	}
	defer os.Remove(tmp.Name())
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, h), io.LimitReader(r, maxSize+1))
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return 0, err
	}
	if n > maxSize {
		return 0, ErrTooLarge
	}
	if hex.EncodeToString(h.Sum(nil)) != hash {
		return 0, ErrHashMismatch
	}
	dst := s.path(hash)
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return 0, err
	}
	if err := os.Rename(tmp.Name(), dst); err != nil {
		return 0, fmt.Errorf("store blob: %w", err)
	}
	return n, nil
}

// PutBytes is a convenience wrapper used by tests and internal writers.
func (s *Store) PutBytes(data []byte) (string, error) {
	sum := sha256.Sum256(data)
	hash := hex.EncodeToString(sum[:])
	if s.Has(hash) {
		return hash, nil
	}
	_, err := s.Put(hash, bytesReader(data), int64(len(data)))
	return hash, err
}

// DiskUsage returns the total size of all stored blobs.
func (s *Store) DiskUsage() int64 {
	var total int64
	filepath.WalkDir(s.dir, func(_ string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if info, err := d.Info(); err == nil {
				total += info.Size()
			}
		}
		return nil
	})
	return total
}

// GC deletes blobs that are not in live and are older than minAge (so blobs
// uploaded just before their commit are never collected). It returns the
// number of deleted blobs and freed bytes.
func (s *Store) GC(live map[string]bool, minAge time.Duration, now time.Time) (int, int64, error) {
	var count int
	var freed int64
	err := filepath.WalkDir(s.dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if d.Name() == "tmp" {
				return filepath.SkipDir
			}
			return nil
		}
		name := d.Name()
		if !ValidHash(name) || live[name] {
			return nil
		}
		info, err := d.Info()
		if err != nil || now.Sub(info.ModTime()) < minAge {
			return nil
		}
		if os.Remove(p) == nil {
			count++
			freed += info.Size()
		}
		return nil
	})
	return count, freed, err
}
