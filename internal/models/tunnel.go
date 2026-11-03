package models

import (
	"time"
)

type TunnelStatus string

const (
	TunnelStatusPending   TunnelStatus = "Pending"
	TunnelStatusActive    TunnelStatus = "Active"
	TunnelStatusOffline   TunnelStatus = "Offline"
	TunnelStatusMigrating TunnelStatus = "Migrating" // Should be rarely used.
	TunnelStatusDisabled  TunnelStatus = "Disabled"
)

type Tunnel struct {
	ID        string
	AccountID string

	// Relay assignment
	VPSID string
	VPS   *VPS

	// WireGuard
	PublicKey  string
	PrivateIP  string
	AllowedIPs string

	// Lifecycle
	Status TunnelStatus

	CreatedAt time.Time
	UpdatedAt time.Time
}
