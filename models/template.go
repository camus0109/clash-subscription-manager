package models

import "time"

// Template represents an editable Clash config template.
type Template struct {
	ID          string `json:"id"`
	AccessToken string `json:"access_token,omitempty"`
	Name        string `json:"name"`
	Content     string `json:"content"`
	// Mode controls how Content is rendered. Empty values are legacy generated templates.
	Mode                    string            `json:"mode,omitempty"`
	IsDefault               bool              `json:"is_default"`
	SelectedSubscriptionIDs []string          `json:"selected_subscription_ids,omitempty"`
	UseAllSubscriptions     *bool             `json:"use_all_subscriptions,omitempty"`
	SubscriptionPrefixes    map[string]string `json:"subscription_prefixes,omitempty"`
	CreatedAt               time.Time         `json:"created_at,omitempty"`
	UpdatedAt               time.Time         `json:"updated_at"`
}
