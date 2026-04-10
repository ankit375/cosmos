package telemetry

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/yourorg/cloudctrl/internal/model"
	pgstore "github.com/yourorg/cloudctrl/internal/store/postgres"
	"go.uber.org/zap"
)

// ClientSessionManager handles opening/closing client sessions in the database.
type ClientSessionManager struct {
	metricsStore *pgstore.MetricsStore
	logger       *zap.Logger
}

// NewClientSessionManager creates a new client session manager.
func NewClientSessionManager(metricsStore *pgstore.MetricsStore, logger *zap.Logger) *ClientSessionManager {
	return &ClientSessionManager{
		metricsStore: metricsStore,
		logger:       logger.Named("sessions"),
	}
}

// ProcessDiffResult opens/closes sessions based on a diff result.
func (m *ClientSessionManager) ProcessDiffResult(ctx context.Context,
	deviceID, tenantID uuid.UUID, siteID *uuid.UUID,
	diff model.ClientDiffResult) {

	// Open sessions for connected clients
	for _, c := range diff.Connected {
		session := &model.ClientSession{
			ID:          uuid.New(),
			TenantID:    tenantID,
			DeviceID:    deviceID,
			SiteID:      siteID,
			ClientMAC:   c.MAC,
			SSID:        c.SSID,
			Band:        c.Band,
			ConnectedAt: c.ConnectedSince,
		}
		if c.IP != "" {
			session.ClientIP = &c.IP
		}
		if c.Hostname != "" {
			session.Hostname = &c.Hostname
		}
		rssi := int16(c.RSSI)
		session.AvgRSSI = &rssi

		if err := m.metricsStore.OpenClientSession(ctx, session); err != nil {
			m.logger.Error("failed to open client session",
				zap.String("device_id", deviceID.String()),
				zap.String("client_mac", c.MAC),
								zap.Error(err),
			)
			continue
		}
		clientSessionsOpened.Inc()

		m.logger.Debug("client session opened",
			zap.String("device_id", deviceID.String()),
			zap.String("client_mac", c.MAC),
			zap.String("ssid", c.SSID),
			zap.String("band", c.Band),
		)
	}

	// Close sessions for disconnected clients
	for _, c := range diff.Disconnected {
		if err := m.metricsStore.CloseClientSession(ctx, deviceID, c.MAC,
			time.Now(), "departed", int64(c.TxBytes), int64(c.RxBytes)); err != nil {
			m.logger.Error("failed to close client session",
				zap.String("device_id", deviceID.String()),
				zap.String("client_mac", c.MAC),
				zap.Error(err),
			)
			continue
		}
		clientSessionsClosed.Inc()

		m.logger.Debug("client session closed",
			zap.String("device_id", deviceID.String()),
			zap.String("client_mac", c.MAC),
			zap.String("reason", "departed"),
		)
	}

	// Handle roaming — close old session, open new one
	for _, r := range diff.Roamed {
		clientRoamsDetected.Inc()

		// Close the old session
		if err := m.metricsStore.CloseClientSession(ctx, deviceID, r.Client.MAC,
			time.Now(), "roam", int64(r.Client.TxBytes), int64(r.Client.RxBytes)); err != nil {
			m.logger.Error("failed to close roam session",
				zap.String("device_id", deviceID.String()),
				zap.String("client_mac", r.Client.MAC),
				zap.Error(err),
			)
		} else {
			clientSessionsClosed.Inc()
		}

		// Open a new session with the new band
		session := &model.ClientSession{
			ID:          uuid.New(),
			TenantID:    tenantID,
			DeviceID:    deviceID,
			SiteID:      siteID,
			ClientMAC:   r.Client.MAC,
			SSID:        r.Client.SSID,
			Band:        r.NewBand,
			ConnectedAt: time.Now(),
			Is11r:       true, // Roaming implies fast transition
		}
		if r.Client.IP != "" {
			session.ClientIP = &r.Client.IP
		}
		if r.Client.Hostname != "" {
			session.Hostname = &r.Client.Hostname
		}
		rssi := int16(r.Client.RSSI)
		session.AvgRSSI = &rssi

		if err := m.metricsStore.OpenClientSession(ctx, session); err != nil {
			m.logger.Error("failed to open roam session",
				zap.String("device_id", deviceID.String()),
				zap.String("client_mac", r.Client.MAC),
				zap.Error(err),
			)
		} else {
			clientSessionsOpened.Inc()
		}

		m.logger.Debug("client roamed",
			zap.String("device_id", deviceID.String()),
			zap.String("client_mac", r.Client.MAC),
			zap.String("old_band", r.OldBand),
			zap.String("new_band", r.NewBand),
		)
	}
}

// UpdateActiveSessions updates stats for currently connected clients.
func (m *ClientSessionManager) UpdateActiveSessions(ctx context.Context,
	deviceID uuid.UUID, clients []model.ClientInfo) {

	for _, c := range clients {
		if err := m.metricsStore.UpdateActiveSession(ctx, deviceID, c.MAC,
			int16(c.RSSI), c.TxRate, c.RxRate, int64(c.TxBytes), int64(c.RxBytes)); err != nil {
			m.logger.Debug("failed to update active session",
				zap.String("device_id", deviceID.String()),
				zap.String("client_mac", c.MAC),
				zap.Error(err),
			)
		}
	}
}

// CloseAllForDevice closes all active sessions for a device (on disconnect/offline).
func (m *ClientSessionManager) CloseAllForDevice(ctx context.Context, deviceID uuid.UUID, reason string) {
	if err := m.metricsStore.CloseAllDeviceSessions(ctx, deviceID, reason); err != nil {
		m.logger.Error("failed to close all sessions for device",
			zap.String("device_id", deviceID.String()),
			zap.Error(err),
		)
	}
}
