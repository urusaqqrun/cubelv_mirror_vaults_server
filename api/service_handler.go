package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ServiceHandler 處理 /svc/ 請求：manifest 認證 + Service Worker 啟動 + proxy
type ServiceHandler struct {
	vaultRoot     string
	workerBaseURL string
	workerSecret  string

	mu       sync.RWMutex
	registry map[string]*serviceEntry // "memberID:serviceDir" → entry
}

type serviceEntry struct {
	MemberID     string `json:"memberID"`
	ServiceDir   string `json:"serviceDir"`
	Port         int    `json:"port"`
	Status       string `json:"status"`
	LastActivity int64  `json:"lastActivity"`
}

type serviceManifest struct {
	Name      string             `json:"name"`
	Version   string             `json:"version"`
	Endpoints []manifestEndpoint `json:"endpoints"`
}

type manifestEndpoint struct {
	Path                string `json:"path"`
	Method              string `json:"method"`
	Public              bool   `json:"public"`
	WebhookSecretHeader string `json:"webhook_secret_header,omitempty"`
	WebhookSecretValue  string `json:"webhook_secret_value,omitempty"`
}

func NewServiceHandler(vaultRoot, serviceWorkerURL, workerSecret string) *ServiceHandler {
	return &ServiceHandler{
		vaultRoot:     vaultRoot,
		workerBaseURL: strings.TrimRight(serviceWorkerURL, "/"),
		workerSecret:  workerSecret,
		registry:      make(map[string]*serviceEntry),
	}
}

func (h *ServiceHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/svc/", h.HandleSvcRequest)
}

// HandleSvcRequest: /svc/{memberID}/{serviceDir}/{endpoint...}
func (h *ServiceHandler) HandleSvcRequest(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/svc/")
	parts := strings.SplitN(path, "/", 3)
	if len(parts) < 2 {
		http.Error(w, "invalid service path", http.StatusBadRequest)
		return
	}
	memberID := filepath.Base(parts[0])
	serviceDir := filepath.Base(parts[1])
	endpoint := "/"
	if len(parts) == 3 {
		endpoint = "/" + parts[2]
	}

	manifest, err := h.loadManifest(memberID, serviceDir)
	if err != nil {
		http.Error(w, "service not found", http.StatusNotFound)
		return
	}

	ep := findEndpoint(manifest, endpoint, r.Method)

	if ep != nil && ep.Public {
		if ep.WebhookSecretHeader != "" {
			expected := ep.WebhookSecretValue
			actual := r.Header.Get(ep.WebhookSecretHeader)
			if actual == "" || actual != expected {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
		}
	} else {
		userID := r.Header.Get("X-User-ID")
		if userID == "" || userID != memberID {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
	}

	port, err := h.ensureService(r.Context(), memberID, serviceDir)
	if err != nil {
		log.Printf("[ServiceHandler] ensure failed: %s/%s: %v", memberID, serviceDir, err)
		http.Error(w, "service unavailable: "+err.Error(), http.StatusServiceUnavailable)
		return
	}

	proxyURL := fmt.Sprintf("%s/svc-proxy/%s/%s%s", h.workerBaseURL, memberID, serviceDir, endpoint)
	h.proxyRequest(w, r, proxyURL, port)
}

func (h *ServiceHandler) loadManifest(memberID, serviceDir string) (*serviceManifest, error) {
	manifestPath := filepath.Join(h.vaultRoot, memberID, "services", serviceDir, "manifest.json")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, err
	}
	var m serviceManifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

func findEndpoint(m *serviceManifest, path, method string) *manifestEndpoint {
	for i := range m.Endpoints {
		ep := &m.Endpoints[i]
		if strings.EqualFold(ep.Method, method) && ep.Path == path {
			return ep
		}
		if ep.Method == "" && ep.Path == path {
			return ep
		}
	}
	for i := range m.Endpoints {
		ep := &m.Endpoints[i]
		if strings.HasPrefix(path, ep.Path) {
			return ep
		}
	}
	return nil
}

func (h *ServiceHandler) ensureService(ctx context.Context, memberID, serviceDir string) (int, error) {
	key := memberID + ":" + serviceDir

	h.mu.RLock()
	entry, exists := h.registry[key]
	h.mu.RUnlock()

	if exists && entry.Status == "running" {
		return entry.Port, nil
	}

	reqBody, _ := json.Marshal(map[string]string{
		"memberID":   memberID,
		"serviceDir": serviceDir,
	})

	req, err := http.NewRequestWithContext(ctx, "POST", h.workerBaseURL+"/internal/service/ensure", bytes.NewReader(reqBody))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	if h.workerSecret != "" {
		req.Header.Set("X-Internal-Secret", h.workerSecret)
	}

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return 0, fmt.Errorf("worker ensure → %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Status string `json:"status"`
		Port   int    `json:"port"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0, err
	}

	if result.Status == "starting" {
		if err := h.waitForServiceReady(ctx, memberID, serviceDir, 45*time.Second); err != nil {
			return 0, err
		}
	}

	h.mu.Lock()
	h.registry[key] = &serviceEntry{
		MemberID:     memberID,
		ServiceDir:   serviceDir,
		Port:         result.Port,
		Status:       "running",
		LastActivity: time.Now().Unix(),
	}
	h.mu.Unlock()

	return result.Port, nil
}

func (h *ServiceHandler) waitForServiceReady(ctx context.Context, memberID, serviceDir string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		reqBody, _ := json.Marshal(map[string]string{
			"memberID":   memberID,
			"serviceDir": serviceDir,
		})
		req, _ := http.NewRequestWithContext(ctx, "POST", h.workerBaseURL+"/internal/service/ensure", bytes.NewReader(reqBody))
		req.Header.Set("Content-Type", "application/json")
		if h.workerSecret != "" {
			req.Header.Set("X-Internal-Secret", h.workerSecret)
		}

		client := &http.Client{Timeout: 5 * time.Second}
		resp, err := client.Do(req)
		if err != nil {
			time.Sleep(1 * time.Second)
			continue
		}

		var result struct {
			Status string `json:"status"`
		}
		json.NewDecoder(resp.Body).Decode(&result)
		resp.Body.Close()

		if result.Status == "running" {
			return nil
		}
		if result.Status == "error" {
			return fmt.Errorf("service failed to start")
		}
		time.Sleep(1 * time.Second)
	}
	return fmt.Errorf("service start timeout")
}

func (h *ServiceHandler) proxyRequest(w http.ResponseWriter, r *http.Request, targetURL string, _ int) {
	target, err := url.Parse(targetURL)
	if err != nil {
		http.Error(w, "invalid proxy target", http.StatusInternalServerError)
		return
	}

	proxy := httputil.NewSingleHostReverseProxy(&url.URL{
		Scheme: target.Scheme,
		Host:   target.Host,
	})
	r.URL = target
	r.Host = target.Host
	if h.workerSecret != "" {
		r.Header.Set("X-Internal-Secret", h.workerSecret)
	}
	proxy.ServeHTTP(w, r)
}
