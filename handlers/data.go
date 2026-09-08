package handlers

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"clash-subscription-manager/models"
)

// subscriptionCache provides thread-safe caching for subscriptions
var subscriptionCache struct {
	subscriptions []models.Subscription
	mutex         sync.RWMutex
}

// LoadSubscriptions loads subscriptions from the specified file
// Returns an empty slice if the file doesn't exist or is empty
func LoadSubscriptions(dataFile string) ([]models.Subscription, error) {
	subscriptionCache.mutex.Lock()
	defer subscriptionCache.mutex.Unlock()

	// Check if file exists
	if _, err := os.Stat(dataFile); os.IsNotExist(err) {
		subscriptionCache.subscriptions = []models.Subscription{}
		return []models.Subscription{}, nil
	}

	// Read file
	data, err := os.ReadFile(dataFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read subscriptions file: %w", err)
	}

	// Handle empty file
	if len(data) == 0 {
		subscriptionCache.subscriptions = []models.Subscription{}
		return []models.Subscription{}, nil
	}

	// Parse JSON
	var subscriptions []models.Subscription
	if err := json.Unmarshal(data, &subscriptions); err != nil {
		return nil, fmt.Errorf("failed to parse subscriptions JSON: %w", err)
	}

	// Update cache
	subscriptionCache.subscriptions = subscriptions

	return subscriptions, nil
}

// SaveSubscriptions saves subscriptions to the specified file
// Uses pretty-printed JSON format for readability
func SaveSubscriptions(subscriptions []models.Subscription, dataFile string) error {
	subscriptionCache.mutex.Lock()
	defer subscriptionCache.mutex.Unlock()

	// Create directory if it doesn't exist
	dir := filepath.Dir(dataFile)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create data directory: %w", err)
	}

	// Marshal to JSON with indentation
	data, err := json.MarshalIndent(subscriptions, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal subscriptions: %w", err)
	}

	// Write to file atomically using temp file
	tmpFile := dataFile + ".tmp"
	if err := os.WriteFile(tmpFile, data, 0644); err != nil {
		return fmt.Errorf("failed to write subscriptions file: %w", err)
	}

	// Rename temp file to actual file (atomic operation)
	if err := os.Rename(tmpFile, dataFile); err != nil {
		os.Remove(tmpFile) // Clean up temp file
		return fmt.Errorf("failed to save subscriptions file: %w", err)
	}

	// Update cache
	subscriptionCache.subscriptions = subscriptions

	return nil
}

// AddSubscription adds a new subscription and saves it to file
// Automatically generates a unique ID for the subscription
func AddSubscription(subscription models.Subscription, dataFile string) (*models.Subscription, error) {
	// Load existing subscriptions
	subscriptions, err := LoadSubscriptions(dataFile)
	if err != nil {
		return nil, fmt.Errorf("failed to load subscriptions: %w", err)
	}

	// Generate unique ID and public link token
	subscription.ID = generateID()
	if subscription.AccessToken == "" {
		subscription.AccessToken = generateToken()
	}
	now := time.Now()
	if subscription.CreatedAt.IsZero() {
		subscription.CreatedAt = now
	}
	subscription.UpdatedAt = now

	// Add to list
	subscriptions = append(subscriptions, subscription)

	// Save to file
	if err := SaveSubscriptions(subscriptions, dataFile); err != nil {
		return nil, fmt.Errorf("failed to save subscriptions: %w", err)
	}

	return &subscription, nil
}

// DeleteSubscription removes a subscription by ID
// Returns an error if the subscription is not found
func DeleteSubscription(id string, dataFile string) error {
	// Load existing subscriptions
	subscriptions, err := LoadSubscriptions(dataFile)
	if err != nil {
		return fmt.Errorf("failed to load subscriptions: %w", err)
	}

	// Find and remove subscription
	found := false
	var deletedSub *models.Subscription
	newSubscriptions := make([]models.Subscription, 0, len(subscriptions)-1)
	for _, sub := range subscriptions {
		if sub.ID != id {
			newSubscriptions = append(newSubscriptions, sub)
		} else {
			found = true
			subCopy := sub
			deletedSub = &subCopy
		}
	}

	if !found {
		return fmt.Errorf("subscription with ID %s not found", id)
	}

	if deletedSub != nil && deletedSub.FilePath != "" {
		filePath := filepath.Join(filepath.Dir(dataFile), deletedSub.FilePath)
		if err := os.Remove(filePath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("failed to remove cached file: %w", err)
		}
	}

	// Save updated list
	if err := SaveSubscriptions(newSubscriptions, dataFile); err != nil {
		return fmt.Errorf("failed to save subscriptions: %w", err)
	}

	return nil
}

// GetSubscription retrieves a single subscription by ID
// Returns nil if the subscription is not found
func GetSubscription(id string, dataFile string) (*models.Subscription, error) {
	// Load subscriptions
	subscriptions, err := LoadSubscriptions(dataFile)
	if err != nil {
		return nil, fmt.Errorf("failed to load subscriptions: %w", err)
	}

	// Find subscription
	for _, sub := range subscriptions {
		if sub.ID == id {
			return &sub, nil
		}
	}

	return nil, fmt.Errorf("subscription with ID %s not found", id)
}

// ListSubscriptions returns all subscriptions
func ListSubscriptions(dataFile string) ([]models.Subscription, error) {
	return LoadSubscriptions(dataFile)
}

// UpdateSubscription updates an existing subscription in place and persists the result.
func UpdateSubscription(id string, dataFile string, updateFn func(*models.Subscription) error) (*models.Subscription, error) {
	subscriptions, err := LoadSubscriptions(dataFile)
	if err != nil {
		return nil, fmt.Errorf("failed to load subscriptions: %w", err)
	}

	for index := range subscriptions {
		if subscriptions[index].ID != id {
			continue
		}

		if err := updateFn(&subscriptions[index]); err != nil {
			return nil, err
		}
		if err := SaveSubscriptions(subscriptions, dataFile); err != nil {
			return nil, fmt.Errorf("failed to save subscriptions: %w", err)
		}
		updated := subscriptions[index]
		return &updated, nil
	}

	return nil, fmt.Errorf("subscription with ID %s not found", id)
}

// generateID generates a unique ID using crypto/rand
// Returns a 16-character hexadecimal string
func generateID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		// Fallback to timestamp-based ID if crypto/rand fails
		timestamp := time.Now().UnixNano()
		return fmt.Sprintf("%x", timestamp)
	}
	return hex.EncodeToString(b)
}

// generateToken generates a 128-bit random capability token (32 hex chars)
// used in public subscription/download links so they can be revoked by resetting.
func generateToken() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// Fallback to timestamp+ID based token if crypto/rand fails
		return generateID() + generateID()
	}
	return hex.EncodeToString(b)
}

// EnsureAccessTokens backfills access_token for records created before
// link tokens were introduced, so every public link is protected and resettable.
func EnsureAccessTokens(dataDir string) error {
	subscriptionsFile := filepath.Join(dataDir, "subscriptions.json")
	subscriptions, err := LoadSubscriptions(subscriptionsFile)
	if err != nil {
		return fmt.Errorf("load subscriptions for migration: %w", err)
	}
	subsChanged := false
	for i := range subscriptions {
		if subscriptions[i].AccessToken == "" {
			subscriptions[i].AccessToken = generateToken()
			subsChanged = true
		}
	}
	if subsChanged {
		if err := SaveSubscriptions(subscriptions, subscriptionsFile); err != nil {
			return fmt.Errorf("save subscriptions after migration: %w", err)
		}
	}

	templatesFile := filepath.Join(dataDir, "templates.json")
	templates, err := LoadTemplates(templatesFile)
	if err != nil {
		return fmt.Errorf("load templates for migration: %w", err)
	}
	templatesChanged := false
	for i := range templates {
		if templates[i].AccessToken == "" {
			templates[i].AccessToken = generateToken()
			templatesChanged = true
		}
	}
	if templatesChanged {
		if err := SaveTemplates(templates, templatesFile); err != nil {
			return fmt.Errorf("save templates after migration: %w", err)
		}
	}

	return nil
}
