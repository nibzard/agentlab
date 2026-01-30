package daemon

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/agentlab/agentlab/internal/db"
)

const (
	defaultArtifactName = "agentlab-artifacts.tar.gz"
)

// ArtifactAPI handles artifact uploads from guest runners.
type ArtifactAPI struct {
	store       *db.Store
	rootDir     string
	maxBytes    int64
	now         func() time.Time
	agentSubnet *net.IPNet
}

func NewArtifactAPI(store *db.Store, rootDir string, maxBytes int64, agentSubnet *net.IPNet) *ArtifactAPI {
	api := &ArtifactAPI{
		store:       store,
		rootDir:     strings.TrimSpace(rootDir),
		maxBytes:    maxBytes,
		now:         time.Now,
		agentSubnet: agentSubnet,
	}
	return api
}

func (api *ArtifactAPI) Register(mux *http.ServeMux) {
	if mux == nil {
		return
	}
	mux.HandleFunc("/upload", api.handleUpload)
}

func (api *ArtifactAPI) handleUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, []string{http.MethodPost})
		return
	}
	if api.store == nil {
		writeError(w, http.StatusServiceUnavailable, "artifact service unavailable")
		return
	}
	if !api.remoteAllowed(r.RemoteAddr) {
		writeError(w, http.StatusForbidden, "artifact access restricted to agent subnet")
		return
	}
	token := bearerToken(r.Header.Get("Authorization"))
	if token == "" {
		writeError(w, http.StatusUnauthorized, "missing bearer token")
		return
	}
	tokenHash, err := db.HashArtifactToken(token)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid token")
		return
	}
	record, err := api.store.GetArtifactToken(r.Context(), tokenHash)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusForbidden, "invalid artifact token")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to validate artifact token")
		return
	}
	now := api.now().UTC()
	if !record.ExpiresAt.IsZero() && !record.ExpiresAt.After(now) {
		writeError(w, http.StatusForbidden, "artifact token expired")
		return
	}
	jobID := strings.TrimSpace(record.JobID)
	if jobID == "" {
		writeError(w, http.StatusInternalServerError, "artifact token missing job")
		return
	}

	rawPath := strings.TrimSpace(r.URL.Query().Get("path"))
	if rawPath == "" {
		rawPath = defaultArtifactName
	}
	relPath, err := sanitizeArtifactPath(rawPath)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	jobDir, err := api.jobDir(jobID)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	targetPath, err := safeJoin(jobDir, relPath)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := os.MkdirAll(filepath.Dir(targetPath), artifactDirPerms); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create artifact directory")
		return
	}

	maxBytes := api.maxBytes
	if maxBytes <= 0 {
		maxBytes = 256 * 1024 * 1024
	}
	if r.Body == nil {
		writeError(w, http.StatusBadRequest, "request body is required")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
	defer r.Body.Close()

	tmpPath := targetPath + ".tmp-" + randomSuffix()
	file, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o640)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create artifact file")
		return
	}
	defer func() {
		_ = file.Close()
	}()

	hash := sha256.New()
	writer := io.MultiWriter(file, hash)

	var sniff [512]byte
	n, readErr := r.Body.Read(sniff[:])
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		handleUploadReadError(w, readErr)
		_ = os.Remove(tmpPath)
		return
	}
	var size int64
	if n > 0 {
		if _, err := writer.Write(sniff[:n]); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to write artifact")
			_ = os.Remove(tmpPath)
			return
		}
		size += int64(n)
	}

	copied, err := io.Copy(writer, r.Body)
	size += copied
	if err != nil {
		handleUploadReadError(w, err)
		_ = os.Remove(tmpPath)
		return
	}
	if size == 0 {
		writeError(w, http.StatusBadRequest, "artifact body is empty")
		_ = os.Remove(tmpPath)
		return
	}
	if err := file.Sync(); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to persist artifact")
		_ = os.Remove(tmpPath)
		return
	}
	if err := file.Close(); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to close artifact")
		_ = os.Remove(tmpPath)
		return
	}
	if err := os.Rename(tmpPath, targetPath); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to finalize artifact")
		_ = os.Remove(tmpPath)
		return
	}

	sha := hex.EncodeToString(hash.Sum(nil))
	mime := cleanContentType(r.Header.Get("Content-Type"))
	if mime == "" {
		mime = http.DetectContentType(sniff[:n])
	}
	artifact := db.Artifact{
		JobID:     jobID,
		VMID:      record.VMID,
		Name:      filepath.Base(relPath),
		Path:      relPath,
		SizeBytes: size,
		Sha256:    sha,
		MIME:      mime,
		CreatedAt: now,
	}
	id, err := api.store.CreateArtifact(r.Context(), artifact)
	if err != nil {
		_ = os.Remove(targetPath)
		writeError(w, http.StatusInternalServerError, "failed to record artifact")
		return
	}
	artifact.ID = id

	if record.VMID != nil {
		_ = api.store.RecordEvent(r.Context(), "artifact.upload", record.VMID, &jobID, fmt.Sprintf("artifact uploaded: %s", relPath), "")
	}
	_ = api.store.TouchArtifactToken(r.Context(), record.TokenHash, now)

	resp := V1ArtifactUploadResponse{
		JobID: jobID,
		Artifact: V1ArtifactMetadata{
			Name:      artifact.Name,
			Path:      artifact.Path,
			SizeBytes: artifact.SizeBytes,
			Sha256:    artifact.Sha256,
			MIME:      artifact.MIME,
		},
	}
	writeJSON(w, http.StatusCreated, resp)
}

func (api *ArtifactAPI) jobDir(jobID string) (string, error) {
	jobID = strings.TrimSpace(jobID)
	if jobID == "" {
		return "", errors.New("job id is required")
	}
	if strings.Contains(jobID, "/") || strings.Contains(jobID, string(filepath.Separator)) {
		return "", errors.New("job id contains invalid path characters")
	}
	root := strings.TrimSpace(api.rootDir)
	if root == "" {
		return "", errors.New("artifact root is not configured")
	}
	return filepath.Join(root, jobID), nil
}

func (api *ArtifactAPI) remoteAllowed(addr string) bool {
	if api.agentSubnet == nil {
		return true
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	host = strings.Trim(host, "[]")
	ip := net.ParseIP(host)
	if ip == nil || ip.IsUnspecified() {
		return false
	}
	return api.agentSubnet.Contains(ip)
}

func sanitizeArtifactPath(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("artifact path is required")
	}
	if strings.ContainsRune(raw, 0) {
		return "", errors.New("artifact path contains invalid characters")
	}
	raw = strings.ReplaceAll(raw, "\\", "/")
	cleaned := filepath.Clean(raw)
	if cleaned == "." || cleaned == "" {
		return "", errors.New("artifact path is required")
	}
	if filepath.IsAbs(cleaned) {
		return "", errors.New("artifact path must be relative")
	}
	if cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) || strings.HasPrefix(cleaned, "..") {
		return "", errors.New("artifact path must not traverse")
	}
	return cleaned, nil
}

func safeJoin(root, rel string) (string, error) {
	root = filepath.Clean(root)
	target := filepath.Join(root, rel)
	relPath, err := filepath.Rel(root, target)
	if err != nil {
		return "", errors.New("artifact path is invalid")
	}
	if relPath == "." || strings.HasPrefix(relPath, "..") {
		return "", errors.New("artifact path must remain within job directory")
	}
	return target, nil
}

func bearerToken(header string) string {
	header = strings.TrimSpace(header)
	if header == "" {
		return ""
	}
	lower := strings.ToLower(header)
	if !strings.HasPrefix(lower, "bearer ") {
		return ""
	}
	return strings.TrimSpace(header[len("bearer "):])
}

func cleanContentType(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if idx := strings.Index(value, ";"); idx >= 0 {
		value = value[:idx]
	}
	return strings.TrimSpace(value)
}

func randomSuffix() string {
	buf := make([]byte, 6)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf)
}

func handleUploadReadError(w http.ResponseWriter, err error) {
	var maxErr *http.MaxBytesError
	switch {
	case errors.As(err, &maxErr):
		writeError(w, http.StatusRequestEntityTooLarge, "artifact exceeds size limit")
	case errors.Is(err, io.EOF):
		writeError(w, http.StatusBadRequest, "artifact body is empty")
	default:
		writeError(w, http.StatusInternalServerError, "failed to read artifact")
	}
}
