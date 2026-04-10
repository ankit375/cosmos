package configmgr

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/yourorg/cloudctrl/internal/model"
	"github.com/yourorg/cloudctrl/internal/protocol"
	pgstore "github.com/yourorg/cloudctrl/internal/store/postgres"
	"go.uber.org/zap"
)

// ============================================================
// SAFE-APPLY TRACKER
// ============================================================

// SafeApplyState tracks an in-flight safe-apply operation.
type SafeApplyState struct {
	DeviceID       uuid.UUID
	TenantID       uuid.UUID
	Version        int64
	PushedAt       time.Time
	AckReceived    bool
	AckSuccess     bool
	AckError       string
	ConfirmTimeout time.Duration
	Done           chan struct{} // closed when resolved
}

// SafeApplyTracker manages all in-flight safe-apply operations.
type SafeApplyTracker struct {
	mu       sync.Mutex
	inflight map[uuid.UUID]*SafeApplyState // deviceID → state
}

func NewSafeApplyTracker() *SafeApplyTracker {
	return &SafeApplyTracker{
		inflight: make(map[uuid.UUID]*SafeApplyState),
	}
}

// Track starts tracking a safe-apply operation.
func (t *SafeApplyTracker) Track(deviceID, tenantID uuid.UUID, version int64, confirmTimeout time.Duration) *SafeApplyState {
	t.mu.Lock()
	defer t.mu.Unlock()

	state := &SafeApplyState{
		DeviceID:       deviceID,
		TenantID:       tenantID,
		Version:        version,
		PushedAt:       time.Now(),
		ConfirmTimeout: confirmTimeout,
		Done:           make(chan struct{}),
	}
	t.inflight[deviceID] = state
	return state
}

// ResolveAck marks a safe-apply as having received an ACK.
func (t *SafeApplyTracker) ResolveAck(deviceID uuid.UUID, success bool, errMsg string) *SafeApplyState {
	t.mu.Lock()
	defer t.mu.Unlock()

	state, ok := t.inflight[deviceID]
	if !ok {
		return nil
	}

	state.AckReceived = true
	state.AckSuccess = success
	state.AckError = errMsg

	if !success {
		// Failed — resolve immediately
		delete(t.inflight, deviceID)
		closeSafe(state.Done)
	}

	return state
}

// Complete finishes and removes a tracked operation.
func (t *SafeApplyTracker) Complete(deviceID uuid.UUID) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if state, ok := t.inflight[deviceID]; ok {
		delete(t.inflight, deviceID)
		closeSafe(state.Done)
	}
}

// Get returns the current safe-apply state for a device, or nil.
func (t *SafeApplyTracker) Get(deviceID uuid.UUID) *SafeApplyState {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.inflight[deviceID]
}

// GetTimedOut returns all safe-apply operations that have exceeded their timeout.
func (t *SafeApplyTracker) GetTimedOut() []*SafeApplyState {
	t.mu.Lock()
	defer t.mu.Unlock()

	now := time.Now()
	var timedOut []*SafeApplyState

	for deviceID, state := range t.inflight {
		if now.Sub(state.PushedAt) > state.ConfirmTimeout {
			timedOut = append(timedOut, state)
			delete(t.inflight, deviceID)
			closeSafe(state.Done)
		}
	}

	return timedOut
}

// Count returns the number of in-flight operations.
func (t *SafeApplyTracker) Count() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.inflight)
}

func closeSafe(ch chan struct{}) {
	select {
	case <-ch:
		// already closed
	default:
		close(ch)
	}
}

// ============================================================
// HUB INTERFACE (to avoid circular imports)
// ============================================================

// DeviceSender is the interface the manager uses to send messages to devices.
type DeviceSender interface {
	SendMessage(deviceID uuid.UUID, channel uint8, msgType uint16, flags uint8, payload interface{}) (uint32, error)
	IsConnected(deviceID uuid.UUID) bool
}

// ============================================================
// CONFIG MANAGER
// ============================================================

// Manager orchestrates all configuration operations.
type Manager struct {
	pgStore    *pgstore.Store
	sender     DeviceSender
	tracker    *SafeApplyTracker
	logger     *zap.Logger

	// Config
	safeApplyTimeout  time.Duration
	stabilityDelay    time.Duration // wait after ACK before sending Confirm
	reconcileInterval time.Duration

	// Lifecycle
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// ManagerConfig holds configuration for the config manager.
type ManagerConfig struct {
	SafeApplyTimeout  time.Duration
	StabilityDelay    time.Duration
	ReconcileInterval time.Duration
}

// DefaultManagerConfig returns sensible defaults.
func DefaultManagerConfig() ManagerConfig {
	return ManagerConfig{
		SafeApplyTimeout:  60 * time.Second,
		StabilityDelay:    5 * time.Second,
		ReconcileInterval: 60 * time.Second,
	}
}

// NewManager creates a new config manager.
func NewManager(
	pgStore *pgstore.Store,
	sender DeviceSender,
	cfg ManagerConfig,
	logger *zap.Logger,
) *Manager {
	ctx, cancel := context.WithCancel(context.Background())

	return &Manager{
		pgStore:           pgStore,
		sender:            sender,
		tracker:           NewSafeApplyTracker(),
		logger:            logger.Named("configmgr"),
		safeApplyTimeout:  cfg.SafeApplyTimeout,
		stabilityDelay:    cfg.StabilityDelay,
		reconcileInterval: cfg.ReconcileInterval,
		ctx:               ctx,
		cancel:            cancel,
	}
}

// Tracker returns the safe-apply tracker (for handler access).
func (m *Manager) Tracker() *SafeApplyTracker {
	return m.tracker
}

// ============================================================
// SITE CONFIG TEMPLATE OPERATIONS
// ============================================================

// ============================================================
// CHANGES TO: UpdateSiteConfig (add validation metric)
// ============================================================

// UpdateSiteConfig creates a new config template version and pushes to all site devices.
func (m *Manager) UpdateSiteConfig(
	ctx context.Context,
	tenantID, siteID uuid.UUID,
	configData json.RawMessage,
	description string,
	createdBy *uuid.UUID,
) (*model.ConfigTemplate, *ConfigValidationResult, error) {
	validationResult := ValidateConfig(configData, nil)
	if validationResult.HasErrors() {
		configValidationTotal.WithLabelValues("invalid").Inc()
		return nil, validationResult, nil
	}
	configValidationTotal.WithLabelValues("valid").Inc()

	template, err := m.pgStore.Configs.CreateTemplate(ctx, tenantID, siteID, configData, description, createdBy)
	if err != nil {
		return nil, nil, fmt.Errorf("create config template: %w", err)
	}

	m.logger.Info("config template created",
		zap.String("site_id", siteID.String()),
		zap.Int64("version", template.Version),
	)

	go m.pushConfigToSite(tenantID, siteID, template)

	return template, validationResult, nil
}

// RollbackSiteConfig rolls back a site config to a previous version.
func (m *Manager) RollbackSiteConfig(
	ctx context.Context,
	tenantID, siteID uuid.UUID,
	targetVersion int64,
	createdBy *uuid.UUID,
) (*model.ConfigTemplate, error) {
	// Load the target version
	target, err := m.pgStore.Configs.GetTemplateByVersion(ctx, tenantID, siteID, targetVersion)
	if err != nil {
		return nil, fmt.Errorf("get template version %d: %w", targetVersion, err)
	}
	if target == nil {
		return nil, fmt.Errorf("config template version %d not found", targetVersion)
	}

	// Create as new version with same config content
	description := fmt.Sprintf("Rollback to version %d", targetVersion)
	template, err := m.pgStore.Configs.CreateTemplate(ctx, tenantID, siteID, target.Config, description, createdBy)
	if err != nil {
		return nil, fmt.Errorf("create rollback template: %w", err)
	}

	m.logger.Info("site config rolled back",
		zap.String("site_id", siteID.String()),
		zap.Int64("from_version", targetVersion),
		zap.Int64("new_version", template.Version),
	)

	// Push to all devices in the site
	go m.pushConfigToSite(tenantID, siteID, template)

	return template, nil
}

// ============================================================
// DEVICE CONFIG OPERATIONS
// ============================================================

// GenerateAndPushDeviceConfig generates a merged config for a device and pushes it.
func (m *Manager) GenerateAndPushDeviceConfig(
	ctx context.Context,
	tenantID, deviceID uuid.UUID,
	createdBy *uuid.UUID,
) (*model.DeviceConfig, *ConfigValidationResult, error) {
	// Load device
	device, err := m.pgStore.Devices.GetByID(ctx, tenantID, deviceID)
	if err != nil {
		return nil, nil, fmt.Errorf("get device: %w", err)
	}
	if device == nil {
		return nil, nil, fmt.Errorf("device not found")
	}
	if device.SiteID == nil {
		return nil, nil, fmt.Errorf("device has no site assignment")
	}

	// Load site
	site, err := m.pgStore.Sites.GetByID(ctx, tenantID, *device.SiteID)
	if err != nil || site == nil {
		return nil, nil, fmt.Errorf("get site: %w", err)
	}

	// Load latest template
	template, err := m.pgStore.Configs.GetLatestTemplate(ctx, tenantID, *device.SiteID)
	if err != nil {
		return nil, nil, fmt.Errorf("get template: %w", err)
	}
	if template == nil {
		return nil, nil, fmt.Errorf("no config template found for site %s", site.Name)
	}

	// Load device overrides
	var overrides json.RawMessage
	override, err := m.pgStore.Configs.GetDeviceOverrides(ctx, tenantID, deviceID)
	if err != nil {
		return nil, nil, fmt.Errorf("get device overrides: %w", err)
	}
	if override != nil {
		overrides = override.Overrides
	}

	// Generate merged config
	mergedConfig, err := GenerateDeviceConfig(template.Config, overrides, device, site)
	if err != nil {
		return nil, nil, fmt.Errorf("generate device config: %w", err)
	}

	// Validate merged config against device capabilities
	caps := ParseCapabilities(device.Capabilities)
	validationResult := ValidateConfig(mergedConfig, caps)
	if validationResult.HasErrors() {
		return nil, validationResult, nil
	}

	// Store device config
	dc := &model.DeviceConfig{
		TenantID:        tenantID,
		DeviceID:        deviceID,
		Config:          mergedConfig,
		Source:          model.ConfigSourceTemplate,
		TemplateVersion: &template.Version,
		DeviceOverrides: overrides,
		Status:          model.ConfigStatusPending,
		CreatedBy:       createdBy,
	}

	if err := m.pgStore.Configs.CreateDeviceConfig(ctx, dc); err != nil {
		return nil, nil, fmt.Errorf("create device config: %w", err)
	}

	m.logger.Info("device config generated",
		zap.String("device_id", deviceID.String()),
		zap.Int64("version", dc.Version),
		zap.Int64("template_version", template.Version),
	)

	// Push to device if online
	go m.pushConfigToDevice(tenantID, deviceID, dc)

	return dc, validationResult, nil
}

// ForcePushDeviceConfig re-pushes the latest config to a device.
func (m *Manager) ForcePushDeviceConfig(ctx context.Context, tenantID, deviceID uuid.UUID) error {
	// Load the latest device config
	dc, err := m.pgStore.Configs.GetLatestDeviceConfig(ctx, tenantID, deviceID)
	if err != nil {
		return fmt.Errorf("get latest device config: %w", err)
	}
	if dc == nil {
		return fmt.Errorf("no config found for device")
	}

	// Re-push
	go m.pushConfigToDevice(tenantID, deviceID, dc)

	return nil
}

// RollbackDeviceConfig rolls back a device config to a previous version.
func (m *Manager) RollbackDeviceConfig(
	ctx context.Context,
	tenantID, deviceID uuid.UUID,
	targetVersion int64,
	createdBy *uuid.UUID,
) (*model.DeviceConfig, error) {
	// Load the target version
	target, err := m.pgStore.Configs.GetDeviceConfigByVersion(ctx, tenantID, deviceID, targetVersion)
	if err != nil {
		return nil, fmt.Errorf("get device config version %d: %w", targetVersion, err)
	}
	if target == nil {
		return nil, fmt.Errorf("device config version %d not found", targetVersion)
	}

	// Create as new version with rollback source
	dc := &model.DeviceConfig{
		TenantID:        tenantID,
		DeviceID:        deviceID,
		Config:          target.Config,
		Source:          model.ConfigSourceRollback,
		TemplateVersion: target.TemplateVersion,
		DeviceOverrides: target.DeviceOverrides,
		Status:          model.ConfigStatusPending,
		CreatedBy:       createdBy,
	}

	if err := m.pgStore.Configs.CreateDeviceConfig(ctx, dc); err != nil {
		return nil, fmt.Errorf("create rollback device config: %w", err)
	}

	m.logger.Info("device config rolled back",
		zap.String("device_id", deviceID.String()),
		zap.Int64("target_version", targetVersion),
		zap.Int64("new_version", dc.Version),
	)

	go m.pushConfigToDevice(tenantID, deviceID, dc)

	return dc, nil
}

// UpdateDeviceOverrides updates overrides and re-generates + pushes config.
func (m *Manager) UpdateDeviceOverrides(
	ctx context.Context,
	tenantID, deviceID uuid.UUID,
	overrides json.RawMessage,
	updatedBy *uuid.UUID,
) (*model.DeviceConfig, *ConfigValidationResult, error) {
	// Save overrides
	if err := m.pgStore.Configs.UpsertDeviceOverrides(ctx, tenantID, deviceID, overrides, updatedBy); err != nil {
		return nil, nil, fmt.Errorf("upsert device overrides: %w", err)
	}

	// Re-generate and push
	return m.GenerateAndPushDeviceConfig(ctx, tenantID, deviceID, updatedBy)
}

// DeleteDeviceOverrides removes overrides and re-generates + pushes config.
func (m *Manager) DeleteDeviceOverrides(
	ctx context.Context,
	tenantID, deviceID uuid.UUID,
	deletedBy *uuid.UUID,
) (*model.DeviceConfig, *ConfigValidationResult, error) {
	if err := m.pgStore.Configs.DeleteDeviceOverrides(ctx, tenantID, deviceID); err != nil {
		return nil, nil, fmt.Errorf("delete device overrides: %w", err)
	}

	// Re-generate and push without overrides
	return m.GenerateAndPushDeviceConfig(ctx, tenantID, deviceID, deletedBy)
}

// ============================================================
// CONFIG PUSH ENGINE
// ============================================================

// pushConfigToSite generates and pushes config to all devices in a site.
func (m *Manager) pushConfigToSite(tenantID, siteID uuid.UUID, template *model.ConfigTemplate) {
	ctx, cancel := context.WithTimeout(m.ctx, 30*time.Second)
	defer cancel()

	// Get all active devices in site
	devices, err := m.pgStore.Configs.GetDevicesBySite(ctx, tenantID, siteID)
	if err != nil {
		m.logger.Error("failed to get site devices for config push",
			zap.String("site_id", siteID.String()),
			zap.Error(err),
		)
		return
	}

	site, err := m.pgStore.Sites.GetByID(ctx, tenantID, siteID)
	if err != nil || site == nil {
		m.logger.Error("failed to get site for config push",
			zap.String("site_id", siteID.String()),
			zap.Error(err),
		)
		return
	}

	m.logger.Info("pushing config to site devices",
		zap.String("site_id", siteID.String()),
		zap.Int64("template_version", template.Version),
		zap.Int("device_count", len(devices)),
	)

	for _, device := range devices {
		// Load device overrides
		var overrides json.RawMessage
		override, err := m.pgStore.Configs.GetDeviceOverrides(ctx, tenantID, device.ID)
		if err != nil {
			m.logger.Error("failed to get device overrides",
				zap.String("device_id", device.ID.String()),
				zap.Error(err),
			)
			continue
		}
		if override != nil {
			overrides = override.Overrides
		}

		// Generate merged config
		mergedConfig, err := GenerateDeviceConfig(template.Config, overrides, device, site)
		if err != nil {
			m.logger.Error("failed to generate device config",
				zap.String("device_id", device.ID.String()),
				zap.Error(err),
			)
			continue
		}

		// Validate
		caps := ParseCapabilities(device.Capabilities)
		validation := ValidateConfig(mergedConfig, caps)
		if validation.HasErrors() {
			m.logger.Warn("device config validation failed, skipping push",
				zap.String("device_id", device.ID.String()),
				zap.Any("errors", validation.Errors),
			)
			continue
		}

		// Store device config
		dc := &model.DeviceConfig{
			TenantID:        tenantID,
			DeviceID:        device.ID,
			Config:          mergedConfig,
			Source:          model.ConfigSourceTemplate,
			TemplateVersion: &template.Version,
			DeviceOverrides: overrides,
			Status:          model.ConfigStatusPending,
		}

		if err := m.pgStore.Configs.CreateDeviceConfig(ctx, dc); err != nil {
			m.logger.Error("failed to store device config",
				zap.String("device_id", device.ID.String()),
				zap.Error(err),
			)
			continue
		}

		// Push to device
		m.pushConfigToDevice(tenantID, device.ID, dc)
	}
}

func (m *Manager) pushConfigToDevice(tenantID, deviceID uuid.UUID, dc *model.DeviceConfig) {
	if !m.sender.IsConnected(deviceID) {
		m.logger.Debug("device not connected, config will be pushed on reconnect",
			zap.String("device_id", deviceID.String()),
			zap.Int64("version", dc.Version),
		)
		return
	}

	// Build ConfigPush payload
	payload := protocol.ConfigPushPayload{
		Version:        dc.Version,
		Config:         dc.Config,
		SafeApply:      true,
		ConfirmTimeout: int(m.safeApplyTimeout.Seconds()),
	}

	// Send via WebSocket
	_, err := m.sender.SendMessage(
		deviceID,
		protocol.ChannelControl,
		protocol.MsgConfigPush,
		protocol.FlagACKRequired,
		&payload,
	)
	if err != nil {
		m.logger.Error("failed to send config push",
			zap.String("device_id", deviceID.String()),
			zap.Int64("version", dc.Version),
			zap.Error(err),
		)
		configPushesTotal.WithLabelValues("failed").Inc()
		return
	}

	// Update status to pushed
	ctx, cancel := context.WithTimeout(m.ctx, 5*time.Second)
	defer cancel()

	if err := m.pgStore.Configs.UpdateDeviceConfigStatus(
		ctx, tenantID, deviceID, dc.Version, model.ConfigStatusPushed, "",
	); err != nil {
		m.logger.Error("failed to update config status to pushed",
			zap.String("device_id", deviceID.String()),
			zap.Error(err),
		)
	}

	// Track safe-apply
	m.tracker.Track(deviceID, tenantID, dc.Version, m.safeApplyTimeout)
	configSafeApplyInflight.Set(float64(m.tracker.Count()))

	configPushesTotal.WithLabelValues("success").Inc()

	m.logger.Info("config pushed to device",
		zap.String("device_id", deviceID.String()),
		zap.Int64("version", dc.Version),
		zap.Bool("safe_apply", true),
	)
}

// ============================================================
// SAFE-APPLY PROTOCOL HANDLING
// ============================================================

// HandleConfigAck processes a ConfigAck from a device.
// Called by the WebSocket handler when a ConfigAck message is received.
// HandleConfigAck processes a ConfigAck from a device.
func (m *Manager) HandleConfigAck(deviceID, tenantID uuid.UUID, ack *protocol.ConfigAckPayload) {
	state := m.tracker.ResolveAck(deviceID, ack.Success, ack.Error)
	configSafeApplyInflight.Set(float64(m.tracker.Count()))

	if state == nil {
		m.logger.Debug("config ack received but no tracked safe-apply",
			zap.String("device_id", deviceID.String()),
			zap.Int64("version", ack.Version),
		)
		m.updateConfigStatusFromAck(tenantID, deviceID, ack)
		return
	}

	if !ack.Success {
		m.logger.Warn("config apply failed on device",
			zap.String("device_id", deviceID.String()),
			zap.Int64("version", ack.Version),
			zap.String("error", ack.Error),
		)

		configPushesTotal.WithLabelValues("failed").Inc()

		ctx, cancel := context.WithTimeout(m.ctx, 5*time.Second)
		defer cancel()

		_ = m.pgStore.Configs.UpdateDeviceConfigStatus(
			ctx, tenantID, deviceID, ack.Version, model.ConfigStatusFailed, ack.Error,
		)
		return
	}

	m.logger.Info("config ack received, waiting for stability",
		zap.String("device_id", deviceID.String()),
		zap.Int64("version", ack.Version),
		zap.Duration("stability_delay", m.stabilityDelay),
	)

	go m.sendConfigConfirmAfterDelay(deviceID, tenantID, ack.Version)
}

// sendConfigConfirmAfterDelay waits for stability period then sends ConfigConfirm.
func (m *Manager) sendConfigConfirmAfterDelay(deviceID, tenantID uuid.UUID, version int64) {
	select {
	case <-time.After(m.stabilityDelay):
		if !m.sender.IsConnected(deviceID) {
			m.logger.Warn("device disconnected during stability wait, config may rollback on device",
				zap.String("device_id", deviceID.String()),
				zap.Int64("version", version),
			)
			m.tracker.Complete(deviceID)
			configSafeApplyInflight.Set(float64(m.tracker.Count()))
			configPushesTotal.WithLabelValues("failed").Inc()

			ctx, cancel := context.WithTimeout(m.ctx, 5*time.Second)
			defer cancel()
			_ = m.pgStore.Configs.UpdateDeviceConfigStatus(
				ctx, tenantID, deviceID, version, model.ConfigStatusFailed,
				"device disconnected during stability wait",
			)
			return
		}

		confirm := protocol.ConfigConfirmPayload{
			Version:   version,
			Confirmed: true,
		}

		_, err := m.sender.SendMessage(
			deviceID,
			protocol.ChannelControl,
			protocol.MsgConfigConfirm,
			0,
			&confirm,
		)
		if err != nil {
			m.logger.Error("failed to send config confirm",
				zap.String("device_id", deviceID.String()),
				zap.Int64("version", version),
				zap.Error(err),
			)
			m.tracker.Complete(deviceID)
			configSafeApplyInflight.Set(float64(m.tracker.Count()))
			configPushesTotal.WithLabelValues("failed").Inc()
			return
		}

		ctx, cancel := context.WithTimeout(m.ctx, 5*time.Second)
		defer cancel()

		if err := m.pgStore.Configs.UpdateDeviceConfigStatus(
			ctx, tenantID, deviceID, version, model.ConfigStatusApplied, "",
		); err != nil {
			m.logger.Error("failed to mark config as applied",
				zap.String("device_id", deviceID.String()),
				zap.Error(err),
			)
		}

		m.tracker.Complete(deviceID)
		configSafeApplyInflight.Set(float64(m.tracker.Count()))

		m.logger.Info("config confirmed and applied",
			zap.String("device_id", deviceID.String()),
			zap.Int64("version", version),
		)

	case <-m.ctx.Done():
		return
	}
}

// updateConfigStatusFromAck updates DB status based on an ACK when there's no tracked safe-apply.
func (m *Manager) updateConfigStatusFromAck(tenantID, deviceID uuid.UUID, ack *protocol.ConfigAckPayload) {
	ctx, cancel := context.WithTimeout(m.ctx, 5*time.Second)
	defer cancel()

	if ack.Success {
		_ = m.pgStore.Configs.UpdateDeviceConfigStatus(
			ctx, tenantID, deviceID, ack.Version, model.ConfigStatusApplied, "",
		)
	} else {
		_ = m.pgStore.Configs.UpdateDeviceConfigStatus(
			ctx, tenantID, deviceID, ack.Version, model.ConfigStatusFailed, ack.Error,
		)
	}
}

// ============================================================
// BACKGROUND WORKERS
// ============================================================

// Start starts the config manager's background workers.
func (m *Manager) Start() {
	m.wg.Add(1)
	go m.runReconciler()

	m.wg.Add(1)
	go m.runSafeApplyTimeoutChecker()

	m.logger.Info("config manager started",
		zap.Duration("reconcile_interval", m.reconcileInterval),
		zap.Duration("safe_apply_timeout", m.safeApplyTimeout),
	)
}

// Stop gracefully stops the config manager.
func (m *Manager) Stop() {
	m.cancel()
	m.wg.Wait()
	m.logger.Info("config manager stopped")
}

// runReconciler periodically checks for config drift and re-pushes.
func (m *Manager) runReconciler() {
	defer m.wg.Done()

	ticker := time.NewTicker(m.reconcileInterval)
	defer ticker.Stop()

	m.logger.Info("config reconciler started",
		zap.Duration("interval", m.reconcileInterval),
	)

	for {
		select {
		case <-ticker.C:
			m.reconcile()
		case <-m.ctx.Done():
			m.logger.Info("config reconciler stopped")
			return
		}
	}
}

// reconcile finds devices with config drift and re-pushes.
func (m *Manager) reconcile() {
	configReconcileTotal.Inc()

	ctx, cancel := context.WithTimeout(m.ctx, 30*time.Second)
	defer cancel()

	rows, err := m.pgStore.Pool.Query(ctx,
		`SELECT DISTINCT tenant_id FROM devices
		WHERE status IN ('online', 'config_pending')
		  AND desired_config_version > applied_config_version
		  AND status != 'decommissioned'`)
	if err != nil {
		m.logger.Error("reconcile: failed to query tenants with drift", zap.Error(err))
		return
	}
	defer rows.Close()

	var tenantIDs []uuid.UUID
	for rows.Next() {
		var tid uuid.UUID
		if err := rows.Scan(&tid); err != nil {
			continue
		}
		tenantIDs = append(tenantIDs, tid)
	}
	rows.Close()

	totalDrift := 0
	for _, tenantID := range tenantIDs {
		devices, err := m.pgStore.Configs.GetDevicesWithConfigDrift(ctx, tenantID)
		if err != nil {
			m.logger.Error("reconcile: failed to get drift devices",
				zap.String("tenant_id", tenantID.String()),
				zap.Error(err),
			)
			continue
		}

		for _, device := range devices {
			if !m.sender.IsConnected(device.ID) {
				continue
			}

			if m.tracker.Get(device.ID) != nil {
				continue
			}

			dc, err := m.pgStore.Configs.GetLatestDeviceConfig(ctx, tenantID, device.ID)
			if err != nil || dc == nil {
				continue
			}

			m.logger.Info("reconciler: re-pushing config",
				zap.String("device_id", device.ID.String()),
				zap.Int64("desired", device.DesiredConfigVersion),
				zap.Int64("applied", device.AppliedConfigVersion),
			)

			m.pushConfigToDevice(tenantID, device.ID, dc)
			totalDrift++
			configReconcileDrift.Inc()
		}
	}

	if totalDrift > 0 {
		m.logger.Info("reconciliation complete",
			zap.Int("devices_with_drift", totalDrift),
		)
	}
}

// runSafeApplyTimeoutChecker checks for timed-out safe-apply operations.
func (m *Manager) runSafeApplyTimeoutChecker() {
	defer m.wg.Done()

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			m.checkSafeApplyTimeouts()
		case <-m.ctx.Done():
			return
		}
	}
}

// checkSafeApplyTimeouts marks timed-out safe-applies as failed.
func (m *Manager) checkSafeApplyTimeouts() {
	timedOut := m.tracker.GetTimedOut()

	for _, state := range timedOut {
		m.logger.Warn("safe-apply timed out, device may have rolled back",
			zap.String("device_id", state.DeviceID.String()),
			zap.Int64("version", state.Version),
			zap.Duration("elapsed", time.Since(state.PushedAt)),
		)

		configPushesTotal.WithLabelValues("timeout").Inc()

		ctx, cancel := context.WithTimeout(m.ctx, 5*time.Second)
		_ = m.pgStore.Configs.UpdateDeviceConfigStatus(
			ctx, state.TenantID, state.DeviceID, state.Version,
			model.ConfigStatusFailed, "safe-apply timeout — no ACK received, device may have rolled back",
		)
		cancel()
	}

	configSafeApplyInflight.Set(float64(m.tracker.Count()))
}

// ============================================================
// VALIDATION ONLY (dry-run)
// ============================================================

// ValidateSiteConfig validates a config without persisting or pushing.
func (m *Manager) ValidateSiteConfig(
	ctx context.Context,
	tenantID, siteID uuid.UUID,
	configData json.RawMessage,
) (*ConfigValidationResult, error) {
	result := ValidateConfig(configData, nil)
	if result.Valid {
		configValidationTotal.WithLabelValues("valid").Inc()
	} else {
		configValidationTotal.WithLabelValues("invalid").Inc()
	}
	return result, nil
}

// ValidateDeviceConfig generates and validates a config for a specific device (dry-run).
func (m *Manager) ValidateDeviceConfig(
	ctx context.Context,
	tenantID, deviceID uuid.UUID,
	configData json.RawMessage,
) (*ConfigValidationResult, error) {
	device, err := m.pgStore.Devices.GetByID(ctx, tenantID, deviceID)
	if err != nil || device == nil {
		return nil, fmt.Errorf("device not found")
	}

	caps := ParseCapabilities(device.Capabilities)
	result := ValidateConfig(configData, caps)
	if result.Valid {
		configValidationTotal.WithLabelValues("valid").Inc()
	} else {
		configValidationTotal.WithLabelValues("invalid").Inc()
	}
	return result, nil
}


// ============================================================
// RECONNECT CONFIG PUSH
// ============================================================

// PushPendingConfigOnReconnect checks if a device has pending config and pushes it.
// Called by the WebSocket hub when a device transitions to online.
func (m *Manager) PushPendingConfigOnReconnect(tenantID, deviceID uuid.UUID) {
	ctx, cancel := context.WithTimeout(m.ctx, 10*time.Second)
	defer cancel()

	// Check if there's config drift
	device, err := m.pgStore.Devices.GetByID(ctx, tenantID, deviceID)
	if err != nil || device == nil {
		m.logger.Debug("reconnect config check: device not found",
			zap.String("device_id", deviceID.String()),
		)
		return
	}

	if device.DesiredConfigVersion <= device.AppliedConfigVersion {
		// No drift — nothing to push
		return
	}

	// Skip if there's already a safe-apply in flight
	if m.tracker.Get(deviceID) != nil {
		return
	}

	// Load latest device config
	dc, err := m.pgStore.Configs.GetLatestDeviceConfig(ctx, tenantID, deviceID)
	if err != nil || dc == nil {
		m.logger.Debug("reconnect config check: no device config found",
			zap.String("device_id", deviceID.String()),
		)
		return
	}

	m.logger.Info("pushing pending config on reconnect",
		zap.String("device_id", deviceID.String()),
		zap.Int64("desired_version", device.DesiredConfigVersion),
		zap.Int64("applied_version", device.AppliedConfigVersion),
		zap.Int64("config_version", dc.Version),
	)

	m.pushConfigToDevice(tenantID, deviceID, dc)
}
