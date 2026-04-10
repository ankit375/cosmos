
package device

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/yourorg/cloudctrl/internal/model"
	pgstore "github.com/yourorg/cloudctrl/internal/store/postgres"
	"go.uber.org/zap"
)

// EventEmitter handles device lifecycle event emission.
type EventEmitter struct {
	eventStore *pgstore.EventStore
	logger     *zap.Logger
}

// NewEventEmitter creates a new event emitter.
func NewEventEmitter(eventStore *pgstore.EventStore, logger *zap.Logger) *EventEmitter {
	return &EventEmitter{
		eventStore: eventStore,
		logger:     logger.Named("device-events"),
	}
}

// DeviceAdopted emits an event when a device is adopted.
func (e *EventEmitter) DeviceAdopted(ctx context.Context, tenantID, deviceID, siteID uuid.UUID, name string, autoAdopt bool) {
	details := map[string]interface{}{
		"site_id":    siteID.String(),
		"name":       name,
		"auto_adopt": autoAdopt,
	}
	e.emit(ctx, tenantID, deviceID, "device.adopted", model.SeverityInfo,
		"Device adopted and assigned to site", details)
}

// DeviceOnline emits an event when a device comes online.
func (e *EventEmitter) DeviceOnline(ctx context.Context, tenantID, deviceID uuid.UUID, ip string, firmware string) {
	details := map[string]interface{}{
		"ip_address":       ip,
		"firmware_version": firmware,
	}
	e.emit(ctx, tenantID, deviceID, "device.online", model.SeverityInfo,
		"Device connected and online", details)
}

// DeviceOffline emits an event when a device goes offline (heartbeat timeout).
func (e *EventEmitter) DeviceOffline(ctx context.Context, tenantID, deviceID uuid.UUID, lastSeen time.Time) {
	details := map[string]interface{}{
		"last_seen":         lastSeen.Format(time.RFC3339),
		"offline_since":     time.Now().Format(time.RFC3339),
		"missed_heartbeats": 3,
	}
	e.emit(ctx, tenantID, deviceID, "device.offline", model.SeverityWarning,
		"Device offline (heartbeat timeout)", details)
}

// DeviceDisconnected emits an event when a device WebSocket connection closes.
func (e *EventEmitter) DeviceDisconnected(ctx context.Context, tenantID, deviceID uuid.UUID, reason string, duration time.Duration) {
	details := map[string]interface{}{
		"reason":              reason,
		"connection_duration": duration.String(),
	}
	e.emit(ctx, tenantID, deviceID, "device.disconnected", model.SeverityInfo,
		"Device WebSocket connection closed", details)
}

// DeviceDecommissioned emits an event when a device is decommissioned.
func (e *EventEmitter) DeviceDecommissioned(ctx context.Context, tenantID, deviceID uuid.UUID, userID uuid.UUID) {
	details := map[string]interface{}{
		"decommissioned_by": userID.String(),
	}
	e.emit(ctx, tenantID, deviceID, "device.decommissioned", model.SeverityWarning,
		"Device decommissioned", details)
}

// DeviceReconnected emits an event when a previously offline device reconnects.
func (e *EventEmitter) DeviceReconnected(ctx context.Context, tenantID, deviceID uuid.UUID, offlineDuration time.Duration) {
	details := map[string]interface{}{
		"offline_duration": offlineDuration.String(),
	}
	e.emit(ctx, tenantID, deviceID, "device.reconnected", model.SeverityInfo,
		"Device reconnected after being offline", details)
}

// NewDeviceDetected emits an event when a brand new device is seen.
func (e *EventEmitter) NewDeviceDetected(ctx context.Context, tenantID, deviceID uuid.UUID, mac, deviceModel, serial string) {
	details := map[string]interface{}{
		"mac":    mac,
		"model":  deviceModel,
		"serial": serial,
	}
	e.emit(ctx, tenantID, deviceID, "device.discovered", model.SeverityInfo,
		"New device detected, pending adoption", details)
}

// StateTransition emits an event for a device state change.
func (e *EventEmitter) StateTransition(ctx context.Context, tenantID, deviceID uuid.UUID, from, to model.DeviceStatus) {
	if from == to {
		return
	}
	details := map[string]interface{}{
		"from_status": string(from),
		"to_status":   string(to),
	}
	severity := model.SeverityInfo
	if to == model.DeviceStatusError {
		severity = model.SeverityError
	} else if to == model.DeviceStatusOffline {
		severity = model.SeverityWarning
	}
	e.emit(ctx, tenantID, deviceID, "device.state_change", severity,
		"Device status changed from "+string(from)+" to "+string(to), details)
}

// emit is the internal helper that persists the event.
func (e *EventEmitter) emit(ctx context.Context, tenantID, deviceID uuid.UUID,
	eventType string, severity model.EventSeverity, message string, details interface{}) {

	var detailsJSON json.RawMessage
	if details != nil {
		data, err := json.Marshal(details)
		if err != nil {
			e.logger.Error("failed to marshal event details",
				zap.String("event_type", eventType),
				zap.Error(err),
			)
			return
		}
		detailsJSON = data
	}

	event := &model.DeviceEvent{
		ID:        uuid.New(),
		TenantID:  tenantID,
		DeviceID:  deviceID,
		EventType: eventType,
		Severity:  severity,
		Message:   message,
		Details:   detailsJSON,
		Timestamp: time.Now(),
	}

	// Use a short timeout — events are best-effort, never block device processing
	eventCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	if err := e.eventStore.Create(eventCtx, event); err != nil {
		e.logger.Error("failed to emit device event",
			zap.String("device_id", deviceID.String()),
			zap.String("event_type", eventType),
			zap.Error(err),
		)
	}
}
