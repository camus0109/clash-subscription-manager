package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"clash-subscription-manager/models"

	"github.com/gorilla/mux"
)

func TestTemplateLifecycleAndDefaultSelection(t *testing.T) {
	tempDir := t.TempDir()
	templatesFile := filepath.Join(tempDir, "templates.json")

	first, err := AddTemplate(models.Template{
		Name:      "default",
		Content:   "port: 7890\n",
		UpdatedAt: time.Now(),
	}, templatesFile)
	if err != nil {
		t.Fatalf("AddTemplate(first) error = %v", err)
	}
	if !first.IsDefault {
		t.Fatalf("first template should become default")
	}

	second, err := AddTemplate(models.Template{
		Name:      "backup",
		Content:   "mixed-port: 7890\n",
		UpdatedAt: time.Now(),
	}, templatesFile)
	if err != nil {
		t.Fatalf("AddTemplate(second) error = %v", err)
	}
	if second.IsDefault {
		t.Fatalf("second template should not be default initially")
	}

	if _, err := SetDefaultTemplate(second.ID, templatesFile); err != nil {
		t.Fatalf("SetDefaultTemplate() error = %v", err)
	}

	templates, err := ListTemplates(templatesFile)
	if err != nil {
		t.Fatalf("ListTemplates() error = %v", err)
	}
	if len(templates) != 2 {
		t.Fatalf("len(templates) = %d, want 2", len(templates))
	}

	var defaultCount int
	for _, item := range templates {
		if item.IsDefault {
			defaultCount++
			if item.ID != second.ID {
				t.Fatalf("default template id = %q, want %q", item.ID, second.ID)
			}
		}
	}
	if defaultCount != 1 {
		t.Fatalf("defaultCount = %d, want 1", defaultCount)
	}
}

func TestRenderTemplateHandlerInjectsCurrentSubscriptionsAsProxyProviders(t *testing.T) {
	dataDir := t.TempDir()

	if err := SaveSubscriptions([]models.Subscription{
		{
			ID:        "sub-west",
			Name:      "🛫 West",
			URL:       "https://example.com/west",
			Filter:    "(?i)港|hk|hongkong|hong kong",
			FilePath:  "west-source.yaml",
			Type:      "clash",
			UpdatedAt: time.Now(),
			Status:    "active",
		},
		{
			ID:        "sub-ai",
			Name:      "✨ AI Select",
			URL:       "https://example.com/ai",
			FilePath:  "ai-source.yaml",
			Type:      "clash",
			UpdatedAt: time.Now(),
			Status:    "active",
		},
	}, filepath.Join(dataDir, "subscriptions.json")); err != nil {
		t.Fatalf("SaveSubscriptions() error = %v", err)
	}

	templateRecord, err := AddTemplate(models.Template{
		Name: "main",
		Content: strings.TrimSpace(`
listeners:
  - name: USASocks
    type: socks
    port: 10809
    proxy: 🇺🇸 USA
proxy-providers:
  old:
    type: http
    url: https://invalid.example.com
proxy-groups:
  - name: 🚩 PROXY
    type: select
    proxies:
      - ♻️ 自动选择
rules:
  - MATCH,🚩 PROXY
`) + "\n",
		UpdatedAt: time.Now(),
		IsDefault: true,
	}, filepath.Join(dataDir, "templates.json"))
	if err != nil {
		t.Fatalf("AddTemplate() error = %v", err)
	}

	handler := NewHandler(&Config{
		DataDir:         dataDir,
		MaxFileSize:     4096,
		DownloadTimeout: 0,
		RateLimit:       10,
	})

	req := httptest.NewRequest(http.MethodGet, "/api/templates/"+templateRecord.ID+"/render", nil)
	req = mux.SetURLVars(req, map[string]string{"id": templateRecord.ID})
	rec := httptest.NewRecorder()

	handler.RenderTemplateHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	body := rec.Body.String()
	if strings.Contains(body, "https://invalid.example.com") {
		t.Fatalf("body should replace template proxy-providers, got %s", body)
	}
	if !strings.Contains(body, "proxy-providers:") {
		t.Fatalf("body missing proxy-providers section: %s", body)
	}
	if !strings.Contains(body, "/download/sub-west") {
		t.Fatalf("body missing generated download url: %s", body)
	}
	if !strings.Contains(body, "additional-prefix: \"West |\"") {
		t.Fatalf("body missing sanitized additional-prefix: %s", body)
	}
	if !strings.Contains(body, `filter: "(?i)港|hk|hongkong|hong kong"`) {
		t.Fatalf("body missing provider filter: %s", body)
	}
	if !strings.Contains(body, "\"AI Select |\"") {
		t.Fatalf("body missing second provider prefix: %s", body)
	}
	if strings.Contains(body, "ai-source.yaml\n    filter:") {
		t.Fatalf("body should omit empty filter field: %s", body)
	}
}

func TestRenderRawTemplatePreservesCompleteYAML(t *testing.T) {
	dataDir := t.TempDir()
	content := "# keep this comment\r\ndefaults: &defaults\r\n  interval: 300\r\nproxy-providers:\r\n  main:\r\n    <<: *defaults\r\n    type: http\r\nproxies: []\r\n"
	templateRecord, err := AddTemplate(models.Template{
		Name: "raw", Mode: "raw", Content: content, UpdatedAt: time.Now(),
	}, filepath.Join(dataDir, "templates.json"))
	if err != nil {
		t.Fatalf("AddTemplate() error = %v", err)
	}
	handler := NewHandler(&Config{DataDir: dataDir, MaxFileSize: 4096})
	req := mux.SetURLVars(httptest.NewRequest(http.MethodGet, "/templates/"+templateRecord.ID+"/render", nil), map[string]string{"id": templateRecord.ID})
	rec := httptest.NewRecorder()
	handler.RenderTemplateHandler(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body = %s", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != content {
		t.Fatalf("raw content changed: got %q, want %q", rec.Body.String(), content)
	}
	proxiesReq := mux.SetURLVars(httptest.NewRequest(http.MethodGet, "/templates/"+templateRecord.ID+"/render-proxies", nil), map[string]string{"id": templateRecord.ID})
	proxiesRec := httptest.NewRecorder()
	handler.RenderTemplateProxiesHandler(proxiesRec, proxiesReq)
	if proxiesRec.Code != http.StatusBadRequest {
		t.Fatalf("raw proxies status = %d, want %d", proxiesRec.Code, http.StatusBadRequest)
	}
}

func TestCreateTemplateRejectsInvalidYAML(t *testing.T) {
	dataDir := t.TempDir()
	templatesFile := filepath.Join(dataDir, "templates.json")
	if _, err := AddTemplate(models.Template{Name: "existing", Mode: "raw", Content: "port: 7890\n", UpdatedAt: time.Now()}, templatesFile); err != nil {
		t.Fatalf("AddTemplate() error = %v", err)
	}
	handler := NewHandler(&Config{DataDir: dataDir, MaxFileSize: 4096})
	req := httptest.NewRequest(http.MethodPost, "/templates", strings.NewReader(`{"name":"bad","mode":"raw","content":"port: ["}`))
	rec := httptest.NewRecorder()
	handler.TemplatesHandler(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d; body = %s", rec.Code, rec.Body.String())
	}
	templates, err := ListTemplates(templatesFile)
	if err != nil {
		t.Fatalf("ListTemplates() error = %v", err)
	}
	if len(templates) != 1 || templates[0].Name != "existing" {
		t.Fatalf("invalid request mutated templates: %+v", templates)
	}
}

func TestCreateTemplateRejectsDuplicateYAMLKeys(t *testing.T) {
	dataDir := t.TempDir()
	handler := NewHandler(&Config{DataDir: dataDir, MaxFileSize: 4096})
	for _, content := range []string{
		"port: 7890\nport: 7891\n",
		"profile:\n  store-selected: true\n  store-selected: false\n",
	} {
		req := httptest.NewRequest(http.MethodPost, "/templates", strings.NewReader(`{"name":"duplicate","mode":"raw","content":`+strconv.Quote(content)+`}`))
		rec := httptest.NewRecorder()
		handler.TemplatesHandler(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("duplicate YAML status = %d; body = %s", rec.Code, rec.Body.String())
		}
	}
}

func TestRenderTemplateHandlerUsesOnlySelectedSubscriptions(t *testing.T) {
	dataDir := t.TempDir()

	if err := SaveSubscriptions([]models.Subscription{
		{
			ID:        "sub-west",
			Name:      "West",
			URL:       "https://example.com/west",
			FilePath:  "west-source.yaml",
			Type:      "clash",
			UpdatedAt: time.Now(),
			Status:    "active",
		},
		{
			ID:        "sub-ai",
			Name:      "AI",
			URL:       "https://example.com/ai",
			FilePath:  "ai-source.yaml",
			Type:      "clash",
			UpdatedAt: time.Now(),
			Status:    "active",
		},
	}, filepath.Join(dataDir, "subscriptions.json")); err != nil {
		t.Fatalf("SaveSubscriptions() error = %v", err)
	}

	useAll := false
	templateRecord, err := AddTemplate(models.Template{
		Name:                    "selected-only",
		Content:                 "proxy-providers: {}\nrules:\n  - MATCH,DIRECT\n",
		SelectedSubscriptionIDs: []string{"sub-ai"},
		UseAllSubscriptions:     &useAll,
		UpdatedAt:               time.Now(),
		IsDefault:               true,
	}, filepath.Join(dataDir, "templates.json"))
	if err != nil {
		t.Fatalf("AddTemplate() error = %v", err)
	}

	handler := NewHandler(&Config{
		DataDir:         dataDir,
		MaxFileSize:     4096,
		DownloadTimeout: 0,
		RateLimit:       10,
	})

	req := httptest.NewRequest(http.MethodGet, "/api/templates/"+templateRecord.ID+"/render", nil)
	req = mux.SetURLVars(req, map[string]string{"id": templateRecord.ID})
	rec := httptest.NewRecorder()

	handler.RenderTemplateHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	body := rec.Body.String()
	if !strings.Contains(body, "/download/sub-ai") {
		t.Fatalf("body missing selected subscription download url: %s", body)
	}
	if strings.Contains(body, "/download/sub-west") {
		t.Fatalf("body should exclude unselected subscription: %s", body)
	}
}

func TestRenderTemplateHandlerKeepsEmojiAsUTF8Characters(t *testing.T) {
	dataDir := t.TempDir()

	templateRecord, err := AddTemplate(models.Template{
		Name: "emoji",
		Content: strings.TrimSpace(`
proxy-groups:
  - {name: 🇺🇸 USA, type: select, proxies: [DIRECT]}
rules:
  - MATCH,DIRECT
`) + "\n",
		UpdatedAt: time.Now(),
		IsDefault: true,
	}, filepath.Join(dataDir, "templates.json"))
	if err != nil {
		t.Fatalf("AddTemplate() error = %v", err)
	}

	handler := NewHandler(&Config{
		DataDir:         dataDir,
		MaxFileSize:     4096,
		DownloadTimeout: 0,
		RateLimit:       10,
	})

	req := httptest.NewRequest(http.MethodGet, "/api/templates/"+templateRecord.ID+"/render", nil)
	req = mux.SetURLVars(req, map[string]string{"id": templateRecord.ID})
	rec := httptest.NewRecorder()

	handler.RenderTemplateHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	body := rec.Body.String()
	if strings.Contains(body, `\U0001F1FA\U0001F1F8`) {
		t.Fatalf("body should contain emoji characters instead of unicode escapes: %s", body)
	}
	if !strings.Contains(body, "name: 🇺🇸 USA") {
		t.Fatalf("body should preserve utf-8 emoji characters: %s", body)
	}
}

func TestRenderTemplateHandlerPreservesNonProviderTemplateText(t *testing.T) {
	dataDir := t.TempDir()
	if err := SaveSubscriptions([]models.Subscription{
		{
			ID:        "sub-west",
			Name:      "🛫 West",
			URL:       "https://example.com/west",
			FilePath:  "west-source.yaml",
			Type:      "clash",
			UpdatedAt: time.Now(),
			Status:    "active",
		},
	}, filepath.Join(dataDir, "subscriptions.json")); err != nil {
		t.Fatalf("SaveSubscriptions() error = %v", err)
	}

	templateContent := strings.TrimSpace(`
proxy-groups:
  - {
      name: 🇺🇸 USA,
      type: fallback,
      lazy:false,
      use: [🛫 West],
      filter: "美国|United States",
    }
rules:
  - GEOSITE,anthropic,🤖 人工智能
  - GEOSITE,category-ai-chat-!cn,🤖 人工智能
  - GEOIP,google,🚩 PROXY
  - MATCH,🚩 PROXY
proxy-providers:
  stale:
    type: http
`) + "\n"

	templateRecord, err := AddTemplate(models.Template{
		Name:      "preserve",
		Content:   templateContent,
		UpdatedAt: time.Now(),
		IsDefault: true,
	}, filepath.Join(dataDir, "templates.json"))
	if err != nil {
		t.Fatalf("AddTemplate() error = %v", err)
	}

	handler := NewHandler(&Config{
		DataDir:         dataDir,
		MaxFileSize:     4096,
		DownloadTimeout: 0,
		RateLimit:       10,
	})

	req := httptest.NewRequest(http.MethodGet, "/api/templates/"+templateRecord.ID+"/render", nil)
	req = mux.SetURLVars(req, map[string]string{"id": templateRecord.ID})
	rec := httptest.NewRecorder()

	handler.RenderTemplateHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	body := rec.Body.String()
	if !strings.Contains(body, "lazy:false,") {
		t.Fatalf("body should preserve raw template flow map text: %s", body)
	}
	if strings.Contains(body, `'lazy:false': ''`) {
		t.Fatalf("body should not rewrite lazy:false into invalid mapping: %s", body)
	}
	if !strings.Contains(body, "- GEOSITE,anthropic,🤖 人工智能") {
		t.Fatalf("body should preserve raw rule text without forced quotes: %s", body)
	}
	if !strings.Contains(body, "- MATCH,🚩 PROXY") {
		t.Fatalf("body should preserve raw match rule text without forced quotes: %s", body)
	}
	if strings.Contains(body, "stale:") {
		t.Fatalf("body should replace stale proxy-providers section: %s", body)
	}
}

func TestRenderTemplateProxiesHandlerExpandsFilteredSubscriptionProxies(t *testing.T) {
	dataDir := t.TempDir()

	if err := SaveSubscriptions([]models.Subscription{
		{
			ID:        "sub-hk",
			Name:      "HK",
			URL:       "https://example.com/hk",
			Filter:    "(?i)港|hk|hongkong|hong kong",
			FilePath:  "hk.yaml",
			Type:      "clash",
			UpdatedAt: time.Now(),
			Status:    "active",
		},
		{
			ID:        "sub-sg",
			Name:      "SG",
			URL:       "https://example.com/sg",
			Filter:    "(?i)新加坡|singapore|sg",
			FilePath:  "sg.yaml",
			Type:      "clash",
			UpdatedAt: time.Now(),
			Status:    "active",
		},
	}, filepath.Join(dataDir, "subscriptions.json")); err != nil {
		t.Fatalf("SaveSubscriptions() error = %v", err)
	}

	writeTemplateTestFile(t, filepath.Join(dataDir, "hk.yaml"), []byte(strings.TrimSpace(`
proxies:
  - { name: "HK 01", type: ss, server: hk1.example.com, port: 443, cipher: aes-128-gcm, password: secret }
  - { name: "US 01", type: ss, server: us1.example.com, port: 443, cipher: aes-128-gcm, password: secret }
`)+"\n"))
	writeTemplateTestFile(t, filepath.Join(dataDir, "sg.yaml"), []byte(strings.TrimSpace(`
proxies:
  - { name: "Singapore 01", type: ss, server: sg1.example.com, port: 443, cipher: aes-128-gcm, password: secret }
  - { name: "Japan 01", type: ss, server: jp1.example.com, port: 443, cipher: aes-128-gcm, password: secret }
`)+"\n"))

	templateRecord, err := AddTemplate(models.Template{
		Name: "expanded",
		Content: strings.TrimSpace(`
proxy-providers:
  old:
    type: http
proxies:
  - { name: OLD, type: direct }
proxy-groups:
  - {
      name: 🚀 手动切换,
      type: select,
      proxies: [HK 01, Singapore 01],
    }
rules:
  - MATCH,🚀 手动切换
`) + "\n",
		UpdatedAt: time.Now(),
		IsDefault: true,
	}, filepath.Join(dataDir, "templates.json"))
	if err != nil {
		t.Fatalf("AddTemplate() error = %v", err)
	}

	handler := NewHandler(&Config{
		DataDir:         dataDir,
		MaxFileSize:     4096,
		DownloadTimeout: 0,
		RateLimit:       10,
	})

	req := httptest.NewRequest(http.MethodGet, "/api/templates/"+templateRecord.ID+"/render-proxies", nil)
	req = mux.SetURLVars(req, map[string]string{"id": templateRecord.ID})
	rec := httptest.NewRecorder()

	handler.RenderTemplateProxiesHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	body := rec.Body.String()
	if strings.Contains(body, "proxy-providers:") {
		t.Fatalf("body should not contain proxy-providers section in proxies mode: %s", body)
	}
	if !strings.Contains(body, "proxies:") {
		t.Fatalf("body missing proxies section: %s", body)
	}
	if !strings.Contains(body, `name: "HK 01"`) && !strings.Contains(body, "name: HK 01") {
		t.Fatalf("body missing matched HK proxy: %s", body)
	}
	if !strings.Contains(body, `name: "Singapore 01"`) && !strings.Contains(body, "name: Singapore 01") {
		t.Fatalf("body missing matched SG proxy: %s", body)
	}
	if strings.Contains(body, "US 01") || strings.Contains(body, "Japan 01") || strings.Contains(body, "name: OLD") {
		t.Fatalf("body should only include filtered proxies: %s", body)
	}
	if !strings.Contains(body, "- MATCH,🚀 手动切换") {
		t.Fatalf("body should preserve non-proxies template content: %s", body)
	}
}

func TestRenderTemplateProxiesHandlerUsesSelectedSubscriptionsWithoutFilter(t *testing.T) {
	dataDir := t.TempDir()

	if err := SaveSubscriptions([]models.Subscription{
		{
			ID:        "sub-all",
			Name:      "All Nodes",
			URL:       "https://example.com/all",
			FilePath:  "all.yaml",
			Type:      "clash",
			UpdatedAt: time.Now(),
			Status:    "active",
		},
		{
			ID:        "sub-jp",
			Name:      "JP Only",
			URL:       "https://example.com/jp",
			Filter:    "(?i)japan|jp",
			FilePath:  "jp.yaml",
			Type:      "clash",
			UpdatedAt: time.Now(),
			Status:    "active",
		},
	}, filepath.Join(dataDir, "subscriptions.json")); err != nil {
		t.Fatalf("SaveSubscriptions() error = %v", err)
	}

	writeTemplateTestFile(t, filepath.Join(dataDir, "all.yaml"), []byte(strings.TrimSpace(`
proxies:
  - { name: "All 01", type: ss, server: all1.example.com, port: 443, cipher: aes-128-gcm, password: secret }
  - { name: "All 02", type: ss, server: all2.example.com, port: 443, cipher: aes-128-gcm, password: secret }
`)+"\n"))
	writeTemplateTestFile(t, filepath.Join(dataDir, "jp.yaml"), []byte(strings.TrimSpace(`
proxies:
  - { name: "Japan 01", type: ss, server: jp1.example.com, port: 443, cipher: aes-128-gcm, password: secret }
`)+"\n"))

	useAll := false
	templateRecord, err := AddTemplate(models.Template{
		Name:                    "selected-expanded",
		Content:                 "proxy-providers: {}\nrules:\n  - MATCH,DIRECT\n",
		SelectedSubscriptionIDs: []string{"sub-all"},
		UseAllSubscriptions:     &useAll,
		UpdatedAt:               time.Now(),
		IsDefault:               true,
	}, filepath.Join(dataDir, "templates.json"))
	if err != nil {
		t.Fatalf("AddTemplate() error = %v", err)
	}

	handler := NewHandler(&Config{
		DataDir:         dataDir,
		MaxFileSize:     4096,
		DownloadTimeout: 0,
		RateLimit:       10,
	})

	req := httptest.NewRequest(http.MethodGet, "/api/templates/"+templateRecord.ID+"/render-proxies", nil)
	req = mux.SetURLVars(req, map[string]string{"id": templateRecord.ID})
	rec := httptest.NewRecorder()

	handler.RenderTemplateProxiesHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	body := rec.Body.String()
	if !strings.Contains(body, "All 01") || !strings.Contains(body, "All 02") {
		t.Fatalf("body should include all proxies from selected subscription without filter: %s", body)
	}
	if strings.Contains(body, "Japan 01") {
		t.Fatalf("body should exclude proxies from unselected subscription: %s", body)
	}
}

func TestRenderDefaultTemplateProxiesHandlerUsesDefaultTemplate(t *testing.T) {
	dataDir := t.TempDir()

	if err := SaveSubscriptions([]models.Subscription{
		{
			ID:        "sub-hk",
			Name:      "HK",
			URL:       "https://example.com/hk",
			Filter:    "(?i)hk",
			FilePath:  "hk.yaml",
			Type:      "clash",
			UpdatedAt: time.Now(),
			Status:    "active",
		},
	}, filepath.Join(dataDir, "subscriptions.json")); err != nil {
		t.Fatalf("SaveSubscriptions() error = %v", err)
	}

	writeTemplateTestFile(t, filepath.Join(dataDir, "hk.yaml"), []byte("proxies:\n  - { name: HK 02, type: direct }\n"))

	if _, err := AddTemplate(models.Template{
		Name: "default-expanded",
		Content: strings.TrimSpace(`
proxy-providers: {}
rules:
  - MATCH,DIRECT
`) + "\n",
		UpdatedAt: time.Now(),
		IsDefault: true,
	}, filepath.Join(dataDir, "templates.json")); err != nil {
		t.Fatalf("AddTemplate() error = %v", err)
	}

	handler := NewHandler(&Config{
		DataDir:         dataDir,
		MaxFileSize:     4096,
		DownloadTimeout: 0,
		RateLimit:       10,
	})

	req := httptest.NewRequest(http.MethodGet, "/api/templates/default/render-proxies", nil)
	rec := httptest.NewRecorder()

	handler.RenderDefaultTemplateProxiesHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "HK 02") {
		t.Fatalf("body missing proxy from default template render-proxies: %s", rec.Body.String())
	}
}

func TestRenderTemplateProxiesHandlerReplacesTopLevelProxiesOnly(t *testing.T) {
	dataDir := t.TempDir()

	if err := SaveSubscriptions([]models.Subscription{
		{
			ID:        "sub-hk",
			Name:      "HK",
			URL:       "https://example.com/hk",
			Filter:    "(?i)香港|hk",
			FilePath:  "hk.yaml",
			Type:      "clash",
			UpdatedAt: time.Now(),
			Status:    "active",
		},
	}, filepath.Join(dataDir, "subscriptions.json")); err != nil {
		t.Fatalf("SaveSubscriptions() error = %v", err)
	}

	writeTemplateTestFile(t, filepath.Join(dataDir, "hk.yaml"), []byte(strings.TrimSpace(`
proxies:
  - { name: "香港实验性 IEPL 专线 1", type: trojan, server: hk.example.com, port: 443, password: secret, sni: m.ctrip.com, udp: true }
`)+"\n"))

	templateRecord, err := AddTemplate(models.Template{
		Name: "top-level-proxies",
		Content: strings.TrimSpace(`
proxy-groups:
  - {
      name: 🚀 手动切换,
      type: select,
      interval: 300,
      proxies: [DIRECT],
    }
proxies:
  - { name: OLD, type: direct }
rule-providers:
  AWAvenue-Ads:
    type: http
rules:
  - MATCH,🚀 手动切换
`) + "\n",
		UpdatedAt: time.Now(),
		IsDefault: true,
	}, filepath.Join(dataDir, "templates.json"))
	if err != nil {
		t.Fatalf("AddTemplate() error = %v", err)
	}

	handler := NewHandler(&Config{
		DataDir:         dataDir,
		MaxFileSize:     4096,
		DownloadTimeout: 0,
		RateLimit:       10,
	})

	req := httptest.NewRequest(http.MethodGet, "/api/templates/"+templateRecord.ID+"/render-proxies", nil)
	req = mux.SetURLVars(req, map[string]string{"id": templateRecord.ID})
	rec := httptest.NewRecorder()

	handler.RenderTemplateProxiesHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	body := rec.Body.String()
	if !strings.Contains(body, "interval: 300,") {
		t.Fatalf("body should preserve proxy-group flow content: %s", body)
	}
	if !strings.Contains(body, "proxies: [DIRECT]") {
		t.Fatalf("body should preserve nested proxy-group proxies list: %s", body)
	}
	if !strings.Contains(body, "rule-providers:") || !strings.Contains(body, "AWAvenue-Ads:") {
		t.Fatalf("body should preserve following top-level sections: %s", body)
	}
	if strings.Contains(body, "name: OLD") {
		t.Fatalf("body should replace only the top-level proxies section: %s", body)
	}
	if !strings.Contains(body, "香港实验性 IEPL 专线 1") {
		t.Fatalf("body should include expanded proxy in top-level proxies section: %s", body)
	}
}

func TestRenderTemplateProxiesHandlerKeepsEmojiAsUTF8Characters(t *testing.T) {
	dataDir := t.TempDir()

	if err := SaveSubscriptions([]models.Subscription{
		{
			ID:        "sub-hk",
			Name:      "HK",
			URL:       "https://example.com/hk",
			Filter:    "(?i)香港|hk",
			FilePath:  "hk.yaml",
			Type:      "clash",
			UpdatedAt: time.Now(),
			Status:    "active",
		},
	}, filepath.Join(dataDir, "subscriptions.json")); err != nil {
		t.Fatalf("SaveSubscriptions() error = %v", err)
	}

	writeTemplateTestFile(t, filepath.Join(dataDir, "hk.yaml"), []byte(strings.TrimSpace(`
proxies:
  - { name: "🇭🇰 香港实验性 IEPL 专线 1", type: trojan, server: hk.example.com, port: 443, password: secret, sni: m.ctrip.com, udp: true }
`)+"\n"))

	templateRecord, err := AddTemplate(models.Template{
		Name: "emoji-expanded",
		Content: strings.TrimSpace(`
proxies: []
rules:
  - MATCH,DIRECT
`) + "\n",
		UpdatedAt: time.Now(),
		IsDefault: true,
	}, filepath.Join(dataDir, "templates.json"))
	if err != nil {
		t.Fatalf("AddTemplate() error = %v", err)
	}

	handler := NewHandler(&Config{
		DataDir:         dataDir,
		MaxFileSize:     4096,
		DownloadTimeout: 0,
		RateLimit:       10,
	})

	req := httptest.NewRequest(http.MethodGet, "/api/templates/"+templateRecord.ID+"/render-proxies", nil)
	req = mux.SetURLVars(req, map[string]string{"id": templateRecord.ID})
	rec := httptest.NewRecorder()

	handler.RenderTemplateProxiesHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	body := rec.Body.String()
	if strings.Contains(body, `\U0001F1ED\U0001F1F0`) {
		t.Fatalf("body should contain emoji characters instead of unicode escapes: %s", body)
	}
	if !strings.Contains(body, "🇭🇰 香港实验性 IEPL 专线 1") {
		t.Fatalf("body should preserve emoji characters in expanded proxies mode: %s", body)
	}
}

func writeTemplateTestFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatalf("os.WriteFile(%q) error = %v", path, err)
	}
}

func TestRenderTemplateHandlerUsesCustomSubscriptionPrefix(t *testing.T) {
	dataDir := t.TempDir()

	if err := SaveSubscriptions([]models.Subscription{
		{
			ID:        "sub-west",
			Name:      "🛫 West",
			URL:       "https://example.com/west",
			FilePath:  "west-source.yaml",
			Type:      "clash",
			UpdatedAt: time.Now(),
			Status:    "active",
		},
		{
			ID:        "sub-ai",
			Name:      "✨ AI Select",
			URL:       "https://example.com/ai",
			FilePath:  "ai-source.yaml",
			Type:      "clash",
			UpdatedAt: time.Now(),
			Status:    "active",
		},
	}, filepath.Join(dataDir, "subscriptions.json")); err != nil {
		t.Fatalf("SaveSubscriptions() error = %v", err)
	}

	templateRecord, err := AddTemplate(models.Template{
		Name: "custom-prefix",
		Content: strings.TrimSpace(`
proxy-providers: {}
rules:
  - MATCH,DIRECT
`) + "\n",
		SubscriptionPrefixes: map[string]string{
			"sub-west": "Custom HK |",
			"sub-ai":   "My AI Nodes |",
		},
		UpdatedAt: time.Now(),
		IsDefault: true,
	}, filepath.Join(dataDir, "templates.json"))
	if err != nil {
		t.Fatalf("AddTemplate() error = %v", err)
	}

	handler := NewHandler(&Config{
		DataDir:         dataDir,
		MaxFileSize:     4096,
		DownloadTimeout: 0,
		RateLimit:       10,
	})

	req := httptest.NewRequest(http.MethodGet, "/api/templates/"+templateRecord.ID+"/render", nil)
	req = mux.SetURLVars(req, map[string]string{"id": templateRecord.ID})
	rec := httptest.NewRecorder()

	handler.RenderTemplateHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"Custom HK |"`) {
		t.Fatalf("body should use custom prefix for sub-west: %s", body)
	}
	if !strings.Contains(body, `"My AI Nodes |"`) {
		t.Fatalf("body should use custom prefix for sub-ai: %s", body)
	}
	if strings.Contains(body, `"West |"`) {
		t.Fatalf("body should not contain default prefix for sub-west: %s", body)
	}
	if strings.Contains(body, `"AI Select |"`) {
		t.Fatalf("body should not contain default prefix for sub-ai: %s", body)
	}
}

func TestRenderTemplateHandlerFallsBackToDefaultPrefixWhenCustomIsEmpty(t *testing.T) {
	dataDir := t.TempDir()

	if err := SaveSubscriptions([]models.Subscription{
		{
			ID:        "sub-west",
			Name:      "West",
			URL:       "https://example.com/west",
			FilePath:  "west-source.yaml",
			Type:      "clash",
			UpdatedAt: time.Now(),
			Status:    "active",
		},
	}, filepath.Join(dataDir, "subscriptions.json")); err != nil {
		t.Fatalf("SaveSubscriptions() error = %v", err)
	}

	templateRecord, err := AddTemplate(models.Template{
		Name: "fallback-prefix",
		Content: strings.TrimSpace(`
proxy-providers: {}
rules:
  - MATCH,DIRECT
`) + "\n",
		SubscriptionPrefixes: map[string]string{
			"sub-west": "   ",
		},
		UpdatedAt: time.Now(),
		IsDefault: true,
	}, filepath.Join(dataDir, "templates.json"))
	if err != nil {
		t.Fatalf("AddTemplate() error = %v", err)
	}

	handler := NewHandler(&Config{
		DataDir:         dataDir,
		MaxFileSize:     4096,
		DownloadTimeout: 0,
		RateLimit:       10,
	})

	req := httptest.NewRequest(http.MethodGet, "/api/templates/"+templateRecord.ID+"/render", nil)
	req = mux.SetURLVars(req, map[string]string{"id": templateRecord.ID})
	rec := httptest.NewRecorder()

	handler.RenderTemplateHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"West |"`) {
		t.Fatalf("body should fall back to default prefix when custom is whitespace: %s", body)
	}
}

func TestListTemplatesHandlerReturnsSavedTemplates(t *testing.T) {
	dataDir := t.TempDir()
	if _, err := AddTemplate(models.Template{
		Name:      "demo",
		Content:   "port: 7890\n",
		UpdatedAt: time.Now(),
		IsDefault: true,
	}, filepath.Join(dataDir, "templates.json")); err != nil {
		t.Fatalf("AddTemplate() error = %v", err)
	}

	handler := NewHandler(&Config{
		DataDir:         dataDir,
		MaxFileSize:     1024,
		DownloadTimeout: 0,
		RateLimit:       10,
	})

	req := httptest.NewRequest(http.MethodGet, "/api/templates", nil)
	rec := httptest.NewRecorder()

	handler.ListTemplatesHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var resp Response
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	items, ok := resp.Data.([]any)
	if !ok {
		t.Fatalf("response data type = %T, want array", resp.Data)
	}
	if len(items) != 1 {
		t.Fatalf("len(items) = %d, want 1", len(items))
	}
}
