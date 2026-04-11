package tools

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"time"
)

// browserbaseBaseURL is the Browserbase API endpoint.
const browserbaseBaseURL = "https://www.browserbase.com/v1"

// BrowserSession manages a Browserbase CDP session lifecycle.
type BrowserSession struct {
	mu        sync.Mutex
	sessionID string
	apiKey    string
	projectID string
	client    *http.Client

	// Current page state.
	currentURL string
	pageTitle  string
}

// newBrowserbaseSession creates a new BrowserSession for the Browserbase API.
func newBrowserbaseSession() (*BrowserSession, error) {
	apiKey := os.Getenv("BROWSERBASE_API_KEY")
	if apiKey == "" {
		return nil, fmt.Errorf(
			"BROWSERBASE_API_KEY is not set")
	}

	projectID := os.Getenv("BROWSERBASE_PROJECT_ID")

	session := &BrowserSession{
		apiKey:    apiKey,
		projectID: projectID,
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
	}

	if err := session.createSession(); err != nil {
		return nil, fmt.Errorf("create browser session: %w", err)
	}

	return session, nil
}

// createSession creates a new session via the Browserbase API.
func (bs *BrowserSession) createSession() error {
	body := map[string]any{}
	if bs.projectID != "" {
		body["projectId"] = bs.projectID
	}

	resp, err := bs.apiRequest("POST", "/sessions", body)
	if err != nil {
		return err
	}

	sessionID, _ := resp["id"].(string)
	if sessionID == "" {
		return fmt.Errorf("no session ID returned from Browserbase API")
	}

	bs.sessionID = sessionID
	slog.Info("Browserbase session created", "session_id", sessionID)
	return nil
}

// apiRequest makes an authenticated request to the Browserbase API.
func (bs *BrowserSession) apiRequest(method, path string, body any) (map[string]any, error) {
	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal request body: %w", err)
		}
		bodyReader = bytes.NewReader(data)
	}

	url := browserbaseBaseURL + path
	req, err := http.NewRequest(method, url, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+bs.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := bs.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("API request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response body: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("Browserbase API error (HTTP %d): %s", resp.StatusCode, string(respBody))
	}

	var result map[string]any
	if err := json.Unmarshal(respBody, &result); err != nil {
		// Some endpoints return non-JSON; that's OK.
		return map[string]any{"raw": string(respBody)}, nil
	}

	return result, nil
}

// sendCDPCommand sends a Chrome DevTools Protocol command to the session.
func (bs *BrowserSession) sendCDPCommand(method string, params map[string]any) (map[string]any, error) {
	body := map[string]any{
		"method": method,
		"params": params,
	}

	path := fmt.Sprintf("/sessions/%s/cdp", bs.sessionID)
	return bs.apiRequest("POST", path, body)
}

// navigate navigates the browser to a URL.
func (bs *BrowserSession) navigate(url string) (map[string]any, error) {
	bs.mu.Lock()
	defer bs.mu.Unlock()

	result, err := bs.sendCDPCommand("Page.navigate", map[string]any{
		"url": url,
	})
	if err != nil {
		return nil, err
	}

	bs.currentURL = url

	// Wait briefly for the page to load.
	time.Sleep(2 * time.Second)

	return map[string]any{
		"success":    true,
		"url":        url,
		"session_id": bs.sessionID,
		"result":     result,
	}, nil
}

// snapshot takes a screenshot and returns an accessibility tree snapshot.
func (bs *BrowserSession) snapshot() (map[string]any, error) {
	bs.mu.Lock()
	defer bs.mu.Unlock()

	// Get the accessibility tree.
	axTree, err := bs.sendCDPCommand("Accessibility.getFullAXTree", map[string]any{})
	if err != nil {
		slog.Warn("Failed to get accessibility tree", "error", err)
		axTree = map[string]any{"error": err.Error()}
	}

	// Get page title.
	titleResult, err := bs.sendCDPCommand("Runtime.evaluate", map[string]any{
		"expression": "document.title",
	})
	if err == nil {
		if result, ok := titleResult["result"].(map[string]any); ok {
			if value, ok := result["value"].(string); ok {
				bs.pageTitle = value
			}
		}
	}

	return map[string]any{
		"success":            true,
		"url":                bs.currentURL,
		"title":              bs.pageTitle,
		"accessibility_tree": axTree,
	}, nil
}

// click clicks an element by its accessibility tree reference ID.
func (bs *BrowserSession) click(ref string) (map[string]any, error) {
	bs.mu.Lock()
	defer bs.mu.Unlock()

	// Use DOM.querySelector to find the element and click it.
	script := fmt.Sprintf(`
		(function() {
			var el = document.querySelector('[data-ref="%s"]');
			if (!el) {
				// Try finding by aria attributes or other selectors.
				var els = document.querySelectorAll('*');
				for (var i = 0; i < els.length; i++) {
					if (els[i].getAttribute('data-testid') === '%s' ||
						els[i].id === '%s') {
						el = els[i];
						break;
					}
				}
			}
			if (el) {
				el.click();
				return 'clicked: ' + el.tagName;
			}
			return 'element not found';
		})()
	`, ref, ref, ref)

	result, err := bs.sendCDPCommand("Runtime.evaluate", map[string]any{
		"expression": script,
	})
	if err != nil {
		return nil, err
	}

	return map[string]any{
		"success": true,
		"ref":     ref,
		"result":  result,
	}, nil
}

// typeText types text into an input element.
func (bs *BrowserSession) typeText(ref, text string, clearFirst bool) (map[string]any, error) {
	bs.mu.Lock()
	defer bs.mu.Unlock()

	clearScript := ""
	if clearFirst {
		clearScript = "el.value = '';"
	}

	script := fmt.Sprintf(`
		(function() {
			var el = document.querySelector('[data-ref="%s"]');
			if (!el) {
				el = document.querySelector('#%s');
			}
			if (!el) {
				el = document.querySelector('[name="%s"]');
			}
			if (el) {
				el.focus();
				%s
				el.value = el.value + %s;
				el.dispatchEvent(new Event('input', {bubbles: true}));
				el.dispatchEvent(new Event('change', {bubbles: true}));
				return 'typed into: ' + el.tagName;
			}
			return 'element not found';
		})()
	`, ref, ref, ref, clearScript, jsonStringLiteral(text))

	result, err := bs.sendCDPCommand("Runtime.evaluate", map[string]any{
		"expression": script,
	})
	if err != nil {
		return nil, err
	}

	return map[string]any{
		"success": true,
		"ref":     ref,
		"result":  result,
	}, nil
}

// scroll scrolls the page in the given direction.
func (bs *BrowserSession) scroll(direction string, amount int) (map[string]any, error) {
	bs.mu.Lock()
	defer bs.mu.Unlock()

	if amount <= 0 {
		amount = 500
	}

	var deltaX, deltaY int
	switch direction {
	case "up":
		deltaY = -amount
	case "down":
		deltaY = amount
	case "left":
		deltaX = -amount
	case "right":
		deltaX = amount
	}

	script := fmt.Sprintf("window.scrollBy(%d, %d)", deltaX, deltaY)
	result, err := bs.sendCDPCommand("Runtime.evaluate", map[string]any{
		"expression": script,
	})
	if err != nil {
		return nil, err
	}

	return map[string]any{
		"success":   true,
		"direction": direction,
		"amount":    amount,
		"result":    result,
	}, nil
}

// goBack navigates back in browser history.
func (bs *BrowserSession) goBack() (map[string]any, error) {
	bs.mu.Lock()
	defer bs.mu.Unlock()

	result, err := bs.sendCDPCommand("Runtime.evaluate", map[string]any{
		"expression": "window.history.back()",
	})
	if err != nil {
		return nil, err
	}

	time.Sleep(1 * time.Second)

	return map[string]any{
		"success": true,
		"result":  result,
	}, nil
}

// pressKey presses a keyboard key.
func (bs *BrowserSession) pressKey(key string) (map[string]any, error) {
	bs.mu.Lock()
	defer bs.mu.Unlock()

	result, err := bs.sendCDPCommand("Input.dispatchKeyEvent", map[string]any{
		"type": "keyDown",
		"key":  key,
	})
	if err != nil {
		return nil, err
	}

	// Also send keyUp.
	bs.sendCDPCommand("Input.dispatchKeyEvent", map[string]any{
		"type": "keyUp",
		"key":  key,
	})

	return map[string]any{
		"success": true,
		"key":     key,
		"result":  result,
	}, nil
}

// getImages returns images on the current page.
func (bs *BrowserSession) getImages() (map[string]any, error) {
	bs.mu.Lock()
	defer bs.mu.Unlock()

	script := `
		(function() {
			var imgs = document.querySelectorAll('img');
			var result = [];
			for (var i = 0; i < Math.min(imgs.length, 50); i++) {
				result.push({
					src: imgs[i].src,
					alt: imgs[i].alt || '',
					width: imgs[i].naturalWidth,
					height: imgs[i].naturalHeight
				});
			}
			return JSON.stringify(result);
		})()
	`

	result, err := bs.sendCDPCommand("Runtime.evaluate", map[string]any{
		"expression": script,
	})
	if err != nil {
		return nil, err
	}

	return map[string]any{
		"success": true,
		"url":     bs.currentURL,
		"result":  result,
	}, nil
}

// executeScript runs JavaScript in the browser console.
func (bs *BrowserSession) executeScript(script string) (map[string]any, error) {
	bs.mu.Lock()
	defer bs.mu.Unlock()

	result, err := bs.sendCDPCommand("Runtime.evaluate", map[string]any{
		"expression":    script,
		"returnByValue": true,
	})
	if err != nil {
		return nil, err
	}

	return map[string]any{
		"success": true,
		"result":  result,
	}, nil
}

// close terminates the Browserbase session.
func (bs *BrowserSession) close() {
	bs.apiRequest("DELETE",
		fmt.Sprintf("/sessions/%s", bs.sessionID), nil)
}

// jsonStringLiteral returns a JSON-encoded string literal for embedding in JS.
func jsonStringLiteral(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// --- Override the stub handlers with real implementations ---

func init() {
	// Re-register browser tool handlers with actual implementations.
	// These replace the stubs defined in browser.go.

	overrides := map[string]ToolHandler{
		"browser_navigate":   handleBrowserNavigateImpl,
		"browser_snapshot":   handleBrowserSnapshotImpl,
		"browser_click":      handleBrowserClickImpl,
		"browser_type":       handleBrowserTypeImpl,
		"browser_scroll":     handleBrowserScrollImpl,
		"browser_back":       handleBrowserBackImpl,
		"browser_press":      handleBrowserPressImpl,
		"browser_get_images": handleBrowserGetImagesImpl,
		"browser_vision":     handleBrowserVisionImpl,
		"browser_console":    handleBrowserConsoleImpl,
		"browser_close":      handleBrowserCloseImpl,
	}

	for name, handler := range overrides {
		entry := Registry().GetSchema(name)
		if entry != nil {
			// The tool is registered; update its handler.
			Registry().mu.Lock()
			if tool, ok := Registry().tools[name]; ok {
				tool.Handler = handler
			}
			Registry().mu.Unlock()
		}
	}
}

func browserError(tool string, err error) string {
	return toJSON(map[string]any{
		"error": err.Error(),
		"tool":  tool,
	})
}

func handleBrowserNavigateImpl(args map[string]any, ctx *ToolContext) string {
	navURL, _ := args["url"].(string)
	if navURL == "" {
		return `{"error":"url is required"}`
	}

	// SSRF protection: block navigation to internal/metadata endpoints.
	if reason := checkNavigationSafety(navURL); reason != "" {
		return toJSON(map[string]any{
			"error":   "blocked_url",
			"url":     navURL,
			"reason":  reason,
			"message": "this url was blocked for security reasons",
		})
	}

	backend, err := getOrCreateBackend()
	if err != nil {
		return browserError("browser_navigate", err)
	}

	result, err := backend.Navigate(navURL)
	if err != nil {
		return browserError("browser_navigate", err)
	}

	return toJSON(result)
}

func handleBrowserSnapshotImpl(args map[string]any, ctx *ToolContext) string {
	backend, err := getOrCreateBackend()
	if err != nil {
		return browserError("browser_snapshot", err)
	}

	result, err := backend.Snapshot()
	if err != nil {
		return browserError("browser_snapshot", err)
	}

	return toJSON(result)
}

func handleBrowserClickImpl(args map[string]any, ctx *ToolContext) string {
	ref, _ := args["ref"].(string)
	if ref == "" {
		return `{"error":"ref is required"}`
	}

	backend, err := getOrCreateBackend()
	if err != nil {
		return browserError("browser_click", err)
	}

	result, err := backend.Click(ref)
	if err != nil {
		return browserError("browser_click", err)
	}

	return toJSON(result)
}

func handleBrowserTypeImpl(args map[string]any, ctx *ToolContext) string {
	ref, _ := args["ref"].(string)
	text, _ := args["text"].(string)
	clearFirst, _ := args["clear_first"].(bool)

	if ref == "" || text == "" {
		return `{"error":"ref and text are required"}`
	}

	backend, err := getOrCreateBackend()
	if err != nil {
		return browserError("browser_type", err)
	}

	result, err := backend.Type(ref, text, clearFirst)
	if err != nil {
		return browserError("browser_type", err)
	}

	return toJSON(result)
}

func handleBrowserScrollImpl(args map[string]any, ctx *ToolContext) string {
	direction, _ := args["direction"].(string)
	if direction == "" {
		return `{"error":"direction is required"}`
	}

	amount := 500
	if a, ok := args["amount"].(float64); ok && a > 0 {
		amount = int(a)
	}

	backend, err := getOrCreateBackend()
	if err != nil {
		return browserError("browser_scroll", err)
	}

	result, err := backend.Scroll(direction, amount)
	if err != nil {
		return browserError("browser_scroll", err)
	}

	return toJSON(result)
}

func handleBrowserBackImpl(args map[string]any, ctx *ToolContext) string {
	backend, err := getOrCreateBackend()
	if err != nil {
		return browserError("browser_back", err)
	}

	result, err := backend.GoBack()
	if err != nil {
		return browserError("browser_back", err)
	}

	return toJSON(result)
}

func handleBrowserPressImpl(args map[string]any, ctx *ToolContext) string {
	key, _ := args["key"].(string)
	if key == "" {
		return `{"error":"key is required"}`
	}

	backend, err := getOrCreateBackend()
	if err != nil {
		return browserError("browser_press", err)
	}

	result, err := backend.PressKey(key)
	if err != nil {
		return browserError("browser_press", err)
	}

	return toJSON(result)
}

func handleBrowserGetImagesImpl(args map[string]any, ctx *ToolContext) string {
	backend, err := getOrCreateBackend()
	if err != nil {
		return browserError("browser_get_images", err)
	}

	result, err := backend.GetImages()
	if err != nil {
		return browserError("browser_get_images", err)
	}

	return toJSON(result)
}

func handleBrowserVisionImpl(args map[string]any, ctx *ToolContext) string {
	// Vision requires a screenshot + multimodal LLM. For now, we take a
	// snapshot and return it -- full vision analysis would require piping
	// the screenshot through a vision model.
	backend, err := getOrCreateBackend()
	if err != nil {
		return browserError("browser_vision", err)
	}

	prompt, _ := args["prompt"].(string)
	if prompt == "" {
		prompt = "Describe what you see on this page."
	}

	snapshot, err := backend.Snapshot()
	if err != nil {
		return browserError("browser_vision", err)
	}

	snapshot["prompt"] = prompt
	snapshot["note"] = "Vision analysis requires a multimodal LLM. The accessibility tree is provided as a text representation of the page."

	return toJSON(snapshot)
}

func handleBrowserConsoleImpl(args map[string]any, ctx *ToolContext) string {
	script, _ := args["script"].(string)
	if script == "" {
		return `{"error":"script is required"}`
	}

	backend, err := getOrCreateBackend()
	if err != nil {
		return browserError("browser_console", err)
	}

	result, err := backend.ExecuteScript(script)
	if err != nil {
		return browserError("browser_console", err)
	}

	return toJSON(result)
}

func handleBrowserCloseImpl(args map[string]any, ctx *ToolContext) string {
	activeBackendMu.Lock()
	hasBackend := activeBackend != nil
	activeBackendMu.Unlock()

	if !hasBackend {
		return toJSON(map[string]any{
			"success": true,
			"message": "No active browser session to close.",
		})
	}

	closeActiveBackend()

	return toJSON(map[string]any{
		"success": true,
		"message": "Browser session closed successfully.",
	})
}
