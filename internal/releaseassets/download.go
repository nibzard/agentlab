// Package releaseassets retrieves pinned server artifacts from AgentLab releases.
package releaseassets

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"
)

const DefaultBaseURL = "https://github.com/nibzard/agentlab/releases/download"
const maxArchiveBytes = 256 << 20
const maxChecksumBytes = 1 << 20

var versionPattern = regexp.MustCompile(`^v?(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z.-]+)?$`)

type Pair struct {
	Client       string
	Daemon       string
	Tag          string
	Architecture string
}
type Downloader struct {
	Root    string
	BaseURL string
	Client  *http.Client
}
type manifest struct {
	Tag          string            `json:"tag"`
	Architecture string            `json:"architecture"`
	ClientSHA256 string            `json:"client_sha256"`
	DaemonSHA256 string            `json:"daemon_sha256"`
	Archives     map[string]string `json:"archives"`
}

// Tag normalizes a release version without selecting latest or a snapshot.
func Tag(version string) (string, error) {
	if !versionPattern.MatchString(version) {
		return "", fmt.Errorf("a pinned release version is required; development clients must provide Linux server binaries")
	}
	if parts := strings.SplitN(version, "-", 2); len(parts) == 2 {
		for _, identifier := range strings.Split(parts[1], ".") {
			if identifier == "" || (len(identifier) > 1 && identifier[0] == '0' && strings.Trim(identifier, "0123456789") == "") {
				return "", fmt.Errorf("invalid pinned prerelease version")
			}
		}
	}
	return "v" + strings.TrimPrefix(version, "v"), nil
}

func safeURL(raw string) bool {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.User != nil || parsed.Host == "" {
		return false
	}
	return parsed.Scheme == "https" || (parsed.Scheme == "http" && (parsed.Hostname() == "127.0.0.1" || parsed.Hostname() == "localhost" || parsed.Hostname() == "::1"))
}

func (d Downloader) download(ctx context.Context, address string, limit int64) ([]byte, error) {
	if !safeURL(address) {
		return nil, fmt.Errorf("release downloads require HTTPS")
	}
	transport := http.DefaultTransport
	if d.Client != nil && d.Client.Transport != nil {
		transport = d.Client.Transport
	}
	client := &http.Client{Timeout: 2 * time.Minute, Transport: transport, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return fmt.Errorf("too many release redirects")
		}
		if !safeURL(req.URL.String()) {
			return fmt.Errorf("release redirect uses an unsafe URL")
		}
		if via[0].URL.Scheme == "https" && req.URL.Scheme != "https" {
			return fmt.Errorf("release redirect downgrades HTTPS")
		}
		return nil
	}}
	request, err := http.NewRequestWithContext(ctx, "GET", address, nil)
	if err != nil {
		return nil, err
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("release download returned HTTP %d; verify the pinned release exists", response.StatusCode)
	}
	if response.ContentLength > limit {
		return nil, fmt.Errorf("release artifact exceeds size limit")
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("release artifact exceeds size limit")
	}
	return data, nil
}

func checksum(data []byte, name string) (string, error) {
	expected := ""
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		if strings.TrimPrefix(fields[1], "*") != name {
			continue
		}
		digest, err := hex.DecodeString(fields[0])
		if err != nil || len(digest) != sha256.Size {
			return "", fmt.Errorf("invalid checksum for release archive")
		}
		if expected != "" {
			return "", fmt.Errorf("duplicate checksum for release archive")
		}
		expected = strings.ToLower(fields[0])
	}
	if expected == "" {
		return "", fmt.Errorf("release archive is missing from checksums.txt")
	}
	return expected, nil
}

func extract(data []byte, binary, path string) (string, error) {
	reader, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	defer reader.Close()
	archive := tar.NewReader(reader)
	found := false
	count := 0
	total := int64(0)
	digest := ""
	for {
		header, err := archive.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", err
		}
		count++
		total += header.Size
		if count > 128 || header.Size < 0 || total > maxArchiveBytes {
			return "", fmt.Errorf("release archive exceeds extraction limits")
		}
		name := strings.TrimPrefix(header.Name, "./")
		if name == "" || strings.HasPrefix(name, "/") || strings.Contains(name, "\\") || filepath.ToSlash(filepath.Clean(name)) != name || name == ".." || strings.HasPrefix(name, "../") {
			return "", fmt.Errorf("unsafe release archive path")
		}
		if header.Typeflag == tar.TypeDir {
			continue
		}
		if header.Typeflag != tar.TypeReg {
			return "", fmt.Errorf("release archive contains a non-regular entry")
		}
		if name != binary {
			continue
		}
		if found {
			return "", fmt.Errorf("release archive contains duplicate binaries")
		}
		found = true
		file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0755)
		if err != nil {
			return "", err
		}
		hash := sha256.New()
		_, copyErr := io.Copy(io.MultiWriter(file, hash), archive)
		if copyErr == nil {
			copyErr = file.Sync()
		}
		closeErr := file.Close()
		if copyErr != nil {
			return "", copyErr
		}
		if closeErr != nil {
			return "", closeErr
		}
		digest = hex.EncodeToString(hash.Sum(nil))
	}
	// Reading the gzip trailer verifies the compressed stream after tar's EOF.
	if n, err := io.Copy(io.Discard, io.LimitReader(reader, (1<<20)+1)); err != nil {
		return "", err
	} else if n > 1<<20 {
		return "", fmt.Errorf("release archive has excessive trailing data")
	}
	if !found {
		return "", fmt.Errorf("release archive does not contain %s", binary)
	}
	return digest, nil
}

func fileDigest(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0100 == 0 {
		return "", fmt.Errorf("invalid cached release executable")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, io.LimitReader(file, maxArchiveBytes+1)); err != nil {
		return "", err
	}
	if info.Size() > maxArchiveBytes {
		return "", fmt.Errorf("cached release executable exceeds size limit")
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func cached(root, tag, arch string) (Pair, error) {
	pair := Pair{Client: filepath.Join(root, "agentlab"), Daemon: filepath.Join(root, "agentlabd"), Tag: tag, Architecture: arch}
	info, err := os.Lstat(root)
	if err != nil {
		return pair, err
	}
	if !info.IsDir() {
		return pair, fmt.Errorf("invalid release cache directory")
	}
	manifestInfo, err := os.Lstat(filepath.Join(root, "manifest.json"))
	if err != nil {
		return pair, err
	}
	if !manifestInfo.Mode().IsRegular() || manifestInfo.Size() > maxChecksumBytes {
		return pair, fmt.Errorf("invalid release cache manifest")
	}
	data, err := os.ReadFile(filepath.Join(root, "manifest.json"))
	if err != nil {
		return pair, err
	}
	var manifest manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return pair, err
	}
	if manifest.Tag != tag || manifest.Architecture != arch {
		return pair, fmt.Errorf("release cache identity mismatch")
	}
	for path, expected := range map[string]string{pair.Client: manifest.ClientSHA256, pair.Daemon: manifest.DaemonSHA256} {
		digest, err := fileDigest(path)
		if err != nil {
			return pair, err
		}
		if digest != expected {
			return pair, fmt.Errorf("cached release executable changed; remove its cache directory and retry")
		}
	}
	return pair, nil
}

// Fetch verifies both archives against checksums.txt before extracting binaries.
// Cache reads perform no network requests, so a verified cache works offline.
func (d Downloader) Fetch(ctx context.Context, version, arch string) (Pair, error) {
	var pair Pair
	tag, err := Tag(version)
	if err != nil {
		return pair, err
	}
	if arch != "amd64" && arch != "arm64" {
		return pair, fmt.Errorf("unsupported release architecture")
	}
	base := d.BaseURL
	if base == "" {
		base = DefaultBaseURL
	}
	base = strings.TrimRight(base, "/")
	if !safeURL(base) {
		return pair, fmt.Errorf("release downloads require HTTPS")
	}
	key := sha256.Sum256([]byte(base + "\x00" + tag + "\x00" + arch))
	id := hex.EncodeToString(key[:])
	root := filepath.Join(d.Root, id)
	if err := os.MkdirAll(d.Root, 0700); err != nil {
		return pair, err
	}
	lock, err := os.OpenFile(filepath.Join(d.Root, id+".lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return pair, err
	}
	defer lock.Close()
	// Nonblocking acquisition keeps cancelled clients from waiting behind downloads.
	for {
		err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			return pair, err
		}
		timer := time.NewTimer(100 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return pair, ctx.Err()
		case <-timer.C:
		}
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	// This request lock owns all temporary directories with this digest prefix.
	// Remove only incomplete downloads left by a terminated process.
	entries, err := os.ReadDir(d.Root)
	if err != nil {
		return pair, err
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".release-"+id+"-") {
			if err := os.RemoveAll(filepath.Join(d.Root, entry.Name())); err != nil {
				return pair, err
			}
		}
	}
	if _, err := os.Lstat(root); err == nil {
		return cached(root, tag, arch)
	} else if !os.IsNotExist(err) {
		return pair, err
	}
	checksums, err := d.download(ctx, base+"/"+tag+"/checksums.txt", maxChecksumBytes)
	if err != nil {
		return pair, err
	}
	temp, err := os.MkdirTemp(d.Root, ".release-"+id+"-*")
	if err != nil {
		return pair, err
	}
	defer os.RemoveAll(temp)
	record := manifest{Tag: tag, Architecture: arch, Archives: map[string]string{}}
	for _, binary := range []string{"agentlab", "agentlabd"} {
		name := fmt.Sprintf("%s_%s_linux-%s.tar.gz", binary, tag, arch)
		expected, err := checksum(checksums, name)
		if err != nil {
			return pair, err
		}
		data, err := d.download(ctx, base+"/"+tag+"/"+name, maxArchiveBytes)
		if err != nil {
			return pair, err
		}
		digest := sha256.Sum256(data)
		if hex.EncodeToString(digest[:]) != expected {
			return pair, fmt.Errorf("release archive checksum mismatch: %s", name)
		}
		binaryHash, err := extract(data, binary, filepath.Join(temp, binary))
		if err != nil {
			return pair, err
		}
		record.Archives[name] = expected
		if binary == "agentlab" {
			record.ClientSHA256 = binaryHash
		} else {
			record.DaemonSHA256 = binaryHash
		}
	}
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return pair, err
	}
	if err := os.WriteFile(filepath.Join(temp, "manifest.json"), data, 0600); err != nil {
		return pair, err
	}
	if err := os.Rename(temp, root); err != nil {
		return pair, err
	}
	return cached(root, tag, arch)
}
