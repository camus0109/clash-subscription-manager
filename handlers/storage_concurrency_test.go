package handlers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"clash-subscription-manager/models"
	"github.com/gorilla/mux"
)

func TestConcurrentAddSubscriptionsKeepsEveryUpdate(t *testing.T) {
	dataFile := filepath.Join(t.TempDir(), "subscriptions.json")
	const count = 32

	var wg sync.WaitGroup
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			if _, err := AddSubscription(models.Subscription{
				Name: "subscription-" + string(rune('a'+index)),
				URL:  "https://example.invalid/" + string(rune('a'+index)),
			}, dataFile); err != nil {
				t.Errorf("AddSubscription() error = %v", err)
			}
		}(i)
	}
	wg.Wait()

	subscriptions, err := LoadSubscriptions(dataFile)
	if err != nil {
		t.Fatalf("LoadSubscriptions() error = %v", err)
	}
	if len(subscriptions) != count {
		t.Fatalf("len(subscriptions) = %d, want %d", len(subscriptions), count)
	}
}

func TestAtomicSubscriptionSaveNeverExposesPartialJSON(t *testing.T) {
	dataFile := filepath.Join(t.TempDir(), "subscriptions.json")
	initial := []models.Subscription{{ID: "initial", Name: "initial"}}
	if err := SaveSubscriptions(initial, dataFile); err != nil {
		t.Fatalf("SaveSubscriptions(initial) error = %v", err)
	}

	const maxReads = 2000
	deadline := time.Now().Add(2 * time.Second)
	stop := make(chan struct{})
	readerErrors := make(chan error, 1)
	var reader sync.WaitGroup
	reader.Add(1)
	go func() {
		defer reader.Done()
		for reads := 0; reads < maxReads && time.Now().Before(deadline); reads++ {
			select {
			case <-stop:
				return
			default:
			}
			data, err := readFile(dataFile)
			if err != nil {
				select {
				case readerErrors <- err:
				default:
				}
				return
			}
			var decoded []models.Subscription
			if err := json.Unmarshal(data, &decoded); err != nil {
				select {
				case readerErrors <- err:
				default:
				}
				return
			}
		}
	}()

	for i := 0; i < 100; i++ {
		if err := SaveSubscriptions([]models.Subscription{{ID: "version", Name: string(rune('a' + i%26))}}, dataFile); err != nil {
			close(stop)
			reader.Wait()
			t.Fatalf("SaveSubscriptions() error = %v", err)
		}
	}
	close(stop)
	reader.Wait()
	select {
	case err := <-readerErrors:
		t.Fatalf("reader observed invalid or missing file: %v", err)
	default:
	}
}

func TestConcurrentAddTemplatesKeepsSingleDefault(t *testing.T) {
	dataFile := filepath.Join(t.TempDir(), "templates.json")
	const count = 32
	var wg sync.WaitGroup
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			_, err := AddTemplate(models.Template{
				Name:    fmt.Sprintf("template-%d", index),
				Content: "port: 7890\n",
			}, dataFile)
			if err != nil {
				t.Errorf("AddTemplate() error = %v", err)
			}
		}(i)
	}
	wg.Wait()

	templates, err := LoadTemplates(dataFile)
	if err != nil {
		t.Fatalf("LoadTemplates() error = %v", err)
	}
	if len(templates) != count {
		t.Fatalf("len(templates) = %d, want %d", len(templates), count)
	}
	defaults := 0
	for _, template := range templates {
		if template.IsDefault {
			defaults++
		}
	}
	if defaults != 1 {
		t.Fatalf("default template count = %d, want 1", defaults)
	}
}

func TestAtomicYAMLReplacementExposesOnlyCompleteValues(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "config.yaml")
	oldContent := []byte("port: 7890\nproxies:\n  - old\n")
	newContent := []byte("port: 7891\nproxies:\n  - new\n")
	if err := atomicWriteFile(filename, oldContent, 0600); err != nil {
		t.Fatalf("initial atomicWriteFile() error = %v", err)
	}

	const maxReads = 2000
	deadline := time.Now().Add(2 * time.Second)
	readErrors := make(chan error, 1)
	stop := make(chan struct{})
	var reader sync.WaitGroup
	reader.Add(1)
	go func() {
		defer reader.Done()
		for reads := 0; reads < maxReads && time.Now().Before(deadline); reads++ {
			select {
			case <-stop:
				return
			default:
			}
			content, err := readFile(filename)
			if err != nil {
				select {
				case readErrors <- err:
				default:
				}
				return
			}
			if string(content) != string(oldContent) && string(content) != string(newContent) {
				select {
				case readErrors <- fmt.Errorf("partial YAML content: %q", content):
				default:
				}
				return
			}
		}
	}()

	for i := 0; i < 100; i++ {
		content := oldContent
		if i%2 == 1 {
			content = newContent
		}
		if err := atomicWriteFile(filename, content, 0600); err != nil {
			close(stop)
			reader.Wait()
			t.Fatalf("atomicWriteFile() error = %v", err)
		}
	}
	close(stop)
	reader.Wait()
	select {
	case err := <-readErrors:
		t.Fatal(err)
	default:
	}
}

func TestDownloadDuringAtomicRefreshReturnsCompleteConfig(t *testing.T) {
	dataDir := t.TempDir()
	dataFile := filepath.Join(dataDir, "subscriptions.json")
	cacheName := "subscription.yaml"
	cachePath := filepath.Join(dataDir, cacheName)
	oldContent := []byte("port: 7890\nproxies:\n  - old\n")
	newContent := []byte("port: 7891\nproxies:\n  - new\n")
	if err := SaveSubscriptions([]models.Subscription{{ID: "sub-1", Name: "demo", FilePath: cacheName}}, dataFile); err != nil {
		t.Fatalf("SaveSubscriptions() error = %v", err)
	}
	if err := atomicWriteFile(cachePath, oldContent, 0600); err != nil {
		t.Fatalf("initial atomicWriteFile() error = %v", err)
	}

	handler := NewHandler(&Config{DataDir: dataDir})
	const requests = 100
	responses := make(chan error, requests)
	var writer sync.WaitGroup
	writer.Add(1)
	go func() {
		defer writer.Done()
		for i := 0; i < requests; i++ {
			content := oldContent
			if i%2 == 1 {
				content = newContent
			}
			if err := atomicWriteFile(cachePath, content, 0600); err != nil {
				responses <- fmt.Errorf("refresh write %d: %w", i, err)
				return
			}
		}
	}()

	for i := 0; i < requests; i++ {
		req := httptest.NewRequest(http.MethodGet, "/download/sub-1", nil)
		req = mux.SetURLVars(req, map[string]string{"id": "sub-1"})
		rec := httptest.NewRecorder()
		handler.DownloadHandler(rec, req)
		if rec.Code != http.StatusOK {
			responses <- fmt.Errorf("download %d status = %d, body = %s", i, rec.Code, rec.Body.String())
			continue
		}
		body := rec.Body.Bytes()
		if !bytes.Equal(body, oldContent) && !bytes.Equal(body, newContent) {
			responses <- fmt.Errorf("download %d returned partial config: %q", i, body)
		}
	}
	writer.Wait()
	close(responses)
	for err := range responses {
		t.Fatal(err)
	}
}
