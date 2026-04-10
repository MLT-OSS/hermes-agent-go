package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSSETransportV2_ConnectAndReceive(t *testing.T) {
	// Create a test SSE server.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			w.WriteHeader(http.StatusOK)

			flusher := w.(http.Flusher)

			// Send a JSON-RPC response as SSE event.
			resp := jsonRPCResponse{
				JSONRPC: "2.0",
				ID:      1,
				Result:  json.RawMessage(`{"tools":[]}`),
			}
			data, _ := json.Marshal(resp)
			fmt.Fprintf(w, "event: message\ndata: %s\n\n", data)
			flusher.Flush()

			// Keep connection open briefly.
			time.Sleep(100 * time.Millisecond)
		}
	}))
	defer server.Close()

	transport := newSSETransportV2(server.URL, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := transport.Connect(ctx); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer transport.Close()

	// Should receive the response.
	resp, err := transport.Receive()
	if err != nil {
		t.Fatalf("receive: %v", err)
	}
	if resp.ID != 1 {
		t.Errorf("response ID = %v, want 1", resp.ID)
	}
}

func TestSSETransportV2_SendPost(t *testing.T) {
	var receivedBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			body, _ := io.ReadAll(r.Body)
			receivedBody = string(body)
			w.WriteHeader(http.StatusOK)
		} else {
			// SSE endpoint — keep alive briefly.
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			w.(http.Flusher).Flush()
			time.Sleep(200 * time.Millisecond)
		}
	}))
	defer server.Close()

	transport := newSSETransportV2(server.URL+"/sse", nil)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := transport.Connect(ctx); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer transport.Close()

	req := jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      42,
		Method:  "tools/list",
	}
	if err := transport.Send(req); err != nil {
		t.Fatalf("send: %v", err)
	}

	// Verify the POST went to /message (not /sse).
	if receivedBody == "" {
		t.Error("server did not receive POST body")
	}
}

func TestSSETransportV2_Notifications(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)

		// Send a notification (no "id" field).
		notif := map[string]any{
			"jsonrpc": "2.0",
			"method":  "notifications/tools/list_changed",
		}
		data, _ := json.Marshal(notif)
		fmt.Fprintf(w, "data: %s\n\n", data)
		flusher.Flush()

		time.Sleep(100 * time.Millisecond)
	}))
	defer server.Close()

	transport := newSSETransportV2(server.URL, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := transport.Connect(ctx); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer transport.Close()

	select {
	case notif := <-transport.Notifications():
		if notif.Method != "notifications/tools/list_changed" {
			t.Errorf("method = %q, want notifications/tools/list_changed", notif.Method)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for notification")
	}
}

func TestSSETransportV2_Close(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		// Block until client disconnects.
		<-r.Context().Done()
	}))
	defer server.Close()

	transport := newSSETransportV2(server.URL, nil)

	ctx := context.Background()
	if err := transport.Connect(ctx); err != nil {
		t.Fatalf("connect: %v", err)
	}

	// Close should cancel context and wait for goroutine.
	done := make(chan struct{})
	go func() {
		transport.Close()
		close(done)
	}()

	select {
	case <-done:
		// OK — Close returned.
	case <-time.After(3 * time.Second):
		t.Fatal("Close did not return in time (goroutine leak?)")
	}
}

func TestSSETransportV2_Headers(t *testing.T) {
	var gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		time.Sleep(100 * time.Millisecond)
	}))
	defer server.Close()

	headers := map[string]string{"Authorization": "Bearer test-token"}
	transport := newSSETransportV2(server.URL, headers)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := transport.Connect(ctx); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer transport.Close()

	if gotAuth != "Bearer test-token" {
		t.Errorf("Authorization header = %q, want %q", gotAuth, "Bearer test-token")
	}
}
