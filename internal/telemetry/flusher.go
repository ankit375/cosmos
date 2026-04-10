package telemetry

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/yourorg/cloudctrl/internal/model"
	pgstore "github.com/yourorg/cloudctrl/internal/store/postgres"
	"go.uber.org/zap"
)

// Flusher drains the inactive buffer and writes to the database.
type Flusher struct {
	metricsStore   *pgstore.MetricsStore
	sessionManager *ClientSessionManager
	logger         *zap.Logger
}

// NewFlusher creates a new metrics flusher.
func NewFlusher(metricsStore *pgstore.MetricsStore, sessionMgr *ClientSessionManager, logger *zap.Logger) *Flusher {
	return &Flusher{
		metricsStore:   metricsStore,
		sessionManager: sessionMgr,
		logger:         logger.Named("flusher"),
	}
}

// Flush writes a batch of metrics to the database.
func (f *Flusher) Flush(ctx context.Context, batch *MetricsBatch) error {
	if batch.Size() == 0 {
		return nil
	}

	start := time.Now()

	// 1. COPY device_metrics
	if len(batch.DeviceMetrics) > 0 {
		copied, err := f.metricsStore.BatchInsertDeviceMetrics(ctx, batch.DeviceMetrics)
		if err != nil {
			flushErrors.WithLabelValues("device_metrics").Inc()
			f.logger.Error("failed to flush device metrics",
				zap.Int("rows", len(batch.DeviceMetrics)),
				zap.Error(err),
			)
			// Don't return — try to flush radio metrics and client events too
		} else {
			flushDeviceRows.Add(float64(copied))
			f.logger.Debug("flushed device metrics", zap.Int64("rows", copied))
		}
	}

	// 2. COPY radio_metrics
	if len(batch.RadioMetrics) > 0 {
		copied, err := f.metricsStore.BatchInsertRadioMetrics(ctx, batch.RadioMetrics)
		if err != nil {
			flushErrors.WithLabelValues("radio_metrics").Inc()
			f.logger.Error("failed to flush radio metrics",
				zap.Int("rows", len(batch.RadioMetrics)),
				zap.Error(err),
			)
		} else {
			flushRadioRows.Add(float64(copied))
			f.logger.Debug("flushed radio metrics", zap.Int64("rows", copied))
		}
	}

	// 3. Process client events → update sessions
	if len(batch.ClientEvents) > 0 {
		f.processClientEvents(ctx, batch.ClientEvents)
	}

	duration := time.Since(start)
	flushDuration.Observe(duration.Seconds())

	f.logger.Debug("flush complete",
		zap.Int("device_rows", len(batch.DeviceMetrics)),
		zap.Int("radio_rows", len(batch.RadioMetrics)),
		zap.Int("client_events", len(batch.ClientEvents)),
		zap.Duration("duration", duration),
	)

	return nil
}

// processClientEvents converts buffered client events into session operations.
func (f *Flusher) processClientEvents(ctx context.Context, events []ClientEvent) {
	// Group events by device to reduce DB round trips
	type deviceGroup struct {
		tenantID uuid.UUID
		siteID   *uuid.UUID
		diff     model.ClientDiffResult
	}

	groups := make(map[uuid.UUID]*deviceGroup)

	for _, ev := range events {
		deviceID := uuid.UUID(ev.DeviceID)
		g, ok := groups[deviceID]
		if !ok {
			tenantID := uuid.UUID(ev.TenantID)
			var siteID *uuid.UUID
			if ev.SiteID != nil {
				sid := uuid.UUID(*ev.SiteID)
				siteID = &sid
			}
			g = &deviceGroup{
				tenantID: tenantID,
				siteID:   siteID,
			}
			groups[deviceID] = g
		}

		switch ev.EventType {
		case "connect":
			g.diff.Connected = append(g.diff.Connected, ev.ClientInfo)
		case "disconnect":
			g.diff.Disconnected = append(g.diff.Disconnected, ev.ClientInfo)
		case "roam":
			g.diff.Roamed = append(g.diff.Roamed, model.ClientRoamInfo{
				Client:  ev.ClientInfo,
				OldBand: ev.OldBand,
				NewBand: ev.ClientInfo.Band,
			})
		}
	}

	for deviceID, g := range groups {
		f.sessionManager.ProcessDiffResult(ctx, deviceID, g.tenantID, g.siteID, g.diff)
	}
}
