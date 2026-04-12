package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// WorkerClient 封裝對 CLI Worker 的 HTTP 呼叫（僅 Rebuild）
type WorkerClient struct {
	baseURL    string
	secret     string
	httpClient *http.Client
}

func NewWorkerClient(baseURL, secret string) *WorkerClient {
	return &WorkerClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		secret:  secret,
		httpClient: &http.Client{
			Timeout: 2 * time.Minute,
		},
	}
}

func (w *WorkerClient) do(ctx context.Context, method, path string, body interface{}) (*http.Response, error) {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal body: %w", err)
		}
		reader = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, w.baseURL+path, reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if w.secret != "" {
		req.Header.Set("X-Internal-Secret", w.secret)
	}
	return w.httpClient.Do(req)
}

func (w *WorkerClient) doJSON(ctx context.Context, method, path string, body interface{}, result interface{}) error {
	resp, err := w.do(ctx, method, path, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("worker %s %s → %d: %s", method, path, resp.StatusCode, string(b))
	}
	if result != nil {
		return json.NewDecoder(resp.Body).Decode(result)
	}
	return nil
}

// RebuildReq 插件重新編譯請求
type RebuildReq struct {
	MemberID  string `json:"memberID"`
	PluginDir string `json:"pluginDir"`
}

// RebuildResp 插件重新編譯回應
type RebuildResp struct {
	Status     string `json:"status"`
	PluginDir  string `json:"pluginDir"`
	BundleHash string `json:"bundleHash"`
	Error      string `json:"error,omitempty"`
}

// Rebuild 觸發 CLI Worker 重新編譯插件（npm install + esbuild + validator）
func (w *WorkerClient) Rebuild(ctx context.Context, req RebuildReq) (*RebuildResp, error) {
	var resp RebuildResp
	if err := w.doJSON(ctx, "POST", "/internal/rebuild", req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}
