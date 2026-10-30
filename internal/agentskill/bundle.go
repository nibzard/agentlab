// Package agentskill exposes the maintained skill embedded in the client.
package agentskill

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"

	agentlab "github.com/agentlab/agentlab"
)

type Guide struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	File string `json:"file"`
}

type Manifest struct {
	Name              string   `json:"name"`
	Version           string   `json:"version"`
	SupportedVersions []string `json:"supported_agent_versions"`
	Guides            []Guide  `json:"included_skills"`
}

type Info struct {
	Manifest Manifest `json:"manifest"`
	SHA256   string   `json:"sha256"`
	Files    []string `json:"files"`
}

type Exported struct {
	Info
	Directory  string `json:"directory"`
	Entrypoint string `json:"entrypoint"`
}

// Error describes a local export requirement without connection side effects.
type Error struct{ Code, Action string }

func (e *Error) Error() string { return e.Action }

type bundle struct {
	info  Info
	files map[string][]byte
}

func load() (bundle, error) {
	result := bundle{files: map[string][]byte{}}
	root, err := fs.Sub(agentlab.InstallationAssets, "skills/agentlab")
	if err != nil {
		return result, err
	}
	hash := sha256.New()
	err = fs.WalkDir(root, ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() || !fs.ValidPath(name) {
			return fmt.Errorf("invalid packaged skill entry")
		}
		data, err := fs.ReadFile(root, name)
		if err != nil {
			return err
		}
		if len(data) > 1<<20 || len(result.files) >= 128 {
			return fmt.Errorf("packaged skill exceeds its size limits")
		}
		result.files[name] = data
		result.info.Files = append(result.info.Files, name)
		fmt.Fprintf(hash, "%d:%s:%d:", len(name), name, len(data))
		_, err = hash.Write(data)
		return err
	})
	if err != nil {
		return result, err
	}
	if err := json.Unmarshal(result.files["bundle/manifest.json"], &result.info.Manifest); err != nil {
		return result, fmt.Errorf("invalid packaged skill manifest")
	}
	if result.info.Manifest.Name != "agentlab" || result.info.Manifest.Version == "" || len(result.files["SKILL.md"]) == 0 {
		return result, fmt.Errorf("incomplete packaged skill")
	}
	seen := map[string]bool{}
	for _, guide := range result.info.Manifest.Guides {
		if !fs.ValidPath(guide.File) || guide.ID == "" || guide.Name == "" || seen[guide.File] || len(result.files[guide.File]) == 0 {
			return result, fmt.Errorf("invalid packaged skill guide")
		}
		seen[guide.File] = true
	}
	result.info.SHA256 = hex.EncodeToString(hash.Sum(nil))
	return result, nil
}

func Inspect() (Info, error) { b, err := load(); return b.info, err }

// Export publishes the exact skill bundle. It never replaces existing content.
// An identical destination is a successful retry; modified content is preserved.
func Export(ctx context.Context, destination string) (Exported, error) {
	b, err := load()
	if err != nil {
		return Exported{}, err
	}
	if destination == "" {
		cache, err := os.UserCacheDir()
		if err != nil {
			return Exported{}, err
		}
		destination = filepath.Join(cache, "agentlab", "skills", b.info.SHA256, "agentlab")
	}
	destination, err = filepath.Abs(destination)
	if err != nil {
		return Exported{}, err
	}
	result := Exported{Info: b.info, Directory: destination, Entrypoint: filepath.Join(destination, "SKILL.md")}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if _, err := os.Lstat(destination); err == nil {
		return result, b.verify(ctx, destination)
	} else if !os.IsNotExist(err) {
		return result, err
	}
	parent := filepath.Dir(destination)
	if err := os.MkdirAll(parent, 0700); err != nil {
		return result, err
	}
	temp, err := os.MkdirTemp(parent, ".agentlab-skill-*")
	if err != nil {
		return result, err
	}
	defer os.RemoveAll(temp)
	dirs := map[string]bool{temp: true}
	for _, name := range b.info.Files {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		target := filepath.Join(temp, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			return result, err
		}
		for directory := filepath.Dir(target); directory != filepath.Dir(temp); directory = filepath.Dir(directory) {
			dirs[directory] = true
		}
		file, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return result, err
		}
		_, writeErr := file.Write(b.files[name])
		syncErr := file.Sync()
		closeErr := file.Close()
		if writeErr != nil {
			return result, writeErr
		}
		if syncErr != nil {
			return result, syncErr
		}
		if closeErr != nil {
			return result, closeErr
		}
	}
	ordered := make([]string, 0, len(dirs))
	for directory := range dirs {
		ordered = append(ordered, directory)
	}
	sort.Sort(sort.Reverse(sort.StringSlice(ordered)))
	for _, directory := range ordered {
		if err := syncDirectory(directory); err != nil {
			return result, err
		}
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if err := os.Rename(temp, destination); err != nil {
		// Another process can publish the same bundle while this one stages it.
		if verifyErr := b.verify(ctx, destination); verifyErr == nil {
			return result, nil
		}
		return result, changed()
	}
	if err := syncDirectory(parent); err != nil {
		return result, err
	}
	return result, nil
}

func changed() error {
	return &Error{Code: "skill_export_changed", Action: "preserve the existing skill files and choose an unused --out directory to export this client bundle"}
}

func (b bundle) verify(ctx context.Context, root string) error {
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return changed()
	}
	allowedDirs := map[string]bool{".": true}
	for name := range b.files {
		for dir := path.Dir(name); dir != "."; dir = path.Dir(dir) {
			allowedDirs[dir] = true
		}
	}
	count := 0
	err = filepath.WalkDir(root, func(absolute string, entry fs.DirEntry, err error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err != nil {
			return changed()
		}
		name, err := filepath.Rel(root, absolute)
		if err != nil {
			return changed()
		}
		name = filepath.ToSlash(name)
		if entry.IsDir() {
			if !allowedDirs[name] {
				return changed()
			}
			return nil
		}
		data, ok := b.files[name]
		if !ok || !entry.Type().IsRegular() {
			return changed()
		}
		info, err := entry.Info()
		if err != nil || info.Size() != int64(len(data)) {
			return changed()
		}
		file, err := os.Open(absolute)
		if err != nil {
			return changed()
		}
		actual, readErr := io.ReadAll(io.LimitReader(file, int64(len(data))+1))
		closeErr := file.Close()
		if readErr != nil || closeErr != nil || !bytes.Equal(actual, data) {
			return changed()
		}
		count++
		return nil
	})
	if err != nil {
		return err
	}
	if count != len(b.files) {
		return changed()
	}
	return nil
}

func syncDirectory(directory string) error {
	file, err := os.Open(directory)
	if err != nil {
		return err
	}
	defer file.Close()
	return file.Sync()
}
