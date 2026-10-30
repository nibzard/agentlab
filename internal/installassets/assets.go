// Package installassets materializes the assets embedded in the client.
package installassets

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	agentlab "github.com/agentlab/agentlab"
)

// Materialize returns a content-addressed installation tree. A changed or
// incomplete cache fails closed; it never runs modified cached scripts.
func Materialize(cache string) (string, error) {
	hash := sha256.New()
	err := fs.WalkDir(agentlab.InstallationAssets, ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		data, err := agentlab.InstallationAssets.ReadFile(path)
		if err != nil {
			return err
		}
		fmt.Fprintf(hash, "%d:%s:%d:", len(path), path, len(data))
		_, err = hash.Write(data)
		return err
	})
	if err != nil {
		return "", err
	}
	root := filepath.Join(cache, "install-"+hex.EncodeToString(hash.Sum(nil)))
	if info, err := os.Lstat(root); err == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("invalid installation asset cache")
		}
		if err := Verify(root); err != nil {
			return "", err
		}
		return root, nil
	} else if !os.IsNotExist(err) {
		return "", err
	}
	if err := os.MkdirAll(cache, 0700); err != nil {
		return "", err
	}
	temp, err := os.MkdirTemp(cache, ".install-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(temp)
	err = fs.WalkDir(agentlab.InstallationAssets, ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == "." {
			return nil
		}
		destination := filepath.Join(temp, filepath.FromSlash(path))
		if entry.IsDir() {
			return os.MkdirAll(destination, 0700)
		}
		data, err := agentlab.InstallationAssets.ReadFile(path)
		if err != nil {
			return err
		}
		mode := fs.FileMode(0644)
		if executable(path) {
			mode = 0755
		}
		return os.WriteFile(destination, data, mode)
	})
	if err != nil {
		return "", err
	}
	if err := os.Rename(temp, root); err != nil {
		// Another process can publish the same immutable tree first.
		if verifyErr := Verify(root); verifyErr != nil {
			return "", err
		}
	}
	return root, Verify(root)
}

func executable(path string) bool {
	if strings.HasSuffix(path, ".sh") {
		return true
	}
	if strings.HasPrefix(path, "scripts/guest/") {
		switch filepath.Ext(path) {
		case "":
			return true
		}
	}
	return false
}

// Verify rejects edited content, symlinks, and unexpected files in the cache.
func Verify(root string) error {
	info, err := os.Lstat(root)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("invalid installation asset cache root")
	}
	expected := map[string]bool{}
	err = fs.WalkDir(agentlab.InstallationAssets, ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == "." {
			return nil
		}
		expected[path] = true
		destination := filepath.Join(root, filepath.FromSlash(path))
		info, err := os.Lstat(destination)
		if err != nil {
			return fmt.Errorf("installation assets are incomplete; remove the cache directory and retry")
		}
		if info.Mode()&os.ModeSymlink != 0 || info.IsDir() != entry.IsDir() {
			return fmt.Errorf("installation asset cache has an unsafe entry")
		}
		if entry.IsDir() {
			return nil
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("installation cache contains a non-regular file")
		}
		data, err := os.ReadFile(destination)
		if err != nil {
			return err
		}
		shipped, err := agentlab.InstallationAssets.ReadFile(path)
		if err != nil {
			return err
		}
		if !bytes.Equal(data, shipped) || (executable(path) && info.Mode().Perm()&0100 == 0) {
			return fmt.Errorf("installation assets differ from this client; remove the cache directory and retry")
		}
		return nil
	})
	if err != nil {
		return err
	}
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if relative != "." && !expected[filepath.ToSlash(relative)] {
			return fmt.Errorf("installation asset cache contains an unexpected file")
		}
		return nil
	})
}
