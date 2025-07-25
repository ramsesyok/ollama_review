package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestUsersHandler_CreateAndList(t *testing.T) {
	h := NewUsersHandler()

	// Initially list should be empty
	req := httptest.NewRequest(http.MethodGet, "/users", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}
	var list []User
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("failed to decode list: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("expected empty list, got %v", list)
	}

	// Create a new user
	body := bytes.NewBufferString(`{"name":"Alice"}`)
	req = httptest.NewRequest(http.MethodPost, "/users", body)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d", rec.Code)
	}
	var u User
	if err := json.Unmarshal(rec.Body.Bytes(), &u); err != nil {
		t.Fatalf("failed to decode user: %v", err)
	}
	if u.ID != 1 || u.Name != "Alice" {
		t.Fatalf("unexpected user %+v", u)
	}

	// Verify list now has one user
	req = httptest.NewRequest(http.MethodGet, "/users", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("failed to decode list: %v", err)
	}
	if len(list) != 1 || list[0].Name != "Alice" {
		t.Fatalf("unexpected list %+v", list)
	}
}

func TestUsersHandler_CreateValidation(t *testing.T) {
	h := NewUsersHandler()

	// invalid json
	req := httptest.NewRequest(http.MethodPost, "/users", bytes.NewBufferString("{"))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid json, got %d", rec.Code)
	}

	// missing name
	req = httptest.NewRequest(http.MethodPost, "/users", bytes.NewBufferString(`{"name":""}`))
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for empty name, got %d", rec.Code)
	}
}
