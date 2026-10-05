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

// subscriptionCache serializes subscription file transactions.
var subscriptionCache struct {
	mutex sync.Mutex
}

// LoadSubscriptions loads subscriptions from the specified file
// Returns an empty slice if the file doesn't exist or is empty
func LoadSubscriptions(dataFile string) ([]models.Subscription, error) {
	subscriptionCache.mutex.Lock()
	defer subscriptionCache.mutex.Unlock()
	return loadSubscriptionsLocked(dataFile)
}

func loadSubscriptionsLocked(dataFile string) ([]models.Subscription, error) {
	// Check if file exists
	if _, err := os.Stat(dataFile); os.IsNotExist(err) {
		return []models.Subscription{}, nil
	}

	// Read file
	data, err := readFile(dataFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read subscriptions file: %w", err)
	}

	// Handle empty file
	if len(data) == 0 {
		return []models.Subscription{}, nil
	}

	// Parse JSON
	var subscriptions []models.Subscription
	if err := json.Unmarshal(data, &subscriptions); err != nil {
		return nil, fmt.Errorf("failed to parse subscriptions JSON: %w", err)
	}

	return cloneSubscriptions(subscriptions), nil
}

// SaveSubscriptions saves subscriptions to the specified file
// Uses pretty-printed JSON format for readability
func SaveSubscriptions(subscriptions []models.Subscription, dataFile string) error {
	subscriptionCache.mutex.Lock()
	defer subscriptionCache.mutex.Unlock()
	return saveSubscriptionsLocked(subscriptions, dataFile)
}

func saveSubscriptionsLocked(subscriptions []models.Subscription, dataFile string) error {
	if err := os.MkdirAll(filepath.Dir(dataFile), 0755); err != nil {
		return fmt.Errorf("failed to create data directory: %w", err)
	}
	// Marshal to JSON with indentation
	data, err := json.MarshalIndent(subscriptions, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal subscriptions: %w", err)
	}

	if err := atomicWriteFile(dataFile, data, 0600); err != nil {
		return fmt.Errorf("failed to save subscriptions file: %w", err)
	}

	return nil
}

// AddSubscription adds a new subscription and saves it to file
// Automatically generates a unique ID for the subscription
func AddSubscription(subscription models.Subscription, dataFile string) (*models.Subscription, error) {
	subscriptionCache.mutex.Lock()
	defer subscriptionCache.mutex.Unlock()

	subscriptions, err := loadSubscriptionsLocked(dataFile)
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
	if err := saveSubscriptionsLocked(subscriptions, dataFile); err != nil {
		return nil, fmt.Errorf("failed to save subscriptions: %w", err)
	}

	return &subscription, nil
}

// DeleteSubscription removes a subscription by ID
// Returns an error if the subscription is not found
func DeleteSubscription(id string, dataFile string) error {
	subscriptionCache.mutex.Lock()
	defer subscriptionCache.mutex.Unlock()

	subscriptions, err := loadSubscriptionsLocked(dataFile)
	if err != nil {
		return fmt.Errorf("failed to load subscriptions: %w", err)
	}

	// Find and remove subscription
	found := false
	var deletedSub *models.Subscription
	newSubscriptions := make([]models.Subscription, 0, len(subscriptions))
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

	// Save updated list
	if err := saveSubscriptionsLocked(newSubscriptions, dataFile); err != nil {
		return fmt.Errorf("failed to save subscriptions: %w", err)
	}

	// The metadata deletion is durable before removing the derived cache file.
	// A cache cleanup failure is reported after the subscription is deleted.
	if deletedSub != nil && deletedSub.FilePath != "" {
		filePath := filepath.Join(filepath.Dir(dataFile), deletedSub.FilePath)
		if err := os.Remove(filePath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("subscription deleted but failed to remove cached file: %w", err)
		}
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
	subscriptionCache.mutex.Lock()
	defer subscriptionCache.mutex.Unlock()

	subscriptions, err := loadSubscriptionsLocked(dataFile)
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
		if err := saveSubscriptionsLocked(subscriptions, dataFile); err != nil {
			return nil, fmt.Errorf("failed to save subscriptions: %w", err)
		}
		updated := subscriptions[index]
		return &updated, nil
	}

	return nil, fmt.Errorf("subscription with ID %s not found", id)
}

func cloneSubscriptions(subscriptions []models.Subscription) []models.Subscription {
	if subscriptions == nil {
		return []models.Subscription{}
	}
	return append([]models.Subscription(nil), subscriptions...)
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
	subscriptionCache.mutex.Lock()
	subscriptions, err := loadSubscriptionsLocked(subscriptionsFile)
	if err != nil {
		subscriptionCache.mutex.Unlock()
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
		if err := saveSubscriptionsLocked(subscriptions, subscriptionsFile); err != nil {
			subscriptionCache.mutex.Unlock()
			return fmt.Errorf("save subscriptions after migration: %w", err)
		}
	}
	subscriptionCache.mutex.Unlock()

	templatesFile := filepath.Join(dataDir, "templates.json")
	templateCache.mutex.Lock()
	templates, err := loadTemplatesLocked(templatesFile)
	if err != nil {
		templateCache.mutex.Unlock()
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
		if err := saveTemplatesLocked(templates, templatesFile); err != nil {
			templateCache.mutex.Unlock()
			return fmt.Errorf("save templates after migration: %w", err)
		}
	}
	templateCache.mutex.Unlock()

	return nil
}
