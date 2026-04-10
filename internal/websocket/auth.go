package websocket

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/yourorg/cloudctrl/internal/model"
	"github.com/yourorg/cloudctrl/internal/protocol"
	"github.com/yourorg/cloudctrl/pkg/crypto"
	"go.uber.org/zap"
)

// AuthResult is the result of authenticating a device.
type AuthResult struct {
	Authenticated bool
	Device        *model.Device
	IsNewDevice   bool
	Token         string // New token if issued during adoption
}

// authenticateDevice handles the first message from a device (DeviceAuth).
// This is called from the upgrade handler before readPump/writePump start.
func (h *Hub) authenticateDevice(conn *DeviceConnection, authMsg *protocol.Message) (*AuthResult, error) {
	var payload protocol.DeviceAuthPayload
	if err := json.Unmarshal(authMsg.Payload, &payload); err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(h.ctx, 5*time.Second)
	defer cancel()

	result := &AuthResult{}

	// ── Case 1: Device has a token → look up by token hash ──
	if payload.Token != "" {
		tokenHash := crypto.HashToken(payload.Token)
		device, err := h.pgStore.Devices.GetByTokenHash(ctx, tokenHash)
		if err != nil {
			return nil, err
		}
		if device == nil {
			// Token invalid
			return &AuthResult{Authenticated: false}, nil
		}

		// Check device isn't decommissioned
		if device.Status == model.DeviceStatusDecommissioned {
			return &AuthResult{Authenticated: false}, nil
		}

		// Update device info from auth payload
		h.updateDeviceInfo(ctx, device, &payload)

		result.Authenticated = true
		result.Device = device
		return result, nil
	}

	// ── Case 2: No token → look up by MAC address ──────────
	device, err := h.pgStore.Devices.GetByMAC(ctx, payload.MAC)
	if err != nil {
		return nil, err
	}

	if device != nil {
		// Known device but no valid token
		if device.Status == model.DeviceStatusDecommissioned {
			return &AuthResult{Authenticated: false}, nil
		}

		// If device is already adopted, it needs a valid token
		if device.Status != model.DeviceStatusPendingAdopt {
			h.logger.Warn("known device connected without valid token",
				zap.String("mac", payload.MAC),
				zap.String("device_id", device.ID.String()),
				zap.String("status", string(device.Status)),
			)
			return &AuthResult{Authenticated: false, Device: device}, nil
		}

		// Device is pending_adopt — update its info
		h.updateDeviceInfo(ctx, device, &payload)

		// ── Auto-adopt check ────────────────────────────────
		autoAdoptResult, err := h.tryAutoAdopt(ctx, device)
		if err != nil {
			h.logger.Error("auto-adopt failed",
				zap.String("device_id", device.ID.String()),
				zap.Error(err),
			)
			// Fall through to pending — auto-adopt failure is non-fatal
		} else if autoAdoptResult != nil {
			return autoAdoptResult, nil
		}

		result.Device = device
		result.IsNewDevice = false
		return result, nil
	}

	// ── Case 3: Completely new device ───────────────────────
	h.logger.Info("new device detected",
		zap.String("mac", payload.MAC),
		zap.String("serial", payload.Serial),
		zap.String("model", payload.Model),
	)

	newDevice := &model.Device{
		ID:              uuid.New(),
		TenantID:        h.resolveDefaultTenant(ctx),
		MAC:             payload.MAC,
		Serial:          payload.Serial,
		Name:            payload.Model + "-" + payload.MAC[len(payload.MAC)-5:],
		Model:           payload.Model,
		Status:          model.DeviceStatusPendingAdopt,
		FirmwareVersion: payload.FirmwareVersion,
		Capabilities:    payload.Capabilities,
		SystemInfo:      payload.SystemInfo,
	}

	// Default empty JSON if nil
	if newDevice.Capabilities == nil {
		newDevice.Capabilities = json.RawMessage(`{}`)
	}
	if newDevice.SystemInfo == nil {
		newDevice.SystemInfo = json.RawMessage(`{}`)
	}

	if err := h.pgStore.Devices.Create(ctx, newDevice); err != nil {
		return nil, err
	}

	// Emit discovery event
	go h.eventEmitter.NewDeviceDetected(h.ctx, newDevice.TenantID, newDevice.ID,
		newDevice.MAC, newDevice.Model, newDevice.Serial)

	// ── Auto-adopt check for new device ─────────────────────
	autoAdoptResult, err := h.tryAutoAdopt(ctx, newDevice)
	if err != nil {
		h.logger.Error("auto-adopt for new device failed",
			zap.String("device_id", newDevice.ID.String()),
			zap.Error(err),
		)
		// Fall through to pending
	} else if autoAdoptResult != nil {
		autoAdoptResult.IsNewDevice = true
		return autoAdoptResult, nil
	}

	result.Device = newDevice
	result.IsNewDevice = true
	return result, nil
}

// tryAutoAdopt attempts to auto-adopt a pending device if any site in the tenant
// has auto_adopt enabled. Returns an AuthResult if auto-adoption succeeded,
// nil if no auto-adopt site found or limits reached.
func (h *Hub) tryAutoAdopt(ctx context.Context, device *model.Device) (*AuthResult, error) {
	// List all sites for the device's tenant
	sites, err := h.pgStore.Sites.List(ctx, device.TenantID)
	if err != nil {
		return nil, err
	}

	// Find the first site with auto_adopt enabled
	var autoAdoptSite *model.Site
	for _, site := range sites {
		if site.AutoAdopt {
			autoAdoptSite = site
			break
		}
	}

	if autoAdoptSite == nil {
		return nil, nil // No auto-adopt site found
	}

	// Check tenant device limit
	count, err := h.pgStore.Devices.CountByTenant(ctx, device.TenantID)
	if err != nil {
		return nil, err
	}

	tenant, err := h.pgStore.Tenants.GetByID(ctx, device.TenantID)
	if err != nil || tenant == nil {
		return nil, err
	}

	if count >= tenant.MaxDevices {
		h.logger.Warn("auto-adopt skipped: tenant device limit reached",
			zap.String("device_id", device.ID.String()),
			zap.Int("current", count),
			zap.Int("max", tenant.MaxDevices),
		)
		return nil, nil // Limit reached, not an error — just skip
	}

	// Generate device token
	token, err := crypto.GenerateToken(32) // 256-bit token
	if err != nil {
		return nil, err
	}
	tokenHash := crypto.HashToken(token)

	name := device.Name
	if name == "" {
		name = device.Model + "-" + device.MAC[len(device.MAC)-5:]
	}

	// Adopt the device in DB
	if err := h.pgStore.Devices.SetAdopted(ctx, device.ID, autoAdoptSite.ID, tokenHash, name); err != nil {
		return nil, err
	}

	h.logger.Info("device auto-adopted",
		zap.String("device_id", device.ID.String()),
		zap.String("site_id", autoAdoptSite.ID.String()),
		zap.String("site_name", autoAdoptSite.Name),
	)

	// Emit adoption event
	go h.eventEmitter.DeviceAdopted(h.ctx, device.TenantID, device.ID,
		autoAdoptSite.ID, name, true)

	// Metrics
	wsDeviceAdoptionsTotal.WithLabelValues("auto").Inc()
	wsAutoAdoptionsTotal.Inc()
	wsDeviceStateTransitions.WithLabelValues(
		string(model.DeviceStatusPendingAdopt),
		string(model.DeviceStatusProvisioning),
	).Inc()

	// Update device struct for the auth result
	device.SiteID = &autoAdoptSite.ID
	device.Status = model.DeviceStatusProvisioning
	device.DeviceTokenHash = &tokenHash

	return &AuthResult{
		Authenticated: true,
		Device:        device,
		IsNewDevice:   false,
		Token:         token,
	}, nil
}

// resolveDefaultTenant returns the first active tenant for auto-created devices.
// In a multi-tenant system, new devices need to be associated with a tenant.
// This uses a simple strategy: assign to the first active tenant.
// In production, you might use the site's auto_adopt + a mapping table.
func (h *Hub) resolveDefaultTenant(ctx context.Context) uuid.UUID {
	tenants, err := h.pgStore.Tenants.List(ctx)
	if err != nil || len(tenants) == 0 {
		h.logger.Error("no tenants found for device auto-creation")
		return uuid.Nil
	}
	return tenants[0].ID
}

// updateDeviceInfo updates a device's reported info from the auth payload.
func (h *Hub) updateDeviceInfo(ctx context.Context, device *model.Device, payload *protocol.DeviceAuthPayload) {
	_, err := h.pgStore.Pool.Exec(ctx,
		`UPDATE devices SET
			firmware_version = \$1,
			capabilities = \$2,
			system_info = \$3,
			last_seen = NOW(),
			updated_at = NOW()
		WHERE id = \$4`,
		payload.FirmwareVersion,
		payload.Capabilities,
		payload.SystemInfo,
		device.ID,
	)
	if err != nil {
		h.logger.Error("failed to update device info",
			zap.String("device_id", device.ID.String()),
			zap.Error(err),
		)
	}
}

// buildAuthResult creates the AuthResult protocol payload to send back to the device.
func (h *Hub) buildAuthResult(result *AuthResult) *protocol.AuthResultPayload {
	if result.Device == nil {
		return &protocol.AuthResultPayload{
			Success:    false,
			ServerTime: time.Now().Unix(),
			Error:      "unknown device",
		}
	}

	// Not authenticated and not pending → reject
	if !result.Authenticated && result.Device.Status != model.DeviceStatusPendingAdopt {
		return &protocol.AuthResultPayload{
			Success:    false,
			ServerTime: time.Now().Unix(),
			Error:      "invalid device token",
			Status:     string(result.Device.Status),
		}
	}

	// Pending adoption → tell device to wait
	if result.Device.Status == model.DeviceStatusPendingAdopt {
		return &protocol.AuthResultPayload{
			Success:    false,
			DeviceID:   result.Device.ID.String(),
			ServerTime: time.Now().Unix(),
			Status:     "pending_adoption",
			Error:      "device pending adoption",
		}
	}

	// Authenticated → success
	resp := &protocol.AuthResultPayload{
		Success:           true,
		DeviceID:          result.Device.ID.String(),
		ConfigRequired:    result.Device.DesiredConfigVersion > result.Device.AppliedConfigVersion,
		ServerTime:        time.Now().Unix(),
		HeartbeatInterval: 30,
		MetricsInterval:   60,
	}

	// If a new token was issued (during adoption flow)
	if result.Token != "" {
		resp.DeviceToken = result.Token
	}

	return resp
}

// HandleAdoption is called when an admin adopts a pending device via the API.
// It generates a device token, assigns to site, and updates the device.
func (h *Hub) HandleAdoption(ctx context.Context, tenantID, deviceID, siteID uuid.UUID, name string) (string, error) {
	// Generate device token
	token, err := crypto.GenerateToken(32) // 256-bit token
	if err != nil {
		return "", err
	}
	tokenHash := crypto.HashToken(token)

	// Update device in DB
	if err := h.pgStore.Devices.SetAdopted(ctx, deviceID, siteID, tokenHash, name); err != nil {
		return "", err
	}

	// Update or create state store entry
	updated := h.stateStore.Update(deviceID, func(state *DeviceState) {
		state.SiteID = &siteID
		state.Status = model.DeviceStatusProvisioning
		state.Dirty = true
	})
	if !updated {
		// Device isn't in state store yet (wasn't connected) — create entry
		h.stateStore.Set(&DeviceState{
			DeviceID: deviceID,
			TenantID: tenantID,
			SiteID:   &siteID,
			Status:   model.DeviceStatusProvisioning,
			Dirty:    true,
		})
	}

	// Emit adoption event
	go h.eventEmitter.DeviceAdopted(h.ctx, tenantID, deviceID, siteID, name, false)

	// Metrics
	wsDeviceAdoptionsTotal.WithLabelValues("manual").Inc()
	wsDeviceStateTransitions.WithLabelValues(
		string(model.DeviceStatusPendingAdopt),
		string(model.DeviceStatusProvisioning),
	).Inc()

	h.logger.Info("device adopted",
		zap.String("device_id", deviceID.String()),
		zap.String("site_id", siteID.String()),
		zap.String("name", name),
	)

	return token, nil
}
