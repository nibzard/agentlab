// Package assets ships the trusted template recipe with the daemon binary.
package assets

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

//go:embed create_template.sh guest/*
var Files embed.FS

func Digest() string {
	h := sha256.New()
	_ = fs.WalkDir(Files, ".", func(p string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if !d.IsDir() {
			b, e := Files.ReadFile(p)
			if e != nil {
				return e
			}
			fmt.Fprintf(h, "%s\x00%d\x00", p, len(b))
			h.Write(b)
		}
		return nil
	})
	return hex.EncodeToString(h.Sum(nil))
}

// Extract verifies existing content and never replaces a pinned asset directory.
func Extract(root string) (string, error) {
	digest := Digest()
	dest := filepath.Join(root, digest)
	if _, e := os.Lstat(dest); e == nil {
		return dest, Verify(dest)
	}
	if e := os.MkdirAll(root, 0700); e != nil {
		return "", e
	}
	tmp, e := os.MkdirTemp(root, ".assets-")
	if e != nil {
		return "", e
	}
	defer os.RemoveAll(tmp)
	e = fs.WalkDir(Files, ".", func(p string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if p == "." {
			return nil
		}
		target := filepath.Join(tmp, p)
		if d.IsDir() {
			return os.Mkdir(target, 0700)
		}
		b, e := Files.ReadFile(p)
		if e != nil {
			return e
		}
		return os.WriteFile(target, b, 0400)
	})
	if e != nil {
		return "", e
	}
	if e = os.Rename(tmp, dest); e != nil {
		if v := Verify(dest); v != nil {
			return "", e
		}
	}
	return dest, nil
}
func Verify(dir string) error {
	return fs.WalkDir(Files, ".", func(p string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		target := filepath.Join(dir, p)
		info, e := os.Lstat(target)
		if e != nil {
			return e
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink in recipe assets")
		}
		if d.IsDir() {
			return nil
		}
		b, e := os.ReadFile(target)
		if e != nil {
			return e
		}
		want, e := Files.ReadFile(p)
		if e != nil {
			return e
		}
		if sha256.Sum256(b) != sha256.Sum256(want) {
			return fmt.Errorf("recipe asset digest mismatch: %s", p)
		}
		return nil
	})
}

// VerifyDigest accepts a previously installed recipe across daemon upgrades.
func VerifyDigest(dir, digest string) error {
	h := sha256.New()
	e := filepath.WalkDir(dir, func(p string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		rel, e := filepath.Rel(dir, p)
		if e != nil {
			return e
		}
		info, e := os.Lstat(p)
		if e != nil {
			return e
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink in assets")
		}
		if d.IsDir() {
			return nil
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("non-regular recipe asset")
		}
		b, e := os.ReadFile(p)
		if e != nil {
			return e
		}
		fmt.Fprintf(h, "%s\x00%d\x00", filepath.ToSlash(rel), len(b))
		h.Write(b)
		return nil
	})
	if e != nil {
		return e
	}
	if hex.EncodeToString(h.Sum(nil)) != digest {
		return fmt.Errorf("pinned recipe digest mismatch")
	}
	return nil
}
