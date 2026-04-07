package db

import "time"

type ConnectionProfile struct {
	Name       string     `json:"name"`
	ProviderID ProviderID `json:"provider_id"`
	DSN        string     `json:"dsn"`
	ReadOnly   bool       `json:"read_only"`
	Notes      string     `json:"notes,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
}

func (p ConnectionProfile) Validate() error {
	switch {
	case p.Name == "":
		return ErrInvalidProfile("profile name is required")
	case p.ProviderID == "":
		return ErrInvalidProfile("provider is required")
	case p.DSN == "":
		return ErrInvalidProfile("dsn is required")
	default:
		return nil
	}
}
