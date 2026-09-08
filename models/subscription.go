package models

import "time"

// Subscription represents a proxy subscription
type Subscription struct {
	ID             string            `json:"id"`
	AccessToken    string            `json:"access_token,omitempty"`
	Name           string            `json:"name"`
	URL            string            `json:"url"`
	Filter         string            `json:"filter,omitempty"`
	Type           string            `json:"type"`
	RequestHeaders map[string]string `json:"request_headers,omitempty"`
	FilePath       string            `json:"file_path,omitempty"`
	FileSize       int64             `json:"file_size,omitempty"`
	CreatedAt      time.Time         `json:"created_at,omitempty"`
	UpdatedAt      time.Time         `json:"updated_at"`
	LastCheck      time.Time         `json:"last_check"`
	LastError      string            `json:"last_error,omitempty"`
	LastErrorTime  time.Time         `json:"last_error_time,omitempty"`
	Status         string            `json:"status"`
	NodeCount      int               `json:"node_count"`
	Traffic        Traffic           `json:"traffic"`
}

// Traffic represents traffic statistics
type Traffic struct {
	Total    int64 `json:"total"`
	Upload   int64 `json:"upload"`
	Download int64 `json:"download"`
}
