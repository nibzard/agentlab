package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/agentlab/agentlab/internal/models"
	testutil "github.com/agentlab/agentlab/internal/testing"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeExposurePublisher struct {
	publishResult  ExposurePublishResult
	publishErr     error
	unpublishErr   error
	publishCalls   int
	unpublishCalls int
}

func (f *fakeExposurePublisher) Publish(_ context.Context, _ string, _ string, _ int) (ExposurePublishResult, error) {
	f.publishCalls++
	if f.publishErr != nil {
		return ExposurePublishResult{}, f.publishErr
	}
	return f.publishResult, nil
}

func (f *fakeExposurePublisher) Unpublish(_ context.Context, _ string, _ int) error {
	f.unpublishCalls++
	return f.unpublishErr
}

func TestExposureHandlersLifecycle(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	publisher := &fakeExposurePublisher{publishResult: ExposurePublishResult{URL: "tcp://tailnet.example:8080", State: exposureStateServing}}
	api := NewControlAPI(store, map[string]models.Profile{}, nil, nil, nil, "", log.New(io.Discard, "", 0)).
		WithExposurePublisher(publisher)

	fixed := time.Date(2026, time.February, 8, 18, 0, 0, 0, time.UTC)
	api.now = func() time.Time { return fixed }

	sandbox := testutil.NewTestSandbox(testutil.SandboxOpts{
		VMID:          501,
		Name:          "exposure-sb",
		State:         models.SandboxRunning,
		IP:            "10.77.0.10",
		CreatedAt:     fixed,
		LastUpdatedAt: fixed,
	})
	require.NoError(t, store.CreateSandbox(ctx, sandbox))

	body := bytes.NewBufferString(`{"name":"web-501","vmid":501,"port":8080}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/exposures", body)
	rec := httptest.NewRecorder()
	api.handleExposures(rec, req)

	require.Equal(t, http.StatusCreated, rec.Code)
	var created V1Exposure
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&created))
	assert.Equal(t, "web-501", created.Name)
	assert.Equal(t, 501, created.VMID)
	assert.Equal(t, 8080, created.Port)
	assert.Equal(t, "10.77.0.10", created.TargetIP)
	assert.Equal(t, exposureStateServing, created.State)
	assert.Equal(t, "tcp://tailnet.example:8080", created.URL)
	assert.Equal(t, fixed.UTC().Format(time.RFC3339Nano), created.CreatedAt)
	assert.Equal(t, fixed.UTC().Format(time.RFC3339Nano), created.UpdatedAt)

	listReq := httptest.NewRequest(http.MethodGet, "/v1/exposures", nil)
	listRec := httptest.NewRecorder()
	api.handleExposures(listRec, listReq)
	require.Equal(t, http.StatusOK, listRec.Code)
	var listResp V1ExposuresResponse
	require.NoError(t, json.NewDecoder(listRec.Body).Decode(&listResp))
	require.Len(t, listResp.Exposures, 1)
	assert.Equal(t, "web-501", listResp.Exposures[0].Name)

	delReq := httptest.NewRequest(http.MethodDelete, "/v1/exposures/web-501", nil)
	delRec := httptest.NewRecorder()
	api.handleExposureByName(delRec, delReq)
	require.Equal(t, http.StatusOK, delRec.Code)
	var delResp V1Exposure
	require.NoError(t, json.NewDecoder(delRec.Body).Decode(&delResp))
	assert.Equal(t, "web-501", delResp.Name)
	assert.Equal(t, 1, publisher.publishCalls)
	assert.Equal(t, 1, publisher.unpublishCalls)

	listReq = httptest.NewRequest(http.MethodGet, "/v1/exposures", nil)
	listRec = httptest.NewRecorder()
	api.handleExposures(listRec, listReq)
	require.Equal(t, http.StatusOK, listRec.Code)
	listResp = V1ExposuresResponse{}
	require.NoError(t, json.NewDecoder(listRec.Body).Decode(&listResp))
	assert.Empty(t, listResp.Exposures)

	events, err := store.ListEventsBySandbox(ctx, sandbox.VMID, 0, 10)
	require.NoError(t, err)
	require.Len(t, events, 2)
	assert.Equal(t, "exposure.create", events[0].Kind)
	assert.Equal(t, "exposure.delete", events[1].Kind)
}

func TestExposureHandlersErrors(t *testing.T) {
	store := newTestStore(t)
	publisher := &fakeExposurePublisher{}
	api := NewControlAPI(store, map[string]models.Profile{}, nil, nil, nil, "", log.New(io.Discard, "", 0)).
		WithExposurePublisher(publisher)

	body := bytes.NewBufferString(`{"name":"bad","vmid":999,"port":8080}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/exposures", body)
	rec := httptest.NewRecorder()
	api.handleExposures(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code)

	invalidPort := bytes.NewBufferString(`{"name":"bad","vmid":1,"port":70000}`)
	req = httptest.NewRequest(http.MethodPost, "/v1/exposures", invalidPort)
	rec = httptest.NewRecorder()
	api.handleExposures(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)

	delReq := httptest.NewRequest(http.MethodDelete, "/v1/exposures/missing", nil)
	delRec := httptest.NewRecorder()
	api.handleExposureByName(delRec, delReq)
	require.Equal(t, http.StatusNotFound, delRec.Code)
}
