package tripcdp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"
)

type chromeDriverRuntime struct {
	cmd             *exec.Cmd
	endpoint        string
	sessionID       string
	debuggerAddress string
	done            chan error
}

func startChromeDriver(ctx context.Context, driverPath string) (*chromeDriverRuntime, error) {
	port, err := freeTCPPort()
	if err != nil {
		return nil, err
	}
	endpoint := fmt.Sprintf("http://127.0.0.1:%d", port)
	cmd := exec.Command(driverPath, fmt.Sprintf("--port=%d", port), "--allowed-origins=*")
	setProcessGroup(cmd)
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	runtime := &chromeDriverRuntime{cmd: cmd, endpoint: endpoint, done: make(chan error, 1)}
	go func() { runtime.done <- cmd.Wait() }()
	if err := waitChromeDriver(ctx, endpoint); err != nil {
		runtime.stop(context.Background())
		return nil, err
	}
	return runtime, nil
}

func (r *chromeDriverRuntime) createSession(ctx context.Context, chromePath string, userDataDir string, headless bool, debuggerPort int) error {
	args := chromeArguments(userDataDir, headless, debuggerPort, "")
	payload := map[string]any{
		"capabilities": map[string]any{
			"alwaysMatch": map[string]any{
				"browserName": "chrome",
				"goog:chromeOptions": map[string]any{
					"binary":                 chromePath,
					"args":                   args,
					"detach":                 true,
					"excludeSwitches":        []string{"enable-automation"},
					"useAutomationExtension": false,
				},
			},
		},
	}
	raw, err := webdriverJSON(ctx, http.MethodPost, r.endpoint+"/session", payload)
	if err != nil {
		return err
	}
	var response struct {
		SessionID string `json:"sessionId"`
		Value     struct {
			SessionID    string         `json:"sessionId"`
			Capabilities map[string]any `json:"capabilities"`
		} `json:"value"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return err
	}
	sessionID := response.Value.SessionID
	if sessionID == "" {
		sessionID = response.SessionID
	}
	if sessionID == "" {
		return fmt.Errorf("chromedriver did not return a session id")
	}
	r.sessionID = sessionID
	if value, ok := response.Value.Capabilities["goog:chromeOptions"].(map[string]any); ok {
		if address, ok := value["debuggerAddress"].(string); ok {
			r.debuggerAddress = address
		}
	}
	if r.debuggerAddress == "" {
		return fmt.Errorf("chromedriver did not return debuggerAddress")
	}
	return nil
}

func (r *chromeDriverRuntime) attachSession(ctx context.Context, debuggerAddress string) error {
	payload := map[string]any{
		"capabilities": map[string]any{
			"alwaysMatch": map[string]any{
				"browserName": "chrome",
				"goog:chromeOptions": map[string]any{
					"debuggerAddress": debuggerAddress,
				},
			},
		},
	}
	raw, err := webdriverJSON(ctx, http.MethodPost, r.endpoint+"/session", payload)
	if err != nil {
		return err
	}
	var response struct {
		SessionID string `json:"sessionId"`
		Value     struct {
			SessionID string `json:"sessionId"`
		} `json:"value"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return err
	}
	sessionID := response.Value.SessionID
	if sessionID == "" {
		sessionID = response.SessionID
	}
	if sessionID == "" {
		return fmt.Errorf("chromedriver did not return a session id")
	}
	r.sessionID = sessionID
	r.debuggerAddress = debuggerAddress
	return nil
}

func (r *chromeDriverRuntime) browserWebSocketURL(ctx context.Context) (string, error) {
	address := r.debuggerAddress
	if !strings.HasPrefix(address, "http://") && !strings.HasPrefix(address, "https://") {
		address = "http://" + address
	}
	raw, err := webdriverJSON(ctx, http.MethodGet, strings.TrimRight(address, "/")+"/json/version", nil)
	if err != nil {
		return "", err
	}
	var payload struct {
		WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return "", err
	}
	if payload.WebSocketDebuggerURL == "" {
		return "", fmt.Errorf("Chrome /json/version did not return webSocketDebuggerUrl")
	}
	return normalizeLocalhostURL(payload.WebSocketDebuggerURL), nil
}

func (r *chromeDriverRuntime) detachSession(ctx context.Context) {
	if r == nil || r.sessionID == "" {
		return
	}
	_, _ = webdriverJSON(ctx, http.MethodDelete, r.endpoint+"/session/"+r.sessionID, nil)
	r.sessionID = ""
}

func (r *chromeDriverRuntime) detachDriverProcess() {
	if r == nil {
		return
	}
	r.sessionID = ""
	terminateProcessTree(r.cmd)
}

func (r *chromeDriverRuntime) stop(ctx context.Context) {
	if r == nil {
		return
	}
	if r.sessionID != "" {
		deleteCtx, cancel := context.WithTimeout(ctx, time.Second)
		_, _ = webdriverJSON(deleteCtx, http.MethodDelete, r.endpoint+"/session/"+r.sessionID, nil)
		cancel()
		r.sessionID = ""
	}
	terminateProcessTree(r.cmd)
}

func chromeArguments(userDataDir string, headless bool, debuggerPort int, language string) []string {
	args := []string{
		"--user-data-dir=" + userDataDir,
		"--remote-debugging-host=127.0.0.1",
		fmt.Sprintf("--remote-debugging-port=%d", debuggerPort),
		"--lang=" + chromeUILanguage(language),
		"--no-first-run",
		"--no-default-browser-check",
		"--no-sandbox",
		"--test-type",
		"--disable-background-timer-throttling",
		"--disable-backgrounding-occluded-windows",
		"--disable-dev-shm-usage",
		"--disable-gpu",
		"--disable-crash-reporter",
		"--disable-features=Translate,TranslateUI,BackForwardCache",
		"--disable-hang-monitor",
		"--disable-ipc-flooding-protection",
		"--disable-notifications",
		"--disable-popup-blocking",
		"--disable-renderer-backgrounding",
		"--disable-prompt-on-repost",
		"--metrics-recording-only",
		"--no-report-upload",
		"--disable-blink-features=AutomationControlled",
		"--window-size=1920,1080",
		"--start-maximized",
		"--log-level=0",
	}
	if headless {
		args = append(args, "--headless=new")
	}
	return sanitizeChromeArguments(args)
}

func chromeLanguageOverride(language string) string {
	if strings.TrimSpace(language) != "" {
		return strings.TrimSpace(language)
	}
	return chromeLanguage()
}

func chromeUILanguage(language string) string {
	language = chromeLanguageOverride(language)
	if before, _, ok := strings.Cut(language, ","); ok {
		language = before
	}
	if before, _, ok := strings.Cut(language, ";"); ok {
		language = before
	}
	return strings.TrimSpace(language)
}

func sanitizeChromeArguments(args []string) []string {
	blocked := []string{
		"--disable-extensions",
		"--disable-application-cache",
		"--aggressive-cache-discard",
		"--disk-cache-size=",
		"--media-cache-size=",
		"--js-flags=",
		"--disable-background-networking",
		"--disable-default-apps",
		"--disable-sync",
		"--disable-component-update",
		"--disable-domain-reliability",
		"--disable-client-side-phishing-detection",
		"--safebrowsing-disable-auto-update",
	}
	out := make([]string, 0, len(args))
	seen := map[string]bool{}
	for _, arg := range args {
		skip := false
		for _, prefix := range blocked {
			if strings.HasPrefix(arg, prefix) {
				skip = true
				break
			}
		}
		if skip || seen[arg] {
			continue
		}
		seen[arg] = true
		out = append(out, arg)
	}
	return out
}

func chromeLanguage() string {
	for _, key := range []string{"LC_ALL", "LANG"} {
		value := strings.TrimSpace(strings.Split(os.Getenv(key), ".")[0])
		if value != "" && value != "C" && value != "POSIX" {
			return strings.ReplaceAll(value, "_", "-")
		}
	}
	return "en-US"
}

func waitChromeDriver(ctx context.Context, endpoint string) error {
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if err := ctx.Err(); err != nil {
			return err
		}
		if _, err := webdriverJSON(ctx, http.MethodGet, endpoint+"/status", nil); err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	return fmt.Errorf("chromedriver did not become ready")
}

func waitChromeDebugger(ctx context.Context, debuggerAddress string) (string, error) {
	address := debuggerAddress
	if !strings.HasPrefix(address, "http://") && !strings.HasPrefix(address, "https://") {
		address = "http://" + address
	}
	endpoint := strings.TrimRight(address, "/") + "/json/version"
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		raw, err := webdriverJSON(ctx, http.MethodGet, endpoint, nil)
		if err == nil {
			var payload struct {
				WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
			}
			if err := json.Unmarshal(raw, &payload); err != nil {
				return "", err
			}
			if payload.WebSocketDebuggerURL != "" {
				return normalizeLocalhostURL(payload.WebSocketDebuggerURL), nil
			}
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	return "", fmt.Errorf("Chrome debugger did not become ready")
}

func webdriverJSON(ctx context.Context, method string, url string, payload any) ([]byte, error) {
	var body *bytes.Reader
	if payload == nil {
		body = bytes.NewReader(nil)
	} else {
		raw, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, err
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw := new(bytes.Buffer)
	_, _ = raw.ReadFrom(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("%s %s returned %s: %s", method, url, resp.Status, raw.String())
	}
	return raw.Bytes(), nil
}

func freeTCPPort() (int, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port, nil
}

func normalizeLocalhostURL(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	host := parsed.Hostname()
	if host != "localhost" {
		return raw
	}
	port := parsed.Port()
	if port == "" {
		parsed.Host = "127.0.0.1"
	} else {
		parsed.Host = net.JoinHostPort("127.0.0.1", port)
	}
	return parsed.String()
}
