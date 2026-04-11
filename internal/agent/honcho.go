package agent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"time"
)

const (
	defaultHonchoBaseURL = "https://api.honcho.dev"
	honchoCollectionName = "memory"
	honchoProfileKey     = "user_profile"
)

// HonchoProvider implements MemoryProvider using the Honcho API
// for cloud-based memory storage across sessions.
//
// Compile-time interface check.
var _ MemoryProvider = (*HonchoProvider)(nil)

// Compile-time lifecycle interface checks.
var _ PrefetchProvider = (*HonchoProvider)(nil)
var _ TurnSyncProvider = (*HonchoProvider)(nil)

type HonchoProvider struct {
	appID   string
	userID  string
	client  *http.Client
	baseURL string
	apiKey  string
}

// NewHonchoProvider creates a new HonchoProvider configured from environment
// variables. Uses HONCHO_API_KEY for authentication and HONCHO_BASE_URL
// (optional, defaults to https://api.honcho.dev) for the API endpoint.
func NewHonchoProvider(appID, userID string) *HonchoProvider {
	baseURL := os.Getenv("HONCHO_BASE_URL")
	if baseURL == "" {
		baseURL = defaultHonchoBaseURL
	}

	apiKey := os.Getenv("HONCHO_API_KEY")

	// Allow HONCHO_APP_ID env var to override the parameter.
	if envAppID := os.Getenv("HONCHO_APP_ID"); envAppID != "" && appID == "" {
		appID = envAppID
	}

	return &HonchoProvider{
		appID:   appID,
		userID:  userID,
		client:  &http.Client{Timeout: 30 * time.Second},
		baseURL: baseURL,
		apiKey:  apiKey,
	}
}

// honchoDocument represents a document in a Honcho collection.
type honchoDocument struct {
	ID       string         `json:"id,omitempty"`
	Content  string         `json:"content"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

// honchoDocumentsResponse represents the response from listing documents.
type honchoDocumentsResponse struct {
	Items []honchoDocument `json:"items"`
}

// ReadMemory retrieves all documents from the memory collection for this user.
func (h *HonchoProvider) ReadMemory() (string, error) {
	url := fmt.Sprintf("%s/apps/%s/users/%s/collections/%s/documents",
		h.baseURL, h.appID, h.userID, honchoCollectionName)

	body, err := h.doRequest("GET", url, nil)
	if err != nil {
		return "", fmt.Errorf("honcho read memory: %w", err)
	}

	var resp honchoDocumentsResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return "", fmt.Errorf("honcho parse response: %w", err)
	}

	// Concatenate all documents into a single string.
	var result string
	for _, doc := range resp.Items {
		key := ""
		if doc.Metadata != nil {
			if k, ok := doc.Metadata["key"].(string); ok {
				key = k
			}
		}
		if key != "" {
			result += fmt.Sprintf("## %s\n\n%s\n\n", key, doc.Content)
		} else {
			result += doc.Content + "\n\n"
		}
	}

	return result, nil
}

// SaveMemory saves a key/content pair as a document in the memory collection.
func (h *HonchoProvider) SaveMemory(key, content string) error {
	if key == "" || content == "" {
		return fmt.Errorf("both key and content are required")
	}

	// First check if a document with this key already exists.
	existingID, err := h.findDocumentByKey(key)
	if err != nil {
		return err
	}

	doc := honchoDocument{
		Content: content,
		Metadata: map[string]any{
			"key":        key,
			"updated_at": time.Now().Format(time.RFC3339),
		},
	}

	payload, err := json.Marshal(doc)
	if err != nil {
		return fmt.Errorf("marshal document: %w", err)
	}

	if existingID != "" {
		// Update existing document.
		url := fmt.Sprintf("%s/apps/%s/users/%s/collections/%s/documents/%s",
			h.baseURL, h.appID, h.userID, honchoCollectionName, existingID)
		_, err = h.doRequest("PUT", url, payload)
	} else {
		// Create new document.
		url := fmt.Sprintf("%s/apps/%s/users/%s/collections/%s/documents",
			h.baseURL, h.appID, h.userID, honchoCollectionName)
		_, err = h.doRequest("POST", url, payload)
	}

	if err != nil {
		return fmt.Errorf("honcho save memory: %w", err)
	}

	return nil
}

// DeleteMemory removes a document from the memory collection by key.
func (h *HonchoProvider) DeleteMemory(key string) error {
	if key == "" {
		return fmt.Errorf("key is required")
	}

	docID, err := h.findDocumentByKey(key)
	if err != nil {
		return err
	}

	if docID == "" {
		return fmt.Errorf("memory key '%s' not found", key)
	}

	url := fmt.Sprintf("%s/apps/%s/users/%s/collections/%s/documents/%s",
		h.baseURL, h.appID, h.userID, honchoCollectionName, docID)

	_, err = h.doRequest("DELETE", url, nil)
	if err != nil {
		return fmt.Errorf("honcho delete memory: %w", err)
	}

	return nil
}

// ReadUserProfile retrieves the user profile from the Honcho user metadata.
func (h *HonchoProvider) ReadUserProfile() (string, error) {
	url := fmt.Sprintf("%s/apps/%s/users/%s/collections/%s/documents",
		h.baseURL, h.appID, h.userID, honchoCollectionName)

	body, err := h.doRequest("GET", url, nil)
	if err != nil {
		return "", fmt.Errorf("honcho read profile: %w", err)
	}

	var resp honchoDocumentsResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return "", fmt.Errorf("honcho parse response: %w", err)
	}

	// Find the profile document.
	for _, doc := range resp.Items {
		if doc.Metadata != nil {
			if k, ok := doc.Metadata["key"].(string); ok && k == honchoProfileKey {
				return doc.Content, nil
			}
		}
	}

	return "", nil
}

// SaveUserProfile saves the user profile as a special document in the
// memory collection.
func (h *HonchoProvider) SaveUserProfile(content string) error {
	if content == "" {
		return fmt.Errorf("content is required")
	}
	return h.SaveMemory(honchoProfileKey, content)
}

// Prefetch warms the Honcho cache by issuing a read for the user's
// memory collection. The query parameter is logged but currently unused
// by the Honcho API (all documents are fetched).
func (h *HonchoProvider) Prefetch(query string) error {
	slog.Debug("Honcho prefetch", "query_len", len(query))
	_, err := h.ReadMemory()
	if err != nil {
		return fmt.Errorf("honcho prefetch: %w", err)
	}
	return nil
}

// SyncTurn persists a conversation turn as a document in the Honcho
// memory collection so it can be retrieved in future sessions.
func (h *HonchoProvider) SyncTurn(userMsg, assistantMsg string) error {
	if userMsg == "" && assistantMsg == "" {
		return nil
	}

	content := fmt.Sprintf("User: %s\nAssistant: %s", userMsg, assistantMsg)
	key := fmt.Sprintf("turn_%d", time.Now().UnixMilli())

	slog.Debug("Honcho sync turn", "key", key)
	return h.SaveMemory(key, content)
}

// findDocumentByKey searches for a document with the given key in its metadata.
// Returns the document ID if found, empty string otherwise.
func (h *HonchoProvider) findDocumentByKey(key string) (string, error) {
	url := fmt.Sprintf("%s/apps/%s/users/%s/collections/%s/documents",
		h.baseURL, h.appID, h.userID, honchoCollectionName)

	body, err := h.doRequest("GET", url, nil)
	if err != nil {
		return "", err
	}

	var resp honchoDocumentsResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return "", err
	}

	for _, doc := range resp.Items {
		if doc.Metadata != nil {
			if k, ok := doc.Metadata["key"].(string); ok && k == key {
				return doc.ID, nil
			}
		}
	}

	return "", nil
}

// doRequest executes an HTTP request against the Honcho API.
func (h *HonchoProvider) doRequest(method, url string, payload []byte) ([]byte, error) {
	var bodyReader io.Reader
	if payload != nil {
		bodyReader = bytes.NewReader(payload)
	}

	req, err := http.NewRequest(method, url, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "HermesAgent/1.0")

	if h.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+h.apiKey)
	}

	resp, err := h.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response body: %w", err)
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("Honcho API error %d: %s", resp.StatusCode, string(body))
	}

	return body, nil
}
