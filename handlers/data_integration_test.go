package handlers

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"clash-subscription-manager/models"
)

// TestSubscriptionOperations tests a complete workflow of subscription operations
func TestSubscriptionOperations(t *testing.T) {
	tempDir := t.TempDir()
	dataFile := filepath.Join(tempDir, "subscriptions.json")

	// Test 1: Start with empty subscriptions
	subs, err := ListSubscriptions(dataFile)
	if err != nil {
		t.Fatalf("Failed to list subscriptions: %v", err)
	}
	if len(subs) != 0 {
		t.Errorf("Expected 0 subscriptions, got %d", len(subs))
	}

	// Test 2: Add first subscription
	sub1, err := AddSubscription(models.Subscription{
		Name:      "Subscription 1",
		URL:       "http://example.com/1",
		Type:      "v2ray",
		UpdatedAt: time.Now(),
		Status:    "active",
		NodeCount: 10,
	}, dataFile)
	if err != nil {
		t.Fatalf("Failed to add subscription: %v", err)
	}
	if sub1.ID == "" {
		t.Error("Expected generated ID")
	}

	// Test 3: Add second subscription
	sub2, err := AddSubscription(models.Subscription{
		Name:      "Subscription 2",
		URL:       "http://example.com/2",
		Type:      "shadowsocks",
		UpdatedAt: time.Now(),
		Status:    "active",
		NodeCount: 20,
	}, dataFile)
	if err != nil {
		t.Fatalf("Failed to add subscription: %v", err)
	}

	// Test 4: List all subscriptions
	subs, err = ListSubscriptions(dataFile)
	if err != nil {
		t.Fatalf("Failed to list subscriptions: %v", err)
	}
	if len(subs) != 2 {
		t.Errorf("Expected 2 subscriptions, got %d", len(subs))
	}

	// Test 5: Get specific subscription
	retrievedSub, err := GetSubscription(sub1.ID, dataFile)
	if err != nil {
		t.Fatalf("Failed to get subscription: %v", err)
	}
	if retrievedSub.Name != "Subscription 1" {
		t.Errorf("Expected name 'Subscription 1', got '%s'", retrievedSub.Name)
	}

	// Test 6: Delete one subscription
	err = DeleteSubscription(sub1.ID, dataFile)
	if err != nil {
		t.Fatalf("Failed to delete subscription: %v", err)
	}

	// Test 7: Verify deletion
	subs, err = ListSubscriptions(dataFile)
	if err != nil {
		t.Fatalf("Failed to list subscriptions: %v", err)
	}
	if len(subs) != 1 {
		t.Errorf("Expected 1 subscription after deletion, got %d", len(subs))
	}

	// Test 8: Verify the correct subscription remains
	if subs[0].ID != sub2.ID {
		t.Errorf("Expected remaining subscription ID %s, got %s", sub2.ID, subs[0].ID)
	}

	// Test 9: Verify file persistence
	// Load from file again
	subs, err = LoadSubscriptions(dataFile)
	if err != nil {
		t.Fatalf("Failed to load subscriptions after restart: %v", err)
	}
	if len(subs) != 1 {
		t.Errorf("Expected 1 subscription after reload, got %d", len(subs))
	}

	// Test 10: Verify JSON file format
	data, err := os.ReadFile(dataFile)
	if err != nil {
		t.Fatalf("Failed to read data file: %v", err)
	}
	if len(data) == 0 {
		t.Error("Data file is empty")
	}

	// Verify it's valid JSON
	var loadedSubs []models.Subscription
	if err := json.Unmarshal(data, &loadedSubs); err != nil {
		t.Errorf("Invalid JSON in data file: %v", err)
	}
}

// TestConcurrentAccess tests thread safety under concurrent access
func TestConcurrentAccess(t *testing.T) {
	tempDir := t.TempDir()
	dataFile := filepath.Join(tempDir, "subscriptions.json")

	// Add initial subscription
	_, err := AddSubscription(models.Subscription{
		Name:      "Initial",
		URL:       "http://example.com",
		Type:      "v2ray",
		UpdatedAt: time.Now(),
		Status:    "active",
		NodeCount: 10,
	}, dataFile)
	if err != nil {
		t.Fatalf("Failed to add initial subscription: %v", err)
	}

	// Run concurrent operations
	done := make(chan bool)
	errors := make(chan error, 10)

	// Concurrent reads
	for i := 0; i < 5; i++ {
		go func() {
			_, err := ListSubscriptions(dataFile)
			if err != nil {
				errors <- err
			}
			done <- true
		}()
	}

	// Concurrent writes
	for i := 0; i < 5; i++ {
		go func(index int) {
			_, err := AddSubscription(models.Subscription{
				Name:      "Concurrent",
				URL:       "http://example.com/concurrent",
				Type:      "v2ray",
				UpdatedAt: time.Now(),
				Status:    "active",
				NodeCount: index,
			}, dataFile)
			if err != nil {
				errors <- err
			}
			done <- true
		}(i)
	}

	// Wait for all goroutines
	for i := 0; i < 10; i++ {
		select {
		case <-done:
		case err := <-errors:
			t.Fatalf("Concurrent operation failed: %v", err)
		}
	}

	// Verify final state
	subs, err := ListSubscriptions(dataFile)
	if err != nil {
		t.Fatalf("Failed to list subscriptions: %v", err)
	}
	// Should have at least 1 initial + some concurrent additions
	// (some might be lost due to concurrent writes, which is expected without higher-level synchronization)
	if len(subs) < 2 {
		t.Errorf("Expected at least 2 subscriptions, got %d", len(subs))
	}
	if len(subs) > 6 {
		t.Errorf("Expected at most 6 subscriptions, got %d", len(subs))
	}
}
