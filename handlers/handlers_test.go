package handlers

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"clash-subscription-manager/models"

	"github.com/gorilla/mux"
)

func TestSubscribeHandlerDownloadsAndStoresSubscriptionFile(t *testing.T) {
	dataDir := t.TempDir()
	handler := NewHandler(&Config{
		DataDir:         dataDir,
		MaxFileSize:     1024,
		DownloadTimeout: 0,
	})
	var seenHeaders http.Header
	handler.httpClient = &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			seenHeaders = req.Header.Clone()
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader("ss://YWVzLTI1Ni1nY206cGFzcw==@example.com:443#demo")),
			}, nil
		}),
	}

	body := bytes.NewBufferString(`{"name":"demo","url":"https://example.com/subscription","filter":"(?i)港|hk|hongkong|hong kong","request_headers":{"User-Agent":"custom-agent","X-Test-Header":"abc123"}}`)
	req := httptest.NewRequest(http.MethodPost, "/api/subscribe", body)
	rec := httptest.NewRecorder()

	handler.SubscribeHandler(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d; body = %s", rec.Code, http.StatusCreated, rec.Body.String())
	}

	var resp Response
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}

	created, ok := resp.Data.(map[string]any)
	if !ok {
		t.Fatalf("response data type = %T, want object", resp.Data)
	}

	filePath, ok := created["file_path"].(string)
	if !ok || filePath == "" {
		t.Fatalf("file_path = %#v, want non-empty string", created["file_path"])
	}

	storedPath := filepath.Join(dataDir, filePath)
	content, err := os.ReadFile(storedPath)
	if err != nil {
		t.Fatalf("os.ReadFile(%q) error = %v", storedPath, err)
	}
	if !strings.Contains(string(content), "type: ss") {
		t.Fatalf("stored content missing converted ss node:\n%s", string(content))
	}
	if !strings.Contains(string(content), "server: example.com") {
		t.Fatalf("stored content missing converted server:\n%s", string(content))
	}

	subs, err := ListSubscriptions(filepath.Join(dataDir, "subscriptions.json"))
	if err != nil {
		t.Fatalf("ListSubscriptions() error = %v", err)
	}
	if len(subs) != 1 {
		t.Fatalf("len(subs) = %d, want 1", len(subs))
	}
	if subs[0].FilePath != filePath {
		t.Fatalf("subs[0].FilePath = %q, want %q", subs[0].FilePath, filePath)
	}
	if got := seenHeaders.Get("User-Agent"); got != "custom-agent" {
		t.Fatalf("User-Agent header = %q, want %q", got, "custom-agent")
	}
	if got := seenHeaders.Get("X-Test-Header"); got != "abc123" {
		t.Fatalf("X-Test-Header = %q, want %q", got, "abc123")
	}
	if got := subs[0].RequestHeaders["X-Test-Header"]; got != "abc123" {
		t.Fatalf("saved request header = %q, want %q", got, "abc123")
	}
	if got := subs[0].Filter; got != "(?i)港|hk|hongkong|hong kong" {
		t.Fatalf("saved filter = %q, want %q", got, "(?i)港|hk|hongkong|hong kong")
	}
	if got := subs[0].Type; got != "ss" {
		t.Fatalf("saved type = %q, want %q", got, "ss")
	}
	if got := subs[0].NodeCount; got != 1 {
		t.Fatalf("saved node count = %d, want 1", got)
	}
}

func TestSubscribeNodesHandlerCreatesSubscriptionFromMultipleLinks(t *testing.T) {
	dataDir := t.TempDir()
	handler := NewHandler(&Config{
		DataDir:         dataDir,
		MaxFileSize:     1024,
		DownloadTimeout: 0,
	})

	body := bytes.NewBufferString("{\"name\":\"mixed nodes\",\"node_links_text\":\"ss://YWVzLTI1Ni1nY206cGFzcw==@ss.example.com:443#ss-demo\\ntrojan://secret@trojan.example.com:443#trojan-demo\",\"filter\":\"(?i)sg|jp\"}")
	req := httptest.NewRequest(http.MethodPost, "/api/subscribe/nodes", body)
	rec := httptest.NewRecorder()

	handler.SubscribeNodesHandler(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d; body = %s", rec.Code, http.StatusCreated, rec.Body.String())
	}

	subs, err := ListSubscriptions(filepath.Join(dataDir, "subscriptions.json"))
	if err != nil {
		t.Fatalf("ListSubscriptions() error = %v", err)
	}
	if len(subs) != 1 {
		t.Fatalf("len(subs) = %d, want 1", len(subs))
	}
	if got := subs[0].Name; got != "mixed nodes" {
		t.Fatalf("name = %q, want %q", got, "mixed nodes")
	}
	if got := subs[0].URL; got != "nodes://manual" {
		t.Fatalf("url = %q, want %q", got, "nodes://manual")
	}
	if got := subs[0].Type; got != "mixed" {
		t.Fatalf("type = %q, want %q", got, "mixed")
	}
	if got := subs[0].NodeCount; got != 2 {
		t.Fatalf("node count = %d, want 2", got)
	}
	if got := subs[0].Filter; got != "(?i)sg|jp" {
		t.Fatalf("filter = %q, want %q", got, "(?i)sg|jp")
	}
	if len(subs[0].RequestHeaders) != 0 {
		t.Fatalf("request headers = %#v, want empty", subs[0].RequestHeaders)
	}
	content, err := os.ReadFile(filepath.Join(dataDir, subs[0].FilePath))
	if err != nil {
		t.Fatalf("os.ReadFile() error = %v", err)
	}
	if !strings.Contains(string(content), "type: ss") {
		t.Fatalf("stored content missing ss proxy:\n%s", string(content))
	}
	if !strings.Contains(string(content), "type: trojan") {
		t.Fatalf("stored content missing trojan proxy:\n%s", string(content))
	}
}

func TestRateLimitHandlesConcurrentRequestsSafely(t *testing.T) {
	handler := NewHandler(&Config{
		DataDir:         t.TempDir(),
		MaxFileSize:     1024,
		DownloadTimeout: 0,
		RateLimit:       1000,
	})

	wrapped := handler.RateLimit(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	const requestCount = 200
	var wg sync.WaitGroup
	errCh := make(chan error, requestCount)

	for i := 0; i < requestCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()

			req := httptest.NewRequest(http.MethodGet, "/download/sub-1", nil)
			req.RemoteAddr = "127.0.0.1:12345"
			rec := httptest.NewRecorder()

			wrapped(rec, req)

			if rec.Code != http.StatusOK {
				errCh <- fmt.Errorf("unexpected status %d", rec.Code)
			}
		}()
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Fatal(err)
	}
}

func TestDownloadHandlerServesStoredSubscriptionFile(t *testing.T) {
	dataDir := t.TempDir()
	storedName := "subscription-test.txt"
	storedPath := filepath.Join(dataDir, storedName)
	writeHandlerTestFile(t, storedPath, []byte("stored-proxy-file"))

	err := SaveSubscriptions([]models.Subscription{
		{
			ID:       "sub-1",
			Name:     "demo",
			URL:      "http://example.invalid/sub",
			FilePath: storedName,
			FileSize: int64(len("stored-proxy-file")),
		},
	}, filepath.Join(dataDir, "subscriptions.json"))
	if err != nil {
		t.Fatalf("SaveSubscriptions() error = %v", err)
	}

	handler := NewHandler(&Config{
		DataDir:         dataDir,
		MaxFileSize:     1024,
		DownloadTimeout: 0,
	})

	req := httptest.NewRequest(http.MethodGet, "/download/sub-1", nil)
	rec := httptest.NewRecorder()
	req = mux.SetURLVars(req, map[string]string{"id": "sub-1"})

	handler.DownloadHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if rec.Body.String() != "stored-proxy-file" {
		t.Fatalf("body = %q, want %q", rec.Body.String(), "stored-proxy-file")
	}
}

func TestHomeHandlerRendersStaticAssetLinks(t *testing.T) {
	handler := NewHandler(&Config{
		WebAssets:       os.DirFS(".."),
		DataDir:         t.TempDir(),
		MaxFileSize:     1024,
		DownloadTimeout: 0,
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	handler.HomeHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `/static/css/style.css`) {
		t.Fatalf("body missing stylesheet link: %s", body)
	}
	if !strings.Contains(body, `/static/js/main.js`) {
		t.Fatalf("body missing script link: %s", body)
	}
	if !strings.Contains(body, `复制下载地址`) {
		t.Fatalf("body missing copy-download wording: %s", body)
	}
	if strings.Contains(body, `选择机场`) {
		t.Fatalf("body should not contain provider selector wording: %s", body)
	}
	if !strings.Contains(body, `自定义请求头`) {
		t.Fatalf("body missing request headers wording: %s", body)
	}
	if !strings.Contains(body, `协议类型会在下载后自动识别并转换`) {
		t.Fatalf("body missing auto-detect hint for subscription type: %s", body)
	}
	if !strings.Contains(body, `保存后刷新时会重新自动识别类型并更新缓存文件`) {
		t.Fatalf("body missing auto-detect hint for edit form: %s", body)
	}
	if !strings.Contains(body, `id="edit-subscription-name-input"`) {
		t.Fatalf("body missing edit subscription name input id: %s", body)
	}
	if !strings.Contains(body, `id="edit-subscription-url-input"`) {
		t.Fatalf("body missing edit subscription url input id: %s", body)
	}
	if !strings.Contains(body, `id="edit-subscription-filter-input"`) {
		t.Fatalf("body missing edit subscription filter input id: %s", body)
	}
	if !strings.Contains(body, `id="edit-request-headers-input"`) {
		t.Fatalf("body missing edit request headers input id: %s", body)
	}
	if strings.Contains(body, `name="type"`) {
		t.Fatalf("body should not contain subscription type selector: %s", body)
	}
	if strings.Contains(body, `复制默认模板地址`) {
		t.Fatalf("body should not contain default template copy button: %s", body)
	}
	if strings.Contains(body, `复制默认全节点地址`) {
		t.Fatalf("body should not contain default expanded template copy button: %s", body)
	}
	if strings.Contains(body, `x-auth-token`) {
		t.Fatalf("body should not contain x-auth-token template: %s", body)
	}
	if !strings.Contains(body, `workspace-tabs`) {
		t.Fatalf("body missing workspace tabs shell: %s", body)
	}
	if !strings.Contains(body, `data-tab-target="subscriptions-panel"`) {
		t.Fatalf("body missing subscriptions tab toggle: %s", body)
	}
	if !strings.Contains(body, `data-tab-target="templates-panel"`) {
		t.Fatalf("body missing templates tab toggle: %s", body)
	}
	if !strings.Contains(body, `data-toggle-advanced="subscription-advanced"`) {
		t.Fatalf("body missing subscription advanced settings toggle: %s", body)
	}
	if !strings.Contains(body, `id="dashboard-subscription-count"`) {
		t.Fatalf("body missing dashboard metric anchor: %s", body)
	}
	if !strings.Contains(body, `id="templates-panel-tab"`) || !strings.Contains(body, `模板管理`) {
		t.Fatalf("body missing templates tab: %s", body)
	}
	if !strings.Contains(body, `id="subscriptions-panel-tab"`) || !strings.Contains(body, `订阅管理`) {
		t.Fatalf("body missing subscriptions tab: %s", body)
	}
	if strings.Index(body, `id="templates-panel-tab"`) > strings.Index(body, `id="subscriptions-panel-tab"`) {
		t.Fatalf("templates tab should appear before subscriptions tab: %s", body)
	}
	if strings.Index(body, `class="surface template-sidebar"`) > strings.Index(body, `class="surface template-editor-surface"`) {
		t.Fatalf("template page body layout should remain list-left editor-right: %s", body)
	}
	if strings.Index(body, `class="surface surface-accent"`) > strings.Index(body, `<section class="surface">`) {
		t.Fatalf("subscription page body layout should remain create-left list-right: %s", body)
	}
	if !strings.Contains(body, `name="subscription_source"`) {
		t.Fatalf("body missing subscription source mode toggle: %s", body)
	}
	if !strings.Contains(body, `value="url"`) || !strings.Contains(body, `value="nodes"`) {
		t.Fatalf("body missing source mode options: %s", body)
	}
	if !strings.Contains(body, `name="node_links_text"`) {
		t.Fatalf("body missing node links textarea: %s", body)
	}
	if !strings.Contains(body, `支持直接粘贴一个或多个节点链接`) {
		t.Fatalf("body missing node-links helper copy: %s", body)
	}
}

func TestHomeHandlerRendersHeroLogoBranding(t *testing.T) {
	handler := NewHandler(&Config{
		WebAssets:       os.DirFS(".."),
		DataDir:         t.TempDir(),
		MaxFileSize:     1024,
		DownloadTimeout: 0,
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	handler.HomeHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	body := rec.Body.String()
	if !strings.Contains(body, `/static/img/logo-primary.svg`) {
		t.Fatalf("body missing primary logo asset: %s", body)
	}
	if !strings.Contains(body, `class="hero-brand"`) {
		t.Fatalf("body missing hero brand row: %s", body)
	}
	if !strings.Contains(body, `alt="Clash Subscription Manager logo"`) {
		t.Fatalf("body missing primary logo alt text: %s", body)
	}
	if !strings.Contains(body, `Clash 订阅管理`) {
		t.Fatalf("body missing existing Chinese page title: %s", body)
	}
	if strings.Index(body, `class="hero-brand"`) > strings.Index(body, `class="eyebrow"`) {
		t.Fatalf("hero brand should render before eyebrow label: %s", body)
	}
}

func TestHomeHandlerRendersFaviconLink(t *testing.T) {
	handler := NewHandler(&Config{
		WebAssets:       os.DirFS(".."),
		DataDir:         t.TempDir(),
		MaxFileSize:     1024,
		DownloadTimeout: 0,
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	handler.HomeHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	body := rec.Body.String()
	if !strings.Contains(body, `rel="icon"`) {
		t.Fatalf("body missing favicon rel: %s", body)
	}
	if !strings.Contains(body, `/static/img/logo-icon.svg`) {
		t.Fatalf("body missing favicon asset link: %s", body)
	}
}

func TestRefreshSubscriptionUpdatesURLHeadersAndCachedFile(t *testing.T) {
	dataDir := t.TempDir()
	cachedName := "sub-1.yaml"
	cachedPath := filepath.Join(dataDir, cachedName)
	writeHandlerTestFile(t, cachedPath, []byte("old-content"))

	err := SaveSubscriptions([]models.Subscription{
		{
			ID:       "sub-1",
			Name:     "before",
			URL:      "https://example.com/old",
			Filter:   "旧规则",
			Type:     "clash",
			FilePath: cachedName,
			FileSize: int64(len("old-content")),
			RequestHeaders: map[string]string{
				"User-Agent": "before-agent",
			},
		},
	}, filepath.Join(dataDir, "subscriptions.json"))
	if err != nil {
		t.Fatalf("SaveSubscriptions() error = %v", err)
	}

	handler := NewHandler(&Config{
		DataDir:         dataDir,
		MaxFileSize:     1024,
		DownloadTimeout: 0,
	})

	var requestedURL string
	var seenHeaders http.Header
	handler.httpClient = &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			requestedURL = req.URL.String()
			seenHeaders = req.Header.Clone()
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader("trojan://secret@trojan.example.com:443?type=ws&host=cdn.example.com&path=%2Fws&sni=edge.example.com#trojan-demo")),
			}, nil
		}),
	}

	body := bytes.NewBufferString(`{"name":"after","url":"https://example.com/new","filter":"(?i)港|hk|hongkong|hong kong","request_headers":{"User-Agent":"after-agent","X-Refresh-Key":"new-token"}}`)
	req := httptest.NewRequest(http.MethodPost, "/api/subscribe/sub-1/refresh", body)
	req = mux.SetURLVars(req, map[string]string{"id": "sub-1"})
	rec := httptest.NewRecorder()

	handler.RefreshSubscriptionHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if requestedURL != "https://example.com/new" {
		t.Fatalf("requested URL = %q, want %q", requestedURL, "https://example.com/new")
	}
	if got := seenHeaders.Get("X-Refresh-Key"); got != "new-token" {
		t.Fatalf("X-Refresh-Key = %q, want %q", got, "new-token")
	}

	content, err := os.ReadFile(cachedPath)
	if err != nil {
		t.Fatalf("os.ReadFile(%q) error = %v", cachedPath, err)
	}
	if !strings.Contains(string(content), "type: trojan") {
		t.Fatalf("cached content missing converted trojan node:\n%s", string(content))
	}
	if !strings.Contains(string(content), "servername: edge.example.com") {
		t.Fatalf("cached content missing converted sni:\n%s", string(content))
	}

	sub, err := GetSubscription("sub-1", filepath.Join(dataDir, "subscriptions.json"))
	if err != nil {
		t.Fatalf("GetSubscription() error = %v", err)
	}
	if sub.Name != "after" {
		t.Fatalf("name = %q, want %q", sub.Name, "after")
	}
	if sub.URL != "https://example.com/new" {
		t.Fatalf("url = %q, want %q", sub.URL, "https://example.com/new")
	}
	if sub.FileSize != int64(len(content)) {
		t.Fatalf("file size = %d, want %d", sub.FileSize, len(content))
	}
	if sub.RequestHeaders["User-Agent"] != "after-agent" {
		t.Fatalf("request header User-Agent = %q, want %q", sub.RequestHeaders["User-Agent"], "after-agent")
	}
	if sub.Filter != "(?i)港|hk|hongkong|hong kong" {
		t.Fatalf("filter = %q, want %q", sub.Filter, "(?i)港|hk|hongkong|hong kong")
	}
	if sub.Type != "trojan" {
		t.Fatalf("type = %q, want %q", sub.Type, "trojan")
	}
	if sub.NodeCount != 1 {
		t.Fatalf("node count = %d, want 1", sub.NodeCount)
	}
}

func TestRefreshSubscriptionFailurePreservesCachedFileAndRecordsError(t *testing.T) {
	dataDir := t.TempDir()
	cachedName := "sub-1.yaml"
	cachedPath := filepath.Join(dataDir, cachedName)
	writeHandlerTestFile(t, cachedPath, []byte("old-content"))

	lastCheck := time.Now().Add(-3 * time.Hour).UTC()
	err := SaveSubscriptions([]models.Subscription{
		{
			ID:        "sub-1",
			Name:      "before",
			URL:       "https://example.com/old",
			Type:      "clash",
			FilePath:  cachedName,
			FileSize:  int64(len("old-content")),
			LastCheck: lastCheck,
			Status:    "active",
		},
	}, filepath.Join(dataDir, "subscriptions.json"))
	if err != nil {
		t.Fatalf("SaveSubscriptions() error = %v", err)
	}

	handler := NewHandler(&Config{
		DataDir:         dataDir,
		MaxFileSize:     1024,
		DownloadTimeout: 0,
	})
	handler.httpClient = &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return nil, fmt.Errorf("network timeout")
		}),
	}

	body := bytes.NewBufferString(`{}`)
	req := httptest.NewRequest(http.MethodPost, "/api/subscribe/sub-1/refresh", body)
	req = mux.SetURLVars(req, map[string]string{"id": "sub-1"})
	rec := httptest.NewRecorder()

	handler.RefreshSubscriptionHandler(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want %d; body = %s", rec.Code, http.StatusBadGateway, rec.Body.String())
	}

	content, err := os.ReadFile(cachedPath)
	if err != nil {
		t.Fatalf("os.ReadFile(%q) error = %v", cachedPath, err)
	}
	if string(content) != "old-content" {
		t.Fatalf("cached content = %q, want %q", string(content), "old-content")
	}

	sub, err := GetSubscription("sub-1", filepath.Join(dataDir, "subscriptions.json"))
	if err != nil {
		t.Fatalf("GetSubscription() error = %v", err)
	}
	if sub.FilePath != cachedName {
		t.Fatalf("file path = %q, want %q", sub.FilePath, cachedName)
	}
	if sub.FileSize != int64(len("old-content")) {
		t.Fatalf("file size = %d, want %d", sub.FileSize, len("old-content"))
	}
	if got := sub.LastError; !strings.Contains(got, "network timeout") {
		t.Fatalf("last error = %q, want to contain %q", got, "network timeout")
	}
	if sub.LastErrorTime.IsZero() {
		t.Fatal("last error time should be set on refresh failure")
	}
	if !sub.LastCheck.Equal(lastCheck) {
		t.Fatalf("last check = %s, want %s", sub.LastCheck, lastCheck)
	}
}

func TestRefreshSubscriptionSuccessClearsPreviousError(t *testing.T) {
	dataDir := t.TempDir()
	cachedName := "sub-1.yaml"
	cachedPath := filepath.Join(dataDir, cachedName)
	writeHandlerTestFile(t, cachedPath, []byte("old-content"))

	lastErrorTime := time.Now().Add(-time.Hour).UTC()
	err := SaveSubscriptions([]models.Subscription{
		{
			ID:            "sub-1",
			Name:          "before",
			URL:           "https://example.com/old",
			Type:          "clash",
			FilePath:      cachedName,
			FileSize:      int64(len("old-content")),
			Status:        "active",
			LastError:     "download failed with status: 500",
			LastErrorTime: lastErrorTime,
		},
	}, filepath.Join(dataDir, "subscriptions.json"))
	if err != nil {
		t.Fatalf("SaveSubscriptions() error = %v", err)
	}

	handler := NewHandler(&Config{
		DataDir:         dataDir,
		MaxFileSize:     1024,
		DownloadTimeout: 0,
	})
	handler.httpClient = &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader("ss://YWVzLTI1Ni1nY206cGFzcw==@example.com:443#demo")),
			}, nil
		}),
	}

	body := bytes.NewBufferString(`{}`)
	req := httptest.NewRequest(http.MethodPost, "/api/subscribe/sub-1/refresh", body)
	req = mux.SetURLVars(req, map[string]string{"id": "sub-1"})
	rec := httptest.NewRecorder()

	handler.RefreshSubscriptionHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	content, err := os.ReadFile(cachedPath)
	if err != nil {
		t.Fatalf("os.ReadFile(%q) error = %v", cachedPath, err)
	}
	if !strings.Contains(string(content), "type: ss") {
		t.Fatalf("cached content missing converted ss node:\n%s", string(content))
	}

	sub, err := GetSubscription("sub-1", filepath.Join(dataDir, "subscriptions.json"))
	if err != nil {
		t.Fatalf("GetSubscription() error = %v", err)
	}
	if sub.LastError != "" {
		t.Fatalf("last error = %q, want empty", sub.LastError)
	}
	if !sub.LastErrorTime.IsZero() {
		t.Fatalf("last error time = %s, want zero", sub.LastErrorTime)
	}
}

func TestRefreshSubscriptionFileWriteFailureRecordsError(t *testing.T) {
	dataDir := t.TempDir()
	err := SaveSubscriptions([]models.Subscription{
		{
			ID:       "sub-1",
			Name:     "before",
			URL:      "https://example.com/old",
			Type:     "clash",
			FilePath: filepath.Join("missing", "sub-1.yaml"),
			FileSize: int64(len("old-content")),
			Status:   "active",
		},
	}, filepath.Join(dataDir, "subscriptions.json"))
	if err != nil {
		t.Fatalf("SaveSubscriptions() error = %v", err)
	}

	handler := NewHandler(&Config{
		DataDir:         dataDir,
		MaxFileSize:     1024,
		DownloadTimeout: 0,
	})
	handler.httpClient = &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader("ss://YWVzLTI1Ni1nY206cGFzcw==@example.com:443#demo")),
			}, nil
		}),
	}

	body := bytes.NewBufferString(`{}`)
	req := httptest.NewRequest(http.MethodPost, "/api/subscribe/sub-1/refresh", body)
	req = mux.SetURLVars(req, map[string]string{"id": "sub-1"})
	rec := httptest.NewRecorder()

	handler.RefreshSubscriptionHandler(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d; body = %s", rec.Code, http.StatusInternalServerError, rec.Body.String())
	}

	sub, err := GetSubscription("sub-1", filepath.Join(dataDir, "subscriptions.json"))
	if err != nil {
		t.Fatalf("GetSubscription() error = %v", err)
	}
	if got := sub.LastError; !strings.Contains(got, "no such file or directory") {
		t.Fatalf("last error = %q, want filesystem failure", got)
	}
	if sub.LastErrorTime.IsZero() {
		t.Fatal("last error time should be set on filesystem failure")
	}
}

func TestUpdateSubscriptionWithoutRefreshKeepsCachedFile(t *testing.T) {
	dataDir := t.TempDir()
	cachedName := "sub-1.yaml"
	cachedPath := filepath.Join(dataDir, cachedName)
	writeHandlerTestFile(t, cachedPath, []byte("cached-content"))

	lastCheck := time.Now().Add(-2 * time.Hour).UTC()
	updatedAt := time.Now().Add(-time.Hour).UTC()
	err := SaveSubscriptions([]models.Subscription{
		{
			ID:        "sub-1",
			Name:      "before",
			URL:       "https://example.com/old",
			Filter:    "旧规则",
			Type:      "clash",
			FilePath:  cachedName,
			FileSize:  int64(len("cached-content")),
			UpdatedAt: updatedAt,
			LastCheck: lastCheck,
			RequestHeaders: map[string]string{
				"User-Agent": "before-agent",
			},
			Status: "active",
		},
	}, filepath.Join(dataDir, "subscriptions.json"))
	if err != nil {
		t.Fatalf("SaveSubscriptions() error = %v", err)
	}

	handler := NewHandler(&Config{
		DataDir:         dataDir,
		MaxFileSize:     1024,
		DownloadTimeout: 0,
	})

	body := bytes.NewBufferString(`{"name":"after","url":"https://example.com/new","filter":"(?i)港|hk","request_headers":{"User-Agent":"after-agent"}}`)
	req := httptest.NewRequest(http.MethodPut, "/api/subscribe/sub-1", body)
	req = mux.SetURLVars(req, map[string]string{"id": "sub-1"})
	rec := httptest.NewRecorder()

	handler.SubscriptionHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	content, err := os.ReadFile(cachedPath)
	if err != nil {
		t.Fatalf("os.ReadFile(%q) error = %v", cachedPath, err)
	}
	if string(content) != "cached-content" {
		t.Fatalf("cached content = %q, want %q", string(content), "cached-content")
	}

	sub, err := GetSubscription("sub-1", filepath.Join(dataDir, "subscriptions.json"))
	if err != nil {
		t.Fatalf("GetSubscription() error = %v", err)
	}
	if sub.Name != "after" {
		t.Fatalf("name = %q, want %q", sub.Name, "after")
	}
	if sub.URL != "https://example.com/new" {
		t.Fatalf("url = %q, want %q", sub.URL, "https://example.com/new")
	}
	if sub.Filter != "(?i)港|hk" {
		t.Fatalf("filter = %q, want %q", sub.Filter, "(?i)港|hk")
	}
	if sub.RequestHeaders["User-Agent"] != "after-agent" {
		t.Fatalf("request header User-Agent = %q, want %q", sub.RequestHeaders["User-Agent"], "after-agent")
	}
	if sub.FileSize != int64(len("cached-content")) {
		t.Fatalf("file size = %d, want %d", sub.FileSize, len("cached-content"))
	}
	if !sub.LastCheck.Equal(lastCheck) {
		t.Fatalf("last check = %s, want %s", sub.LastCheck, lastCheck)
	}
	if !sub.UpdatedAt.After(updatedAt) {
		t.Fatalf("updated_at = %s, want time after %s", sub.UpdatedAt, updatedAt)
	}
}

func TestSubscribeHandlerDecompressesGzipResponseWhenAcceptEncodingHeaderIsSet(t *testing.T) {
	dataDir := t.TempDir()
	handler := NewHandler(&Config{
		DataDir:         dataDir,
		MaxFileSize:     4096,
		DownloadTimeout: 0,
	})

	handler.httpClient = &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if got := req.Header.Get("Accept-Encoding"); got != "gzip" {
				t.Fatalf("Accept-Encoding = %q, want %q", got, "gzip")
			}

			var buffer bytes.Buffer
			zipWriter := gzip.NewWriter(&buffer)
			if _, err := zipWriter.Write([]byte("proxies:\n  - name: zipped\n    type: ss\n    server: zip.example.com\n    port: 443\n    cipher: aes-256-gcm\n    password: pass\n")); err != nil {
				t.Fatalf("gzip write error = %v", err)
			}
			if err := zipWriter.Close(); err != nil {
				t.Fatalf("gzip close error = %v", err)
			}

			header := make(http.Header)
			header.Set("Content-Encoding", "gzip")

			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     header,
				Body:       io.NopCloser(bytes.NewReader(buffer.Bytes())),
			}, nil
		}),
	}

	body := bytes.NewBufferString(`{"name":"1yunti","url":"https://example.com/subscription","request_headers":{"Accept-Encoding":"gzip"}}`)
	req := httptest.NewRequest(http.MethodPost, "/api/subscribe", body)
	rec := httptest.NewRecorder()

	handler.SubscribeHandler(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d; body = %s", rec.Code, http.StatusCreated, rec.Body.String())
	}

	subs, err := ListSubscriptions(filepath.Join(dataDir, "subscriptions.json"))
	if err != nil {
		t.Fatalf("ListSubscriptions() error = %v", err)
	}
	content, err := os.ReadFile(filepath.Join(dataDir, subs[0].FilePath))
	if err != nil {
		t.Fatalf("os.ReadFile() error = %v", err)
	}
	if !strings.Contains(string(content), "name: zipped") {
		t.Fatalf("stored content = %q, want clash yaml content", string(content))
	}
}

func TestSubscribeHandlerRejectsUnsupportedSubscriptionContent(t *testing.T) {
	dataDir := t.TempDir()
	handler := NewHandler(&Config{
		DataDir:         dataDir,
		MaxFileSize:     1024,
		DownloadTimeout: 0,
	})
	handler.httpClient = &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader("not-a-subscription")),
			}, nil
		}),
	}

	body := bytes.NewBufferString(`{"name":"bad","url":"https://example.com/subscription"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/subscribe", body)
	rec := httptest.NewRecorder()

	handler.SubscribeHandler(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body = %s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}

	subs, err := ListSubscriptions(filepath.Join(dataDir, "subscriptions.json"))
	if err != nil {
		t.Fatalf("ListSubscriptions() error = %v", err)
	}
	if len(subs) != 0 {
		t.Fatalf("len(subs) = %d, want 0", len(subs))
	}

	matches, err := filepath.Glob(filepath.Join(dataDir, "*.yaml"))
	if err != nil {
		t.Fatalf("filepath.Glob() error = %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("unexpected cached files: %v", matches)
	}
}

func writeHandlerTestFile(t *testing.T, path string, data []byte) {
	t.Helper()

	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatalf("os.WriteFile(%q) error = %v", path, err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}
