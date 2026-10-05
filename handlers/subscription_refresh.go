package handlers

import (
	"fmt"
	"net/http"
	"path/filepath"
	"time"

	"clash-subscription-manager/converter"
	"clash-subscription-manager/models"
)

type refreshFailure struct {
	statusCode int
	err        error
}

func (e *refreshFailure) Error() string {
	return e.err.Error()
}

func (h *Handler) refreshSubscription(id string, dataFile string, payload subscriptionPayload) (*models.Subscription, error) {
	current, err := GetSubscription(id, dataFile)
	if err != nil {
		return nil, &refreshFailure{
			statusCode: http.StatusNotFound,
			err:        fmt.Errorf("Subscription not found: %v", err),
		}
	}

	// Manual node subscriptions cannot be refreshed from a remote URL
	if current.URL == "nodes://manual" {
		return nil, &refreshFailure{
			statusCode: http.StatusBadRequest,
			err:        fmt.Errorf("手动添加的节点订阅不支持刷新，请删除后重新添加"),
		}
	}

	name := payload.Name
	if name == "" {
		name = current.Name
	}
	rawURL := payload.URL
	if rawURL == "" {
		rawURL = current.URL
	}
	filter := payload.Filter
	if filter == "" {
		filter = current.Filter
	}
	requestHeaders := payload.RequestHeaders
	if requestHeaders == nil {
		requestHeaders = current.RequestHeaders
	}

	content, err := h.downloadSubscriptionContent(rawURL, requestHeaders)
	if err != nil {
		return h.recordRefreshFailure(id, dataFile, err), &refreshFailure{
			statusCode: http.StatusBadGateway,
			err:        fmt.Errorf("Failed to refresh subscription: %v", err),
		}
	}

	convertedContent, summary, err := converter.ConvertToClash(content)
	if err != nil {
		return h.recordRefreshFailure(id, dataFile, err), &refreshFailure{
			statusCode: http.StatusBadRequest,
			err:        fmt.Errorf("Unsupported subscription content: %v", err),
		}
	}

	filePath := current.FilePath
	if filePath == "" {
		filePath, err = h.storeSubscriptionFile(name, convertedContent)
		if err != nil {
			return h.recordRefreshFailure(id, dataFile, err), &refreshFailure{
				statusCode: http.StatusInternalServerError,
				err:        fmt.Errorf("Failed to store subscription file: %v", err),
			}
		}
	} else {
		if err := atomicWriteFile(filepath.Join(h.config.DataDir, filePath), convertedContent, 0600); err != nil {
			return h.recordRefreshFailure(id, dataFile, err), &refreshFailure{
				statusCode: http.StatusInternalServerError,
				err:        fmt.Errorf("Failed to update cached file: %v", err),
			}
		}
	}

	now := time.Now()
	updatedSub, err := UpdateSubscription(id, dataFile, func(sub *models.Subscription) error {
		sub.Name = name
		sub.URL = rawURL
		sub.Filter = filter
		sub.Type = summary.DetectedType
		sub.RequestHeaders = requestHeaders
		sub.FilePath = filePath
		sub.FileSize = int64(len(convertedContent))
		sub.UpdatedAt = now
		sub.LastCheck = now
		sub.LastError = ""
		sub.LastErrorTime = time.Time{}
		sub.Status = "active"
		sub.NodeCount = summary.NodeCount
		return nil
	})
	if err != nil {
		return nil, &refreshFailure{
			statusCode: http.StatusInternalServerError,
			err:        fmt.Errorf("Failed to update subscription: %v", err),
		}
	}

	return updatedSub, nil
}

func (h *Handler) recordRefreshFailure(id string, dataFile string, refreshErr error) *models.Subscription {
	updatedSub, err := UpdateSubscription(id, dataFile, func(sub *models.Subscription) error {
		sub.LastError = refreshErr.Error()
		sub.LastErrorTime = time.Now()
		sub.Status = "active"
		return nil
	})
	if err != nil {
		return nil
	}

	return updatedSub
}
