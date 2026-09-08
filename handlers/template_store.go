package handlers

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"clash-subscription-manager/models"
)

var templateCache struct {
	templates []models.Template
	mutex     sync.RWMutex
}

func LoadTemplates(dataFile string) ([]models.Template, error) {
	templateCache.mutex.Lock()
	defer templateCache.mutex.Unlock()

	if _, err := os.Stat(dataFile); os.IsNotExist(err) {
		templateCache.templates = []models.Template{}
		return []models.Template{}, nil
	}

	data, err := os.ReadFile(dataFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read templates file: %w", err)
	}
	if len(data) == 0 {
		templateCache.templates = []models.Template{}
		return []models.Template{}, nil
	}

	var templates []models.Template
	if err := json.Unmarshal(data, &templates); err != nil {
		return nil, fmt.Errorf("failed to parse templates JSON: %w", err)
	}

	templateCache.templates = templates
	return templates, nil
}

func SaveTemplates(templates []models.Template, dataFile string) error {
	templateCache.mutex.Lock()
	defer templateCache.mutex.Unlock()

	dir := filepath.Dir(dataFile)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create data directory: %w", err)
	}

	data, err := json.MarshalIndent(templates, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal templates: %w", err)
	}

	tmpFile := dataFile + ".tmp"
	if err := os.WriteFile(tmpFile, data, 0644); err != nil {
		return fmt.Errorf("failed to write templates file: %w", err)
	}
	if err := os.Rename(tmpFile, dataFile); err != nil {
		_ = os.Remove(tmpFile)
		return fmt.Errorf("failed to save templates file: %w", err)
	}

	templateCache.templates = templates
	return nil
}

func AddTemplate(template models.Template, dataFile string) (*models.Template, error) {
	templates, err := LoadTemplates(dataFile)
	if err != nil {
		return nil, fmt.Errorf("failed to load templates: %w", err)
	}

	now := time.Now()
	template.ID = generateID()
	if template.AccessToken == "" {
		template.AccessToken = generateToken()
	}
	if template.CreatedAt.IsZero() {
		template.CreatedAt = now
	}
	template.UpdatedAt = now
	if len(templates) == 0 {
		template.IsDefault = true
	}
	if template.IsDefault {
		clearDefaultTemplateFlag(templates)
	}

	templates = append(templates, template)
	if err := SaveTemplates(templates, dataFile); err != nil {
		return nil, fmt.Errorf("failed to save templates: %w", err)
	}
	return &template, nil
}

func ListTemplates(dataFile string) ([]models.Template, error) {
	return LoadTemplates(dataFile)
}

func GetTemplate(id string, dataFile string) (*models.Template, error) {
	templates, err := LoadTemplates(dataFile)
	if err != nil {
		return nil, fmt.Errorf("failed to load templates: %w", err)
	}
	for _, item := range templates {
		if item.ID == id {
			return &item, nil
		}
	}
	return nil, fmt.Errorf("template with ID %s not found", id)
}

func GetDefaultTemplate(dataFile string) (*models.Template, error) {
	templates, err := LoadTemplates(dataFile)
	if err != nil {
		return nil, fmt.Errorf("failed to load templates: %w", err)
	}
	for _, item := range templates {
		if item.IsDefault {
			return &item, nil
		}
	}
	return nil, fmt.Errorf("default template not found")
}

func UpdateTemplate(id string, dataFile string, updateFn func(*models.Template) error) (*models.Template, error) {
	templates, err := LoadTemplates(dataFile)
	if err != nil {
		return nil, fmt.Errorf("failed to load templates: %w", err)
	}

	for index := range templates {
		if templates[index].ID != id {
			continue
		}
		if err := updateFn(&templates[index]); err != nil {
			return nil, err
		}
		templates[index].UpdatedAt = time.Now()
		if templates[index].IsDefault {
			clearDefaultTemplateFlag(templates[:index])
			clearDefaultTemplateFlag(templates[index+1:])
		}
		if err := SaveTemplates(templates, dataFile); err != nil {
			return nil, fmt.Errorf("failed to save templates: %w", err)
		}
		updated := templates[index]
		return &updated, nil
	}

	return nil, fmt.Errorf("template with ID %s not found", id)
}

func SetDefaultTemplate(id string, dataFile string) (*models.Template, error) {
	templates, err := LoadTemplates(dataFile)
	if err != nil {
		return nil, fmt.Errorf("failed to load templates: %w", err)
	}

	clearDefaultTemplateFlag(templates)
	for index := range templates {
		if templates[index].ID != id {
			continue
		}
		templates[index].IsDefault = true
		templates[index].UpdatedAt = time.Now()
		if err := SaveTemplates(templates, dataFile); err != nil {
			return nil, fmt.Errorf("failed to save templates: %w", err)
		}
		updated := templates[index]
		return &updated, nil
	}

	return nil, fmt.Errorf("template with ID %s not found", id)
}

func DeleteTemplate(id string, dataFile string) error {
	templates, err := LoadTemplates(dataFile)
	if err != nil {
		return fmt.Errorf("failed to load templates: %w", err)
	}

	found := false
	deletedWasDefault := false
	nextTemplates := make([]models.Template, 0, len(templates))
	for _, item := range templates {
		if item.ID == id {
			found = true
			deletedWasDefault = item.IsDefault
			continue
		}
		nextTemplates = append(nextTemplates, item)
	}
	if !found {
		return fmt.Errorf("template with ID %s not found", id)
	}
	if deletedWasDefault && len(nextTemplates) > 0 {
		nextTemplates[0].IsDefault = true
		nextTemplates[0].UpdatedAt = time.Now()
	}
	if err := SaveTemplates(nextTemplates, dataFile); err != nil {
		return fmt.Errorf("failed to save templates: %w", err)
	}
	return nil
}

func clearDefaultTemplateFlag(templates []models.Template) {
	for index := range templates {
		templates[index].IsDefault = false
	}
}
