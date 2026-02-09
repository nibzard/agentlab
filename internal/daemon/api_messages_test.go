package daemon

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
)

func TestMessageHandlers(t *testing.T) {
	store := newTestStore(t)
	api := NewControlAPI(store, nil, nil, nil, nil, "", nil)

	postBody := bytes.NewBufferString(`{"scope_type":"job","scope_id":"job-123","author":"alice","kind":"note","text":"hello"}`)
	postReq := httptest.NewRequest(http.MethodPost, "/v1/messages", postBody)
	postRec := httptest.NewRecorder()
	api.handleMessages(postRec, postReq)
	if postRec.Code != http.StatusCreated {
		t.Fatalf("post status = %d, want %d", postRec.Code, http.StatusCreated)
	}
	var created V1Message
	if err := json.NewDecoder(postRec.Body).Decode(&created); err != nil {
		t.Fatalf("decode post response: %v", err)
	}
	if created.ID == 0 {
		t.Fatalf("expected created message id")
	}

	postBody2 := bytes.NewBufferString(`{"scope_type":"job","scope_id":"job-123","author":"bob","kind":"note","text":"second"}`)
	postReq2 := httptest.NewRequest(http.MethodPost, "/v1/messages", postBody2)
	postRec2 := httptest.NewRecorder()
	api.handleMessages(postRec2, postReq2)
	if postRec2.Code != http.StatusCreated {
		t.Fatalf("post2 status = %d, want %d", postRec2.Code, http.StatusCreated)
	}

	listReq := httptest.NewRequest(http.MethodGet, "/v1/messages?scope_type=job&scope_id=job-123&limit=1", nil)
	listRec := httptest.NewRecorder()
	api.handleMessages(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("list status = %d, want %d", listRec.Code, http.StatusOK)
	}
	var listResp V1MessagesResponse
	if err := json.NewDecoder(listRec.Body).Decode(&listResp); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	if len(listResp.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(listResp.Messages))
	}
	if listResp.Messages[0].ID <= created.ID {
		t.Fatalf("expected tail message to be newer than first")
	}

	afterReq := httptest.NewRequest(http.MethodGet, "/v1/messages?scope_type=job&scope_id=job-123&after_id="+
		intToString(created.ID)+"&limit=10", nil)
	afterRec := httptest.NewRecorder()
	api.handleMessages(afterRec, afterReq)
	if afterRec.Code != http.StatusOK {
		t.Fatalf("after status = %d, want %d", afterRec.Code, http.StatusOK)
	}
	var afterResp V1MessagesResponse
	if err := json.NewDecoder(afterRec.Body).Decode(&afterResp); err != nil {
		t.Fatalf("decode after response: %v", err)
	}
	if len(afterResp.Messages) != 1 {
		t.Fatalf("expected 1 message after id, got %d", len(afterResp.Messages))
	}
	if afterResp.Messages[0].ID <= created.ID {
		t.Fatalf("expected message id after %d", created.ID)
	}
}

func TestMessageHandlerValidation(t *testing.T) {
	store := newTestStore(t)
	api := NewControlAPI(store, nil, nil, nil, nil, "", nil)

	postBody := bytes.NewBufferString(`{"scope_type":"invalid","scope_id":"job-123","text":"hello"}`)
	postReq := httptest.NewRequest(http.MethodPost, "/v1/messages", postBody)
	postRec := httptest.NewRecorder()
	api.handleMessages(postRec, postReq)
	if postRec.Code != http.StatusBadRequest {
		t.Fatalf("post invalid scope status = %d, want %d", postRec.Code, http.StatusBadRequest)
	}

	listReq := httptest.NewRequest(http.MethodGet, "/v1/messages?scope_type=job&scope_id=", nil)
	listRec := httptest.NewRecorder()
	api.handleMessages(listRec, listReq)
	if listRec.Code != http.StatusBadRequest {
		t.Fatalf("list missing scope_id status = %d, want %d", listRec.Code, http.StatusBadRequest)
	}
}

func intToString(value int64) string {
	return strconv.FormatInt(value, 10)
}
