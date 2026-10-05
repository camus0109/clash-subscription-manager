package handlers

import (
	"compress/gzip"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"clash-subscription-manager/converter"
	"clash-subscription-manager/models"

	"github.com/gorilla/mux"
)

// Response represents a standardized JSON response
type Response struct {
	Success bool        `json:"success"`
	Message string      `json:"message,omitempty"`
	Data    interface{} `json:"data,omitempty"`
	Error   string      `json:"error,omitempty"`
}

// Config represents handler configuration
type Config struct {
	WebAssets       fs.FS
	DataDir         string
	MaxFileSize     int64
	DownloadTimeout time.Duration
	RateLimit       int
	Token           string
}

// Handler holds handler dependencies
type Handler struct {
	config     *Config
	httpClient *http.Client
}

type subscriptionPayload struct {
	Name           string            `json:"name"`
	URL            string            `json:"url"`
	Filter         string            `json:"filter"`
	Type           string            `json:"type"`
	NodeLinksText  string            `json:"node_links_text"`
	RequestHeaders map[string]string `json:"request_headers"`
}

// NewHandler creates a new handler instance
func NewHandler(config *Config) *Handler {
	return &Handler{
		config: config,
		httpClient: &http.Client{
			Timeout: config.DownloadTimeout,
		},
	}
}

// Middleware

// rateLimit middleware implements basic rate limiting
func (h *Handler) RateLimit(next http.HandlerFunc) http.HandlerFunc {
	// Simple in-memory rate limiter
	type client struct {
		requests  int
		lastReset time.Time
	}
	clients := make(map[string]*client)
	var clientsMu sync.Mutex

	return func(w http.ResponseWriter, r *http.Request) {
		// Get client IP
		ip := r.RemoteAddr

		clientsMu.Lock()
		record, exists := clients[ip]
		if !exists {
			record = &client{requests: 0, lastReset: time.Now()}
			clients[ip] = record
		}

		// Reset counter if minute has passed
		if time.Since(record.lastReset) > time.Minute {
			record.requests = 0
			record.lastReset = time.Now()
		}

		// Check rate limit
		if record.requests >= h.config.RateLimit {
			clientsMu.Unlock()
			h.respondJSON(w, http.StatusTooManyRequests, Response{
				Success: false,
				Error:   "Rate limit exceeded",
			})
			return
		}

		record.requests++
		clientsMu.Unlock()
		next(w, r)
	}
}

// RequireAuth protects management handlers with the shared admin token
// sent as "Authorization: Bearer <token>".
func (h *Handler) RequireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !h.isValidAdminToken(r) {
			h.respondJSON(w, http.StatusUnauthorized, Response{
				Success: false,
				Error:   "未授权，请输入访问密钥",
			})
			return
		}
		next(w, r)
	}
}

func (h *Handler) isValidAdminToken(r *http.Request) bool {
	const scheme = "Bearer "
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, scheme) {
		return false
	}
	provided := strings.TrimPrefix(auth, scheme)
	if provided == "" || h.config.Token == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(provided), []byte(h.config.Token)) == 1
}

// validLinkToken reports whether the request carries the expected capability
// token for a public subscription/template link.
func validLinkToken(r *http.Request, secret string) bool {
	if secret == "" {
		return false
	}
	provided := r.URL.Query().Get("token")
	if provided == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(provided), []byte(secret)) == 1
}

// SubscriptionLinkAuth guards the public download endpoint. The subscription
// link must include "?token=" plus the subscription's current access token.
func (h *Handler) SubscriptionLinkAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		subscription, err := GetSubscription(mux.Vars(r)["id"], filepath.Join(h.config.DataDir, "subscriptions.json"))
		if err != nil {
			h.respondJSON(w, http.StatusNotFound, Response{Success: false, Error: "无效的下载链接"})
			return
		}
		if !validLinkToken(r, subscription.AccessToken) {
			h.respondJSON(w, http.StatusNotFound, Response{Success: false, Error: "链接已失效，请重新复制下载地址"})
			return
		}
		next(w, r)
	}
}

// TemplateLinkAuth guards public template render endpoints. Works for both
// "/api/templates/{id}/render..." and "/api/templates/default/render...".
func (h *Handler) TemplateLinkAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := mux.Vars(r)["id"]
		dataFile := filepath.Join(h.config.DataDir, "templates.json")
		var item *models.Template
		var err error
		if id == "" || id == "default" {
			item, err = GetDefaultTemplate(dataFile)
		} else {
			item, err = GetTemplate(id, dataFile)
		}
		if err != nil {
			h.respondJSON(w, http.StatusNotFound, Response{Success: false, Error: "无效的模板链接"})
			return
		}
		if !validLinkToken(r, item.AccessToken) {
			h.respondJSON(w, http.StatusNotFound, Response{Success: false, Error: "链接已失效，请重新复制模板地址"})
			return
		}
		next(w, r)
	}
}

// Basic Handlers

// homeHandler serves the main HTML page
func (h *Handler) HomeHandler(w http.ResponseWriter, r *http.Request) {
	// Parse template
	if h.config.WebAssets == nil {
		http.Error(w, "web assets are not configured", http.StatusInternalServerError)
		return
	}
	tmpl, err := template.ParseFS(h.config.WebAssets, "templates/index.html")
	if err != nil {
		h.respondJSON(w, http.StatusInternalServerError, Response{
			Success: false,
			Error:   fmt.Sprintf("Failed to parse template: %v", err),
		})
		return
	}

	// Execute template
	if err := tmpl.Execute(w, nil); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// healthHandler returns health status
func (h *Handler) HealthHandler(w http.ResponseWriter, r *http.Request) {
	h.respondJSON(w, http.StatusOK, Response{
		Success: true,
		Data: map[string]interface{}{
			"status": "healthy",
			"time":   time.Now().Unix(),
		},
	})
}

// listSubscriptionsHandler returns all subscriptions
func (h *Handler) ListSubscriptionsHandler(w http.ResponseWriter, r *http.Request) {
	dataFile := filepath.Join(h.config.DataDir, "subscriptions.json")

	subscriptions, err := ListSubscriptions(dataFile)
	if err != nil {
		h.respondJSON(w, http.StatusInternalServerError, Response{
			Success: false,
			Error:   fmt.Sprintf("Failed to load subscriptions: %v", err),
		})
		return
	}

	h.respondJSON(w, http.StatusOK, Response{
		Success: true,
		Data:    subscriptions,
	})
}

// Subscription Management Handlers

// subscribeHandler adds a new subscription
func (h *Handler) SubscribeHandler(w http.ResponseWriter, r *http.Request) {
	payload, err := decodeSubscriptionPayload(r)
	if err != nil {
		h.respondJSON(w, http.StatusBadRequest, Response{
			Success: false,
			Error:   fmt.Sprintf("Invalid request body: %v", err),
		})
		return
	}

	// Validate required fields
	if payload.Name == "" || payload.URL == "" {
		h.respondJSON(w, http.StatusBadRequest, Response{
			Success: false,
			Error:   "Name and URL are required",
		})
		return
	}

	sub := models.Subscription{
		Name:           payload.Name,
		URL:            payload.URL,
		Filter:         payload.Filter,
		Type:           payload.Type,
		RequestHeaders: payload.RequestHeaders,
	}

	// Set default values
	if sub.Type == "" {
		sub.Type = "unknown"
	}
	now := time.Now()
	sub.CreatedAt = now
	sub.UpdatedAt = now
	sub.LastCheck = now
	sub.Status = "active"

	content, err := h.downloadSubscriptionContent(sub.URL, sub.RequestHeaders)
	if err != nil {
		h.respondJSON(w, http.StatusBadGateway, Response{
			Success: false,
			Error:   fmt.Sprintf("Failed to download subscription: %v", err),
		})
		return
	}

	convertedContent, summary, err := converter.ConvertToClash(content)
	if err != nil {
		h.respondJSON(w, http.StatusBadRequest, Response{
			Success: false,
			Error:   fmt.Sprintf("Unsupported subscription content: %v", err),
		})
		return
	}

	filePath, err := h.storeSubscriptionFile(sub.Name, convertedContent)
	if err != nil {
		h.respondJSON(w, http.StatusInternalServerError, Response{
			Success: false,
			Error:   fmt.Sprintf("Failed to store subscription file: %v", err),
		})
		return
	}

	sub.FilePath = filePath
	sub.FileSize = int64(len(convertedContent))
	sub.Type = summary.DetectedType
	sub.NodeCount = summary.NodeCount

	// Add subscription
	dataFile := filepath.Join(h.config.DataDir, "subscriptions.json")
	addedSub, err := AddSubscription(sub, dataFile)
	if err != nil {
		_ = os.Remove(filepath.Join(h.config.DataDir, filePath))
		h.respondJSON(w, http.StatusInternalServerError, Response{
			Success: false,
			Error:   fmt.Sprintf("Failed to add subscription: %v", err),
		})
		return
	}

	h.respondJSON(w, http.StatusCreated, Response{
		Success: true,
		Message: "Subscription added successfully",
		Data:    addedSub,
	})
}

func (h *Handler) SubscribeNodesHandler(w http.ResponseWriter, r *http.Request) {
	payload, err := decodeSubscriptionPayload(r)
	if err != nil {
		h.respondJSON(w, http.StatusBadRequest, Response{
			Success: false,
			Error:   fmt.Sprintf("Invalid request body: %v", err),
		})
		return
	}

	if payload.Name == "" || strings.TrimSpace(payload.NodeLinksText) == "" {
		h.respondJSON(w, http.StatusBadRequest, Response{
			Success: false,
			Error:   "Name and node links are required",
		})
		return
	}

	convertedContent, summary, err := converter.ConvertNodesTextToClash(payload.NodeLinksText)
	if err != nil {
		h.respondJSON(w, http.StatusBadRequest, Response{
			Success: false,
			Error:   fmt.Sprintf("Unsupported node links: %v", err),
		})
		return
	}

	sub := models.Subscription{
		Name:           payload.Name,
		URL:            "nodes://manual",
		Filter:         payload.Filter,
		Type:           summary.DetectedType,
		RequestHeaders: map[string]string{},
	}

	now := time.Now()
	sub.CreatedAt = now
	sub.UpdatedAt = now
	sub.LastCheck = now
	sub.Status = "active"

	filePath, err := h.storeSubscriptionFile(sub.Name, convertedContent)
	if err != nil {
		h.respondJSON(w, http.StatusInternalServerError, Response{
			Success: false,
			Error:   fmt.Sprintf("Failed to store subscription file: %v", err),
		})
		return
	}

	sub.FilePath = filePath
	sub.FileSize = int64(len(convertedContent))
	sub.NodeCount = summary.NodeCount

	dataFile := filepath.Join(h.config.DataDir, "subscriptions.json")
	addedSub, err := AddSubscription(sub, dataFile)
	if err != nil {
		_ = os.Remove(filepath.Join(h.config.DataDir, filePath))
		h.respondJSON(w, http.StatusInternalServerError, Response{
			Success: false,
			Error:   fmt.Sprintf("Failed to add subscription: %v", err),
		})
		return
	}

	h.respondJSON(w, http.StatusCreated, Response{
		Success: true,
		Message: "Subscription added successfully",
		Data:    addedSub,
	})
}

// subscriptionHandler handles GET, PUT and DELETE for a single subscription
func (h *Handler) SubscriptionHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id := vars["id"]

	dataFile := filepath.Join(h.config.DataDir, "subscriptions.json")

	switch r.Method {
	case http.MethodGet:
		h.getSubscription(w, id, dataFile)
	case http.MethodPut:
		h.updateSubscriptionHandler(w, r, id, dataFile)
	case http.MethodDelete:
		h.deleteSubscription(w, id, dataFile)
	default:
		h.respondJSON(w, http.StatusMethodNotAllowed, Response{
			Success: false,
			Error:   "Method not allowed",
		})
	}
}

func (h *Handler) updateSubscriptionHandler(w http.ResponseWriter, r *http.Request, id string, dataFile string) {
	current, err := GetSubscription(id, dataFile)
	if err != nil {
		h.respondJSON(w, http.StatusNotFound, Response{
			Success: false,
			Error:   fmt.Sprintf("Subscription not found: %v", err),
		})
		return
	}

	payload, err := decodeSubscriptionPayload(r)
	if err != nil {
		h.respondJSON(w, http.StatusBadRequest, Response{
			Success: false,
			Error:   fmt.Sprintf("Invalid request body: %v", err),
		})
		return
	}

	name := payload.Name
	if name == "" {
		name = current.Name
	}
	rawURL := payload.URL
	if rawURL == "" {
		rawURL = current.URL
	}
	subType := payload.Type
	if subType == "" {
		subType = current.Type
	}
	requestHeaders := payload.RequestHeaders
	if requestHeaders == nil {
		requestHeaders = current.RequestHeaders
	}

	updatedSub, err := UpdateSubscription(id, dataFile, func(sub *models.Subscription) error {
		sub.Name = name
		sub.URL = rawURL
		sub.Filter = payload.Filter
		sub.Type = subType
		sub.RequestHeaders = requestHeaders
		sub.UpdatedAt = time.Now()
		return nil
	})
	if err != nil {
		h.respondJSON(w, http.StatusInternalServerError, Response{
			Success: false,
			Error:   fmt.Sprintf("Failed to update subscription: %v", err),
		})
		return
	}

	h.respondJSON(w, http.StatusOK, Response{
		Success: true,
		Message: "Subscription updated successfully",
		Data:    updatedSub,
	})
}

// RefreshSubscriptionHandler updates subscription fields and refreshes the cached file.
func (h *Handler) RefreshSubscriptionHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id := vars["id"]
	dataFile := filepath.Join(h.config.DataDir, "subscriptions.json")

	payload, err := decodeSubscriptionPayload(r)
	if err != nil {
		h.respondJSON(w, http.StatusBadRequest, Response{
			Success: false,
			Error:   fmt.Sprintf("Invalid request body: %v", err),
		})
		return
	}

	updatedSub, err := h.refreshSubscription(id, dataFile, payload)
	if err != nil {
		statusCode := http.StatusInternalServerError
		if refreshErr, ok := err.(*refreshFailure); ok {
			statusCode = refreshErr.statusCode
		}
		h.respondJSON(w, statusCode, Response{
			Success: false,
			Error:   err.Error(),
			Data:    updatedSub,
		})
		return
	}

	h.respondJSON(w, http.StatusOK, Response{
		Success: true,
		Message: "Subscription refreshed successfully",
		Data:    updatedSub,
	})
}

// ResetSubscriptionHandler rotates the public download-link token so any old
// link stops working immediately.
func (h *Handler) ResetSubscriptionHandler(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	dataFile := filepath.Join(h.config.DataDir, "subscriptions.json")

	updatedSub, err := UpdateSubscription(id, dataFile, func(sub *models.Subscription) error {
		sub.AccessToken = generateToken()
		sub.UpdatedAt = time.Now()
		return nil
	})
	if err != nil {
		h.respondJSON(w, http.StatusInternalServerError, Response{
			Success: false,
			Error:   fmt.Sprintf("Failed to reset subscription link: %v", err),
		})
		return
	}

	h.respondJSON(w, http.StatusOK, Response{
		Success: true,
		Message: "订阅链接已重置，旧链接已失效",
		Data:    updatedSub,
	})
}

// getSubscription retrieves a single subscription
func (h *Handler) getSubscription(w http.ResponseWriter, id string, dataFile string) {
	subscription, err := GetSubscription(id, dataFile)
	if err != nil {
		h.respondJSON(w, http.StatusNotFound, Response{
			Success: false,
			Error:   fmt.Sprintf("Subscription not found: %v", err),
		})
		return
	}

	h.respondJSON(w, http.StatusOK, Response{
		Success: true,
		Data:    subscription,
	})
}

// deleteSubscription removes a subscription
func (h *Handler) deleteSubscription(w http.ResponseWriter, id string, dataFile string) {
	if err := DeleteSubscription(id, dataFile); err != nil {
		h.respondJSON(w, http.StatusNotFound, Response{
			Success: false,
			Error:   fmt.Sprintf("Failed to delete subscription: %v", err),
		})
		return
	}

	h.respondJSON(w, http.StatusOK, Response{
		Success: true,
		Message: "Subscription deleted successfully",
	})
}

// downloadHandler downloads subscription content
func (h *Handler) DownloadHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id := vars["id"]

	// Get subscription
	dataFile := filepath.Join(h.config.DataDir, "subscriptions.json")
	subscription, err := GetSubscription(id, dataFile)
	if err != nil {
		h.respondJSON(w, http.StatusNotFound, Response{
			Success: false,
			Error:   fmt.Sprintf("Subscription not found: %v", err),
		})
		return
	}

	if subscription.FilePath == "" {
		h.respondJSON(w, http.StatusNotFound, Response{
			Success: false,
			Error:   "Subscription file is not available",
		})
		return
	}

	filePath := filepath.Join(h.config.DataDir, subscription.FilePath)
	content, err := readFile(filePath)
	if err != nil {
		h.respondJSON(w, http.StatusNotFound, Response{
			Success: false,
			Error:   fmt.Sprintf("Failed to read subscription file: %v", err),
		})
		return
	}

	contentType := http.DetectContentType(content)

	// Set headers for download
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s.yaml\"", sanitizeFilename(subscription.Name)))
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(content)))

	// Write content
	if _, err := w.Write(content); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// Helper Functions

// respondJSON sends a JSON response
func (h *Handler) respondJSON(w http.ResponseWriter, status int, data Response) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func (h *Handler) downloadSubscriptionContent(rawURL string, requestHeaders map[string]string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	for key, value := range requestHeaders {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" || value == "" {
			continue
		}
		req.Header.Set(key, value)
	}

	resp, err := h.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download failed with status: %d", resp.StatusCode)
	}

	if resp.ContentLength > h.config.MaxFileSize {
		return nil, fmt.Errorf("file too large: %d bytes (max: %d)", resp.ContentLength, h.config.MaxFileSize)
	}

	reader := io.Reader(resp.Body)
	if strings.EqualFold(resp.Header.Get("Content-Encoding"), "gzip") {
		gzipReader, err := gzip.NewReader(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("create gzip reader: %w", err)
		}
		defer gzipReader.Close()
		reader = gzipReader
	}

	content, err := io.ReadAll(io.LimitReader(reader, h.config.MaxFileSize+1))
	if err != nil {
		return nil, err
	}

	if len(content) == 0 {
		return nil, fmt.Errorf("downloaded content is empty")
	}
	if int64(len(content)) > h.config.MaxFileSize {
		return nil, fmt.Errorf("file too large: %d bytes (max: %d)", len(content), h.config.MaxFileSize)
	}

	return content, nil
}

func (h *Handler) storeSubscriptionFile(name string, content []byte) (string, error) {
	if err := os.MkdirAll(h.config.DataDir, 0755); err != nil {
		return "", err
	}

	fileName := fmt.Sprintf("%s-%s.yaml", sanitizeFilename(name), generateID())
	filePath := filepath.Join(h.config.DataDir, fileName)
	if err := atomicWriteFile(filePath, content, 0600); err != nil {
		return "", err
	}

	return fileName, nil
}

var filenameSanitizer = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

func sanitizeFilename(name string) string {
	sanitized := filenameSanitizer.ReplaceAllString(strings.TrimSpace(name), "-")
	sanitized = strings.Trim(sanitized, "-.")
	if sanitized == "" {
		return "subscription"
	}
	return sanitized
}

func decodeSubscriptionPayload(r *http.Request) (subscriptionPayload, error) {
	var payload subscriptionPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		return subscriptionPayload{}, err
	}

	payload.Name = strings.TrimSpace(payload.Name)
	payload.URL = strings.TrimSpace(payload.URL)
	payload.Filter = strings.TrimSpace(payload.Filter)
	payload.Type = strings.TrimSpace(payload.Type)
	for key, value := range payload.RequestHeaders {
		trimmedKey := strings.TrimSpace(key)
		trimmedValue := strings.TrimSpace(value)
		delete(payload.RequestHeaders, key)
		if trimmedKey == "" || trimmedValue == "" {
			continue
		}
		payload.RequestHeaders[trimmedKey] = trimmedValue
	}

	return payload, nil
}
