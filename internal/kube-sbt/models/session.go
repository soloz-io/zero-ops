package models

import "time"

// Session represents an authenticated session
type Session struct {
	ID        string                 `json:"id"`
	Identity  Identity               `json:"identity"`
	ExpiresAt time.Time              `json:"expiresAt"`
	IssuedAt  time.Time              `json:"issuedAt"`
}

// Identity represents user identity information
type Identity struct {
	ID     string                 `json:"id"`
	Traits map[string]interface{} `json:"traits"`
}
