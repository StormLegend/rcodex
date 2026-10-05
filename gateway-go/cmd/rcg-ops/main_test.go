package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSendAlertPostsJSON(t *testing.T) {
	var method, contentType, body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method = r.Method
		contentType = r.Header.Get("Content-Type")
		b, _ := io.ReadAll(r.Body)
		body = string(b)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	sendAlert(context.Background(), srv.URL, map[string]any{"degraded": true, "target": "go"})
	if method != http.MethodPost || contentType != "application/json" {
		t.Fatalf("alert request method=%q content-type=%q", method, contentType)
	}
	if body != `{"degraded":true,"target":"go"}` {
		t.Fatalf("alert body=%q", body)
	}
}
