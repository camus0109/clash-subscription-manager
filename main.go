package main

import (
	"context"
	"crypto/rand"
	"embed"
	"encoding/hex"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/signal"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"clash-subscription-manager/handlers"

	"github.com/gorilla/mux"
	"github.com/sirupsen/logrus"
	"gopkg.in/yaml.v3"
)

// Version is the application version, set via ldflags during build
var Version = "v1.0.20"

//go:embed templates/index.html static
var webAssets embed.FS

type Config struct {
	ListenAddress     string        `yaml:"listen_address"`
	Port              int           `yaml:"port"`
	DataDir           string        `yaml:"data_dir"`
	MaxFileSize       int64         `yaml:"max_file_size"`
	DownloadTimeout   time.Duration `yaml:"download_timeout"`
	RateLimit         int           `yaml:"rate_limit"`
	Token             string        `yaml:"token"`
	HTTPS             bool          `yaml:"https"`
	BackupEnabled     bool          `yaml:"backup_enabled"`
	BackupInterval    time.Duration `yaml:"backup_interval"`
	FileRetentionDays int           `yaml:"file_retention_days"`
}

var logger = logrus.New()

func main() {
	cfg, err := loadConfig("config.yaml")
	if err != nil {
		logger.Fatalf("failed to load config: %v", err)
	}

	cfg, err = resolveAdminToken(strings.TrimSpace(os.Getenv("TOKEN")), cfg, "config.yaml")
	if err != nil {
		logger.Fatalf("failed to resolve admin token: %v", err)
	}

	// Backfill access tokens for subscriptions/templates created before link
	// tokens existed, so every public link is protected and resettable.
	if err := handlers.EnsureAccessTokens(cfg.DataDir); err != nil {
		logger.Warnf("failed to migrate access tokens: %v", err)
	}

	handler := handlers.NewHandler(newHandlerConfig(cfg))
	refresher := handlers.NewAutoRefresher(handler)
	server := newServer(cfg, newRouter(handler))
	shutdownSignals := signalContext()

	logStartup(cfg)
	refresher.Start()
	defer refresher.Stop()

	go func() {
		<-shutdownSignals.Done()
		refresher.Stop()

		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil && err != http.ErrServerClosed {
			logger.Errorf("failed to shut down server: %v", err)
		}
	}()

	if cfg.HTTPS {
		logger.Info("HTTPS enabled")
		if err := server.ListenAndServeTLS("cert.pem", "key.pem"); err != nil && err != http.ErrServerClosed {
			logger.Fatalf("failed to start HTTPS server: %v", err)
		}
		return
	}

	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		logger.Fatalf("failed to start server: %v", err)
	}
}

// resolveAdminToken decides the management token used for login:
//  1. TOKEN env var (docker -e TOKEN=...) wins if set;
//  2. otherwise a non-default token from config.yaml is kept;
//  3. otherwise a random token is generated, written back to the config file
//     so the operator can read it from that private file.
func resolveAdminToken(envToken string, cfg Config, configPath string) (Config, error) {
	if envToken != "" {
		cfg.Token = envToken
		logger.Info("使用环境变量 TOKEN 作为访问密钥")
		return cfg, nil
	}

	if cfg.Token != "" && cfg.Token != "your-secret-token" {
		return cfg, nil
	}

	token, err := generateAdminToken()
	if err != nil {
		return cfg, fmt.Errorf("generate admin token: %w", err)
	}
	cfg.Token = token

	if err := persistTokenToConfig(configPath, token); err != nil {
		return cfg, fmt.Errorf("persist admin token: %w", err)
	}
	logger.Infof("未配置访问密钥，已随机生成并写入 %s", configPath)

	return cfg, nil
}

// generateAdminToken returns a random 128-bit hex token.
func generateAdminToken() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// persistTokenToConfig writes the token into the YAML config file, replacing an
// existing token line, appending one when the key is missing, or creating the
// file when it does not exist yet.
func persistTokenToConfig(path string, token string) error {
	tokenLine := "token: " + strconv.Quote(token)

	data, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return err
		}
		return handlers.WritePrivateFile(path, []byte(tokenLine+"\n"))
	}

	lines := strings.Split(string(data), "\n")
	tokenPattern := regexp.MustCompile(`^(\s*)token:`)
	replaced := false
	for i, line := range lines {
		match := tokenPattern.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		lines[i] = match[1] + tokenLine
		replaced = true
		break
	}
	if !replaced {
		lines = append(lines, tokenLine)
	}
	return handlers.WritePrivateFile(path, []byte(strings.Join(lines, "\n")))
}

func logStartup(cfg Config) {
	logger.Infof("starting clash-subscription-manager %s on port %d", Version, cfg.Port)
}

func defaultConfig() Config {
	return Config{
		Port:              8080,
		DataDir:           "./data",
		MaxFileSize:       50 * 1024 * 1024,
		DownloadTimeout:   30 * time.Second,
		RateLimit:         60,
		Token:             "your-secret-token",
		HTTPS:             false,
		BackupEnabled:     true,
		BackupInterval:    24 * time.Hour,
		FileRetentionDays: 30,
	}
}

func loadConfig(path string) (Config, error) {
	cfg := defaultConfig()

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return Config{}, fmt.Errorf("read config: %w", err)
	}

	if len(data) == 0 {
		return cfg, nil
	}

	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse config: %w", err)
	}

	return cfg, nil
}

func newServer(cfg Config, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:         net.JoinHostPort(cfg.ListenAddress, strconv.Itoa(cfg.Port)),
		Handler:      handler,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}
}

func newRouter(h *handlers.Handler) http.Handler {
	router := mux.NewRouter()

	// Public page shell (no data without the admin token).
	router.HandleFunc("/", h.HomeHandler).Methods(http.MethodGet)

	// Public capability links: anonymous access only with "?token=" matching
	// the item's current access token. Registered before the /api management
	// subrouter so mux matches these routes first.
	router.HandleFunc("/download/{id}", h.RateLimit(h.SubscriptionLinkAuth(h.DownloadHandler))).Methods(http.MethodGet)
	router.HandleFunc("/api/templates/default/render", h.TemplateLinkAuth(h.RenderDefaultTemplateHandler)).Methods(http.MethodGet)
	router.HandleFunc("/api/templates/default/render-proxies", h.TemplateLinkAuth(h.RenderDefaultTemplateProxiesHandler)).Methods(http.MethodGet)
	router.HandleFunc("/api/templates/default/render-nodes", h.TemplateLinkAuth(h.RenderDefaultTemplateNodeLinksHandler)).Methods(http.MethodGet)
	router.HandleFunc("/api/templates/{id}/render", h.TemplateLinkAuth(h.RenderTemplateHandler)).Methods(http.MethodGet)
	router.HandleFunc("/api/templates/{id}/render-proxies", h.TemplateLinkAuth(h.RenderTemplateProxiesHandler)).Methods(http.MethodGet)
	router.HandleFunc("/api/templates/{id}/render-nodes", h.TemplateLinkAuth(h.RenderTemplateNodeLinksHandler)).Methods(http.MethodGet)

	// Management API: every route requires Authorization: Bearer <token>.
	api := router.PathPrefix("/api").Subrouter()
	api.HandleFunc("/subscribe", h.RequireAuth(h.SubscribeHandler)).Methods(http.MethodPost)
	api.HandleFunc("/subscribe/nodes", h.RequireAuth(h.SubscribeNodesHandler)).Methods(http.MethodPost)
	api.HandleFunc("/subscriptions", h.RequireAuth(h.ListSubscriptionsHandler)).Methods(http.MethodGet)
	api.HandleFunc("/subscribe/{id}", h.RequireAuth(h.SubscriptionHandler)).Methods(http.MethodGet, http.MethodPut, http.MethodDelete)
	api.HandleFunc("/subscribe/{id}/refresh", h.RequireAuth(h.RefreshSubscriptionHandler)).Methods(http.MethodPost)
	api.HandleFunc("/subscribe/{id}/reset", h.RequireAuth(h.ResetSubscriptionHandler)).Methods(http.MethodPost)
	api.HandleFunc("/templates", h.RequireAuth(h.ListTemplatesHandler)).Methods(http.MethodGet)
	api.HandleFunc("/templates", h.RequireAuth(h.TemplatesHandler)).Methods(http.MethodPost)
	api.HandleFunc("/templates/{id}", h.RequireAuth(h.TemplateHandler)).Methods(http.MethodGet, http.MethodPut, http.MethodDelete)
	api.HandleFunc("/templates/{id}/reset", h.RequireAuth(h.ResetTemplateHandler)).Methods(http.MethodPost)

	router.HandleFunc("/health", h.HealthHandler).Methods(http.MethodGet)

	staticAssets, err := fs.Sub(webAssets, "static")
	if err != nil {
		panic(err) // The embedded directory is fixed at compile time.
	}
	router.PathPrefix("/static/").Handler(http.StripPrefix("/static/", http.FileServer(http.FS(staticAssets))))

	return router
}

func newHandlerConfig(cfg Config) *handlers.Config {
	return &handlers.Config{
		DataDir:         cfg.DataDir,
		MaxFileSize:     cfg.MaxFileSize,
		DownloadTimeout: cfg.DownloadTimeout,
		RateLimit:       cfg.RateLimit,
		Token:           cfg.Token,
		WebAssets:       webAssets,
	}
}

func signalContext() context.Context {
	ctx, _ := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	return ctx
}
