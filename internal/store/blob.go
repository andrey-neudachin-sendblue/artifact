package store

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"

	"github.com/10/artifact/internal/content"
)

// blob is what a completed streaming write learned about the bytes it stored.
// The file is still under its temporary name; publish moves it into place.
type blob struct {
	tmpPath string
	size    int64
	sum     string
	head    []byte
}

// writeBlob streams src into a temporary file inside dir, hashing in the same
// pass, and fsyncs it. The caller either publishes the result or removes it.
func writeBlob(dir string, src io.Reader) (*blob, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create blob directory: %w", err)
	}

	f, err := os.CreateTemp(dir, ".upload-*.tmp")
	if err != nil {
		return nil, fmt.Errorf("create temporary blob: %w", err)
	}

	tmpPath := f.Name()

	discard := func() {
		f.Close()
		os.Remove(tmpPath)
	}

	hash := sha256.New()

	size, err := io.Copy(io.MultiWriter(f, hash), src)
	if err != nil {
		discard()

		// Wrapped, not replaced: the caller inspects this for
		// *http.MaxBytesError to distinguish 413 from 500.
		return nil, fmt.Errorf("stream upload: %w", err)
	}

	if size == 0 {
		discard()

		return nil, ErrEmptyBody
	}

	if err := f.Sync(); err != nil {
		discard()

		return nil, fmt.Errorf("sync blob: %w", err)
	}

	head := make([]byte, min(size, int64(content.SniffLen)))
	if _, err := f.ReadAt(head, 0); err != nil && !errors.Is(err, io.EOF) {
		discard()

		return nil, fmt.Errorf("read blob head: %w", err)
	}

	if err := f.Chmod(0o644); err != nil {
		discard()

		return nil, fmt.Errorf("chmod blob: %w", err)
	}

	if err := f.Close(); err != nil {
		os.Remove(tmpPath)

		return nil, fmt.Errorf("close blob: %w", err)
	}

	return &blob{tmpPath: tmpPath, size: size, sum: hex.EncodeToString(hash.Sum(nil)), head: head}, nil
}

// publish renames a written blob to its version number and fsyncs the
// directory so the rename itself survives a crash.
func publish(dir, tmpPath string, n int) error {
	if err := os.Rename(tmpPath, filepath.Join(dir, strconv.Itoa(n))); err != nil {
		return fmt.Errorf("publish blob: %w", err)
	}

	return syncDir(dir)
}

func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("open blob directory: %w", err)
	}
	defer d.Close()

	if err := d.Sync(); err != nil {
		return fmt.Errorf("sync blob directory: %w", err)
	}

	return nil
}
