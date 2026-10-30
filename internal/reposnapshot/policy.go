package reposnapshot

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/agentlab/agentlab/internal/contentpolicy"
)

func secretContentForPath(name string, data []byte) bool {
	return contentpolicy.SensitiveContent(name, data)
}
func secretPath(name string) bool { return contentpolicy.SensitivePath(name) }

func quotedPath(name string) string {
	var output strings.Builder
	output.WriteByte('"')
	for _, value := range []byte(name) {
		switch value {
		case '"', '\\':
			output.WriteByte('\\')
			output.WriteByte(value)
		default:
			if value < 32 || value >= 127 {
				fmt.Fprintf(&output, "\\%03o", value)
			} else {
				output.WriteByte(value)
			}
		}
	}
	output.WriteByte('"')
	return output.String()
}

func writeCommit(writer io.Writer, ref, parent, message string, timestamp int64, files tree) error {
	if _, err := fmt.Fprintf(writer, "commit %s\ncommitter AgentLab snapshot <snapshot@agentlab.invalid> %d +0000\ndata %d\n%s\n", ref, timestamp, len(message), message); err != nil {
		return err
	}
	if parent != "" {
		if _, err := fmt.Fprintf(writer, "from %s\n", parent); err != nil {
			return err
		}
	}
	if _, err := io.WriteString(writer, "deleteall\n"); err != nil {
		return err
	}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		item := files[name]
		if _, err := fmt.Fprintf(writer, "M %s inline %s\ndata %d\n", item.mode, quotedPath(name), len(item.data)); err != nil {
			return err
		}
		if _, err := writer.Write(item.data); err != nil {
			return err
		}
		if _, err := io.WriteString(writer, "\n"); err != nil {
			return err
		}
	}
	_, err := io.WriteString(writer, "\n")
	return err
}

func hashFile(path string) (string, int64, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", 0, err
	}
	if !info.Mode().IsRegular() {
		return "", 0, fmt.Errorf("snapshot artifact is not a regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer file.Close()
	hash := sha256.New()
	count, err := io.Copy(hash, io.LimitReader(file, (256<<20)+1))
	if err != nil {
		return "", count, err
	}
	if count > 256<<20 {
		return "", count, &Error{Code: "repository_bundle_too_large", Action: "the repository bundle exceeds 256 MiB; reduce the transfer size"}
	}
	return hex.EncodeToString(hash.Sum(nil)), count, nil
}

func verifyResult(root string, expected Manifest) (Result, error) {
	result := Result{PatchPath: filepath.Join(root, "changes.patch"), BundlePath: filepath.Join(root, "source.bundle"), ManifestPath: filepath.Join(root, "manifest.json")}
	info, err := os.Lstat(root)
	if err != nil {
		return result, err
	}
	if !info.IsDir() {
		return result, fmt.Errorf("snapshot cache root is not a directory")
	}
	manifestInfo, err := os.Lstat(result.ManifestPath)
	if err != nil {
		return result, err
	}
	if !manifestInfo.Mode().IsRegular() || manifestInfo.Size() > 16<<20 {
		return result, fmt.Errorf("invalid snapshot cache manifest")
	}
	data, err := os.ReadFile(result.ManifestPath)
	if err != nil {
		return result, err
	}
	if err := json.Unmarshal(data, &result.Manifest); err != nil {
		return result, err
	}
	actual, _ := json.Marshal(result.Manifest)
	want, _ := json.Marshal(expected)
	if !bytes.Equal(actual, want) {
		return result, &Error{Code: "snapshot_cache_changed", Action: "snapshot cache metadata changed; remove its cache directory and retry"}
	}
	digest, size, err := hashFile(result.BundlePath)
	if err != nil {
		return result, err
	}
	if digest != expected.BundleSHA256 || size != expected.BundleBytes {
		return result, &Error{Code: "snapshot_cache_changed", Action: "snapshot bundle changed; remove its cache directory and retry"}
	}
	if expected.PatchSHA256 != "" {
		digest, size, err := hashFile(result.PatchPath)
		if err != nil {
			return result, err
		}
		if digest != expected.PatchSHA256 || size != expected.PatchBytes {
			return result, &Error{Code: "snapshot_cache_changed", Action: "snapshot patch changed; remove its cache directory and retry"}
		}
	} else {
		result.PatchPath = ""
	}
	return result, nil
}
