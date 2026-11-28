package apitypes

import (
	"time"

	"github.com/google/uuid"
)

// Device mirrors the Device schema: a paired phone or other device.
type Device struct {
	ID          uuid.UUID `json:"id"`
	Name        string    `json:"name"`
	CreatedAt   time.Time `json:"createdAt"`
	LastSeenAt  time.Time `json:"lastSeenAt"`
	LastAddress *string   `json:"lastAddress"`
	ExpiresAt   time.Time `json:"expiresAt"`
}

// DeviceList mirrors the DeviceList schema.
type DeviceList struct {
	Devices []Device `json:"devices"`
}

// DevicePairing mirrors the DevicePairing schema: a one-time code and its pairing link.
type DevicePairing struct {
	Code      string    `json:"code"`
	URL       string    `json:"url"`
	ExpiresAt time.Time `json:"expiresAt"`
}
