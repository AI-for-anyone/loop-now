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
	ID        uuid.UUID `json:"id"`
	Code      string    `json:"code"`
	URL       string    `json:"url"`
	ExpiresAt time.Time `json:"expiresAt"`
}

// DevicePairingStatus mirrors the DevicePairingStatus schema: whether a code was used, and by which device.
type DevicePairingStatus struct {
	ID        uuid.UUID `json:"id"`
	State     string    `json:"state"`
	ExpiresAt time.Time `json:"expiresAt"`
	Device    *Device   `json:"device"`
}
