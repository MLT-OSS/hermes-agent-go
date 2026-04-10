package tools

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
)

// sseEvent represents a parsed Server-Sent Event.
type sseEvent struct {
	Event string // event type (default: "message")
	Data  string // event data (may span multiple lines)
	ID    string // optional event ID
}

// sseTransportV2 implements mcpTransport over HTTP/SSE.
// Lifecycle: Connect → goroutine reads SSE stream → Send POSTs JSON-RPC →
// Receive reads from channel → Close cancels context and waits for goroutine.
type sseTransportV2 struct {
	url        string
	headers    map[string]string
	httpClient *http.Client

	// respCh buffers incoming JSON-RPC responses from the SSE stream.
	// Size 16: SSE responses may arrive in bursts (tool list, multi-result);
	// a buffer prevents blocking the reader goroutine. Overflow is not
	// possible because the reader blocks on channel send.
	respCh chan *jsonRPCResponse

	// notifyCh receives MCP notifications (tools/list_changed, etc.).
	// Size 8: notifications are infrequent but may arrive while the agent
	// is busy processing a tool call. A small buffer prevents drops.
	notifyCh chan jsonRPCRequest

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	// postURL is the endpoint for sending JSON-RPC requests (derived from SSE URL).
	postURL string
}

func newSSETransportV2(url string, headers map[string]string) *sseTransportV2 {
	postURL := url
	if strings.HasSuffix(postURL, "/sse") {
		postURL = strings.TrimSuffix(postURL, "/sse") + "/message"
	}

	return &sseTransportV2{
		url:        url,
		headers:    headers,
		httpClient: &http.Client{Timeout: 0}, // no timeout for SSE long-poll
		respCh:     make(chan *jsonRPCResponse, 16),
		notifyCh:   make(chan jsonRPCRequest, 8),
		postURL:    postURL,
	}
}

// Connect establishes the SSE stream and starts the reader goroutine.
func (t *sseTransportV2) Connect(ctx context.Context) error {
	t.ctx, t.cancel = context.WithCancel(ctx)

	req, err := http.NewRequestWithContext(t.ctx, "GET", t.url, nil)
	if err != nil {
		return fmt.Errorf("create sse request: %w", err)
	}
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Cache-Control", "no-cache")
	for k, v := range t.headers {
		req.Header.Set(k, v)
	}

	resp, err := t.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("sse connect: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return fmt.Errorf("sse server returned %d", resp.StatusCode)
	}

	// Start reader goroutine with tracked lifecycle (rule 4).
	t.wg.Add(1)
	go func() {
		defer t.wg.Done()
		defer resp.Body.Close()
		t.readSSEStream(resp)
	}()

	return nil
}

// readSSEStream parses SSE events and routes them to respCh or notifyCh.
func (t *sseTransportV2) readSSEStream(resp *http.Response) {
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 256*1024), 10*1024*1024)

	var current sseEvent
	var dataLines []string

	for scanner.Scan() {
		line := scanner.Text()

		if line == "" {
			// Empty line = end of event.
			if len(dataLines) > 0 {
				current.Data = strings.Join(dataLines, "\n")
				t.dispatchEvent(current)
				current = sseEvent{}
				dataLines = nil
			}
			continue
		}

		if strings.HasPrefix(line, "event: ") {
			current.Event = strings.TrimPrefix(line, "event: ")
		} else if strings.HasPrefix(line, "data: ") {
			dataLines = append(dataLines, strings.TrimPrefix(line, "data: "))
		} else if strings.HasPrefix(line, "id: ") {
			current.ID = strings.TrimPrefix(line, "id: ")
		}
		// Lines starting with ':' are comments — ignore.
	}

	if err := scanner.Err(); err != nil {
		// Only log if not cancelled.
		select {
		case <-t.ctx.Done():
		default:
			slog.Warn("sse stream read error", "error", err)
		}
	}
}

// dispatchEvent routes a parsed SSE event to the appropriate channel.
func (t *sseTransportV2) dispatchEvent(evt sseEvent) {
	if evt.Data == "" {
		return
	}

	// Try to parse as JSON-RPC.
	var msg json.RawMessage
	if err := json.Unmarshal([]byte(evt.Data), &msg); err != nil {
		slog.Debug("sse non-json event", "type", evt.Event, "data_len", len(evt.Data))
		return
	}

	// Check if it's a notification (no "id" field in JSON-RPC) or response.
	var probe struct {
		ID     any    `json:"id"`
		Method string `json:"method"`
	}
	json.Unmarshal([]byte(evt.Data), &probe)

	if probe.Method != "" && probe.ID == nil {
		// Notification.
		var notif jsonRPCRequest
		if err := json.Unmarshal([]byte(evt.Data), &notif); err == nil {
			select {
			case t.notifyCh <- notif:
			default:
				slog.Warn("mcp notification channel full, dropping", "method", notif.Method)
			}
		}
		return
	}

	// Response.
	var rpcResp jsonRPCResponse
	if err := json.Unmarshal([]byte(evt.Data), &rpcResp); err != nil {
		slog.Warn("sse json-rpc parse error", "error", err)
		return
	}

	select {
	case t.respCh <- &rpcResp:
	case <-t.ctx.Done():
	}
}

// Send POSTs a JSON-RPC request to the MCP server.
func (t *sseTransportV2) Send(req jsonRPCRequest) error {
	data, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(t.ctx, "POST", t.postURL, strings.NewReader(string(data)))
	if err != nil {
		return fmt.Errorf("create post request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	for k, v := range t.headers {
		httpReq.Header.Set(k, v)
	}

	resp, err := t.httpClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("post request: %w", err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		return fmt.Errorf("mcp server returned %d", resp.StatusCode)
	}

	return nil
}

// Receive blocks until a JSON-RPC response arrives or context is cancelled.
func (t *sseTransportV2) Receive() (*jsonRPCResponse, error) {
	select {
	case resp := <-t.respCh:
		return resp, nil
	case <-t.ctx.Done():
		return nil, fmt.Errorf("sse transport closed")
	case <-time.After(60 * time.Second):
		return nil, fmt.Errorf("sse receive timeout")
	}
}

// Notifications returns the notification channel for tools/list_changed etc.
func (t *sseTransportV2) Notifications() <-chan jsonRPCRequest {
	return t.notifyCh
}

// Close cancels the context and waits for the reader goroutine to exit.
func (t *sseTransportV2) Close() error {
	if t.cancel != nil {
		t.cancel()
	}
	t.wg.Wait()
	return nil
}
