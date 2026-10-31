package manager

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// moveTree renames src to dst, falling back to copy+delete across volumes.
// dst must not exist: a folder already there is never merged into or
// deleted (after a failed copy only what this call created is removed).
func moveTree(src, dst string) error {
	if _, err := os.Lstat(dst); err == nil {
		return fmt.Errorf("%s already exists", dst)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	if err := copyTree(src, dst); err != nil {
		os.RemoveAll(dst) // it didn't exist before: this call made it
		return err
	}
	return os.RemoveAll(src)
}

// copyTree copies regular files and folders, keeping file modification times
// (backup-workshop relies on them to date Workshop items); symlinks are
// skipped.
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		t := filepath.Join(dst, rel)
		switch {
		case d.Type()&fs.ModeSymlink != 0:
			return nil
		case d.IsDir():
			return os.MkdirAll(t, 0o755)
		}
		in, err := os.Open(p)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.OpenFile(t, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, in); err != nil {
			out.Close()
			return err
		}
		if err := out.Close(); err != nil {
			return err
		}
		if info, err := d.Info(); err == nil {
			os.Chtimes(t, info.ModTime(), info.ModTime())
		}
		return nil
	})
}
