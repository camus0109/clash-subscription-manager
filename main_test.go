package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"clash-subscription-manager/handlers"
	"clash-subscription-manager/models"

	"github.com/sirupsen/logrus"
)

func TestLoadConfigUsesDefaultsWhenFileEmpty(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.yaml")

	cfg, err := loadConfig(configPath)
	if err != nil {
		t.Fatalf("loadConfig() error = %v", err)
	}

	if cfg.Port != 8080 {
		t.Fatalf("cfg.Port = %d, want 8080", cfg.Port)
	}
	if cfg.DataDir != "./data" {
		t.Fatalf("cfg.DataDir = %q, want ./data", cfg.DataDir)
	}
	if cfg.DownloadTimeout != 30*time.Second {
		t.Fatalf("cfg.DownloadTimeout = %v, want %v", cfg.DownloadTimeout, 30*time.Second)
	}
}

func TestLoadConfigReadsYAMLFile(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	writeFile(t, configPath, []byte("port: 9090\ndata_dir: ./custom-data\nrate_limit: 12\ndownload_timeout: 45s\ntoken: test-token\n"))

	cfg, err := loadConfig(configPath)
	if err != nil {
		t.Fatalf("loadConfig() error = %v", err)
	}

	if cfg.Port != 9090 {
		t.Fatalf("cfg.Port = %d, want 9090", cfg.Port)
	}
	if cfg.DataDir != "./custom-data" {
		t.Fatalf("cfg.DataDir = %q, want ./custom-data", cfg.DataDir)
	}
	if cfg.RateLimit != 12 {
		t.Fatalf("cfg.RateLimit = %d, want 12", cfg.RateLimit)
	}
	if cfg.Token != "test-token" {
		t.Fatalf("cfg.Token = %q, want test-token", cfg.Token)
	}
	if cfg.DownloadTimeout != 45*time.Second {
		t.Fatalf("cfg.DownloadTimeout = %v, want %v", cfg.DownloadTimeout, 45*time.Second)
	}
}

func TestNewRouterServesHealthEndpoint(t *testing.T) {
	cfg := defaultConfig()
	router := newRouter(handlers.NewHandler(newHandlerConfig(cfg)))

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestNewRouterRequiresAuthenticationForManagementAPI(t *testing.T) {
	cfg := defaultConfig()
	cfg.DataDir = t.TempDir()
	router := newRouter(handlers.NewHandler(newHandlerConfig(cfg)))

	// Without a token the management API must reject the request.
	req := httptest.NewRequest(http.MethodGet, "/api/subscriptions", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status without token = %d, want %d", rec.Code, http.StatusUnauthorized)
	}

	// With the configured admin token the request is allowed.
	req = httptest.NewRequest(http.MethodGet, "/api/subscriptions", nil)
	req.Header.Set("Authorization", "Bearer "+cfg.Token)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status with token = %d, want %d; body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"success":true`) {
		t.Fatalf("unexpected body: %s", rec.Body.String())
	}
}

func TestSubscriptionDownloadLinkRequiresTokenAndResetInvalidatesOldLink(t *testing.T) {
	cfg := defaultConfig()
	cfg.DataDir = t.TempDir()
	handler := handlers.NewHandler(newHandlerConfig(cfg))
	router := newRouter(handler)

	cachedFile := "cache-test.yaml"
	if err := os.WriteFile(filepath.Join(cfg.DataDir, cachedFile), []byte("proxies:\n  - { name: test }\n"), 0644); err != nil {
		t.Fatalf("write cached file: %v", err)
	}
	sub, err := handlers.AddSubscription(models.Subscription{
		Name: "test", URL: "http://example.com/x", Type: "ss",
		UpdatedAt: time.Now(), Status: "active",
		FilePath: cachedFile, FileSize: 30,
	}, filepath.Join(cfg.DataDir, "subscriptions.json"))
	if err != nil {
		t.Fatalf("AddSubscription: %v", err)
	}
	if sub.AccessToken == "" {
		t.Fatal("expected generated access token on subscription")
	}

	get := func(path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}

	// No token -> rejected; wrong token -> rejected.
	if rec := get("/download/" + sub.ID); rec.Code != http.StatusNotFound {
		t.Fatalf("download without token status = %d, want 404", rec.Code)
	}
	if rec := get("/download/" + sub.ID + "?token=wrong"); rec.Code != http.StatusNotFound {
		t.Fatalf("download with wrong token status = %d, want 404", rec.Code)
	}
	// Correct token -> served.
	if rec := get("/download/" + sub.ID + "?token=" + sub.AccessToken); rec.Code != http.StatusOK {
		t.Fatalf("download with token status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}

	// Reset without admin auth is rejected.
	req := httptest.NewRequest(http.MethodPost, "/api/subscribe/"+sub.ID+"/reset", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("reset without admin token status = %d, want 401", rec.Code)
	}

	// Reset with admin auth rotates the token.
	req = httptest.NewRequest(http.MethodPost, "/api/subscribe/"+sub.ID+"/reset", nil)
	req.Header.Set("Authorization", "Bearer "+cfg.Token)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("reset status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	var payload struct {
		Success bool `json:"success"`
		Data    struct {
			AccessToken string `json:"access_token"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode reset response: %v", err)
	}
	newToken := payload.Data.AccessToken
	if newToken == "" || newToken == sub.AccessToken {
		t.Fatalf("reset should rotate token: old=%q new=%q", sub.AccessToken, newToken)
	}

	// Old token now dead, new token works.
	if rec := get("/download/" + sub.ID + "?token=" + sub.AccessToken); rec.Code != http.StatusNotFound {
		t.Fatalf("old token should be rejected after reset, status = %d", rec.Code)
	}
	if rec := get("/download/" + sub.ID + "?token=" + newToken); rec.Code != http.StatusOK {
		t.Fatalf("new token should be accepted after reset, status = %d; body = %s", rec.Code, rec.Body.String())
	}
}

func TestTemplateRenderLinkRequiresToken(t *testing.T) {
	cfg := defaultConfig()
	cfg.DataDir = t.TempDir()
	handler := handlers.NewHandler(newHandlerConfig(cfg))
	router := newRouter(handler)

	tpl, err := handlers.AddTemplate(models.Template{
		Name: "default", Content: "port: 7890\n", UpdatedAt: time.Now(),
	}, filepath.Join(cfg.DataDir, "templates.json"))
	if err != nil {
		t.Fatalf("AddTemplate: %v", err)
	}
	if tpl.AccessToken == "" {
		t.Fatal("expected generated access token on template")
	}

	get := func(path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}

	if rec := get("/api/templates/" + tpl.ID + "/render"); rec.Code != http.StatusNotFound {
		t.Fatalf("render without token status = %d, want 404", rec.Code)
	}
	if rec := get("/api/templates/" + tpl.ID + "/render?token=" + tpl.AccessToken); rec.Code != http.StatusOK {
		t.Fatalf("render with token status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}

	// Reset via management API (with admin token) invalidates the old render link.
	req := httptest.NewRequest(http.MethodPost, "/api/templates/"+tpl.ID+"/reset", nil)
	req.Header.Set("Authorization", "Bearer "+cfg.Token)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("template reset status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	var payload struct {
		Success bool `json:"success"`
		Data    struct {
			AccessToken string `json:"access_token"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode reset response: %v", err)
	}
	if rec := get("/api/templates/" + tpl.ID + "/render?token=" + tpl.AccessToken); rec.Code != http.StatusNotFound {
		t.Fatalf("old template token should be rejected after reset, status = %d", rec.Code)
	}
	if rec := get("/api/templates/" + tpl.ID + "/render?token=" + payload.Data.AccessToken); rec.Code != http.StatusOK {
		t.Fatalf("new template token should be accepted after reset, status = %d", rec.Code)
	}
}

func TestNewRouterServesLogoAssets(t *testing.T) {
	cfg := defaultConfig()
	cfg.DataDir = t.TempDir()
	router := newRouter(handlers.NewHandler(newHandlerConfig(cfg)))

	for _, path := range []string{"/static/img/logo-primary.svg", "/static/img/logo-icon.svg"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()

		router.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("%s status = %d, want %d", path, rec.Code, http.StatusOK)
		}
		if !strings.Contains(rec.Body.String(), "<svg") {
			t.Fatalf("%s body missing svg markup: %s", path, rec.Body.String())
		}
	}
}

func TestLogStartupIncludesVersionAndPort(t *testing.T) {
	var buffer bytes.Buffer
	originalOut := logger.Out
	originalFormatter := logger.Formatter
	originalLevel := logger.Level
	t.Cleanup(func() {
		logger.SetOutput(originalOut)
		logger.SetFormatter(originalFormatter)
		logger.SetLevel(originalLevel)
	})

	logger.SetOutput(&buffer)
	logger.SetFormatter(&logrus.TextFormatter{
		DisableTimestamp: true,
		DisableColors:    true,
	})
	logger.SetLevel(logrus.InfoLevel)

	logStartup(defaultConfig())

	output := buffer.String()
	if !strings.Contains(output, Version) {
		t.Fatalf("startup log = %q, want version %q", output, Version)
	}
	if !strings.Contains(output, "8080") {
		t.Fatalf("startup log = %q, want port %q", output, "8080")
	}
}

func TestResolveAdminTokenUsesEnvVarWithoutRewritingConfig(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	writeFile(t, cfgPath, []byte("port: 8080\ntoken: cfg-token\n"))

	cfg := defaultConfig()
	resolved, err := resolveAdminToken("env-token", cfg, cfgPath)
	if err != nil {
		t.Fatalf("resolveAdminToken() error = %v", err)
	}
	if resolved.Token != "env-token" {
		t.Fatalf("Token = %q, want env-token", resolved.Token)
	}

	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if strings.Contains(string(data), "env-token") {
		t.Fatalf("env token should not be written to config file: %s", data)
	}
}

func TestResolveAdminTokenKeepsConfiguredToken(t *testing.T) {
	cfg := defaultConfig()
	cfg.Token = "configured-token"

	resolved, err := resolveAdminToken("", cfg, filepath.Join(t.TempDir(), "config.yaml"))
	if err != nil {
		t.Fatalf("resolveAdminToken() error = %v", err)
	}
	if resolved.Token != "configured-token" {
		t.Fatalf("Token = %q, want configured-token", resolved.Token)
	}
}

func TestResolveAdminTokenGeneratesAndPersistsWhenUnset(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	writeFile(t, cfgPath, []byte("port: 8080\ntoken: your-secret-token\n"))

	resolved, err := resolveAdminToken("", defaultConfig(), cfgPath)
	if err != nil {
		t.Fatalf("resolveAdminToken() error = %v", err)
	}
	if resolved.Token == "" || resolved.Token == "your-secret-token" {
		t.Fatalf("expected a generated token, got %q", resolved.Token)
	}

	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	wantLine := "token: \"" + resolved.Token + "\""
	if !strings.Contains(string(data), wantLine) {
		t.Fatalf("config should contain %q, got: %s", wantLine, data)
	}

	// Reloading the config must pick up the persisted token.
	cfg2, err := loadConfig(cfgPath)
	if err != nil {
		t.Fatalf("loadConfig() error = %v", err)
	}
	if cfg2.Token != resolved.Token {
		t.Fatalf("reloaded Token = %q, want %q", cfg2.Token, resolved.Token)
	}
}

func TestResolveAdminTokenCreatesConfigFileWhenMissing(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")

	resolved, err := resolveAdminToken("", defaultConfig(), cfgPath)
	if err != nil {
		t.Fatalf("resolveAdminToken() error = %v", err)
	}

	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("config should have been created: %v", err)
	}
	if !strings.Contains(string(data), "token: \""+resolved.Token+"\"") {
		t.Fatalf("config missing token line, got: %s", data)
	}
}

func TestResolveAdminTokenAppendsTokenKeyWhenMissing(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	writeFile(t, cfgPath, []byte("port: 8080\nhttps: false\n"))

	resolved, err := resolveAdminToken("", defaultConfig(), cfgPath)
	if err != nil {
		t.Fatalf("resolveAdminToken() error = %v", err)
	}

	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "port: 8080") {
		t.Fatalf("existing config content lost: %s", content)
	}
	if !strings.Contains(content, "token: \""+resolved.Token+"\"") {
		t.Fatalf("config missing appended token line, got: %s", content)
	}
}

func writeFile(t *testing.T, path string, data []byte) {
	t.Helper()

	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatalf("os.WriteFile(%q) error = %v", path, err)
	}
}
