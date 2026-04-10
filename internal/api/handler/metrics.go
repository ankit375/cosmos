package handler

import (
	"fmt"
	"time"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/yourorg/cloudctrl/internal/api/middleware"
	"github.com/yourorg/cloudctrl/internal/model"
	pgstore "github.com/yourorg/cloudctrl/internal/store/postgres"
	"github.com/yourorg/cloudctrl/internal/telemetry"
	"go.uber.org/zap"
)

// MetricsHandler handles telemetry and client API endpoints.
type MetricsHandler struct {
	pgStore   *pgstore.Store
	telemetry *telemetry.Engine
	logger    *zap.Logger
}

// NewMetricsHandler creates a new MetricsHandler.
func NewMetricsHandler(pgStore *pgstore.Store, te *telemetry.Engine, logger *zap.Logger) *MetricsHandler {
	return &MetricsHandler{
		pgStore:   pgStore,
		telemetry: te,
		logger:    logger.Named("metrics-handler"),
	}
}

// GetDeviceMetrics handles GET /api/v1/devices/:id/metrics
func (h *MetricsHandler) GetDeviceMetrics(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)

	deviceID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		RespondBadRequest(c, "Invalid device ID", "")
		return
	}

	// Verify device belongs to tenant
	device, err := h.pgStore.Devices.GetByID(c.Request.Context(), tenantID, deviceID)
	if err != nil {
		h.logger.Error("failed to get device", zap.Error(err))
		RespondInternalError(c)
		return
	}
	if device == nil {
		RespondNotFound(c, "Device not found")
		return
	}

	// Parse time range
	query, err := h.parseMetricsQuery(c, tenantID, deviceID)
	if err != nil {
		RespondBadRequest(c, "Invalid query parameters", err.Error())
		return
	}

	results, err := h.pgStore.Metrics.QueryDeviceMetrics(c.Request.Context(), *query)
	if err != nil {
		h.logger.Error("failed to query device metrics",
			zap.String("device_id", deviceID.String()),
			zap.Error(err),
		)
		RespondInternalError(c)
		return
	}

	RespondOK(c, gin.H{
		"device_id":  deviceID,
		"resolution": query.AutoResolution(),
		"start":      query.Start,
		"end":        query.End,
		"points":     len(results),
		"data":       results,
	})
}

// GetDeviceRadioMetrics handles GET /api/v1/devices/:id/metrics/radio
func (h *MetricsHandler) GetDeviceRadioMetrics(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)

	deviceID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		RespondBadRequest(c, "Invalid device ID", "")
		return
	}

	device, err := h.pgStore.Devices.GetByID(c.Request.Context(), tenantID, deviceID)
	if err != nil {
		h.logger.Error("failed to get device", zap.Error(err))
		RespondInternalError(c)
		return
	}
	if device == nil {
		RespondNotFound(c, "Device not found")
		return
	}

	query, err := h.parseMetricsQuery(c, tenantID, deviceID)
	if err != nil {
		RespondBadRequest(c, "Invalid query parameters", err.Error())
		return
	}
	query.Band = c.Query("band") // optional filter

	results, err := h.pgStore.Metrics.QueryRadioMetrics(c.Request.Context(), *query)
	if err != nil {
		h.logger.Error("failed to query radio metrics",
			zap.String("device_id", deviceID.String()),
			zap.Error(err),
		)
		RespondInternalError(c)
		return
	}

	RespondOK(c, gin.H{
		"device_id":  deviceID,
		"resolution": query.AutoResolution(),
		"band":       query.Band,
		"start":      query.Start,
		"end":        query.End,
		"points":     len(results),
		"data":       results,
	})
}

// GetDeviceClients handles GET /api/v1/devices/:id/clients (live from Redis/memory)
func (h *MetricsHandler) GetDeviceClients(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)

	deviceID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		RespondBadRequest(c, "Invalid device ID", "")
		return
	}

	device, err := h.pgStore.Devices.GetByID(c.Request.Context(), tenantID, deviceID)
	if err != nil {
		h.logger.Error("failed to get device", zap.Error(err))
		RespondInternalError(c)
		return
	}
	if device == nil {
		RespondNotFound(c, "Device not found")
		return
	}

	clients, err := h.telemetry.GetLiveClientsFromRedis(c.Request.Context(), deviceID)
	if err != nil {
		h.logger.Error("failed to get live clients",
			zap.String("device_id", deviceID.String()),
			zap.Error(err),
		)
		RespondInternalError(c)
		return
	}

	if clients == nil {
		clients = []model.ClientInfo{}
	}

	RespondOK(c, gin.H{
		"device_id":    deviceID,
		"client_count": len(clients),
		"clients":      clients,
	})
}

// GetDeviceClientHistory handles GET /api/v1/devices/:id/clients/history
func (h *MetricsHandler) GetDeviceClientHistory(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)

	deviceID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		RespondBadRequest(c, "Invalid device ID", "")
		return
	}

	device, err := h.pgStore.Devices.GetByID(c.Request.Context(), tenantID, deviceID)
	if err != nil {
		h.logger.Error("failed to get device", zap.Error(err))
		RespondInternalError(c)
		return
	}
	if device == nil {
		RespondNotFound(c, "Device not found")
		return
	}

	var params model.ClientSessionQuery
	if err := c.ShouldBindQuery(&params); err != nil {
		RespondBadRequest(c, "Invalid query parameters", err.Error())
		return
	}
	params.TenantID = tenantID
	params.DeviceID = &deviceID

	sessions, total, err := h.pgStore.Metrics.ListClientSessions(c.Request.Context(), params)
	if err != nil {
		h.logger.Error("failed to query client history",
			zap.String("device_id", deviceID.String()),
			zap.Error(err),
		)
		RespondInternalError(c)
		return
	}

	RespondList(c, sessions, total, params.Offset, params.Limit)
}

// GetSiteMetrics handles GET /api/v1/sites/:id/metrics (aggregated)
func (h *MetricsHandler) GetSiteMetrics(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)

	siteID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		RespondBadRequest(c, "Invalid site ID", "")
		return
	}

	site, err := h.pgStore.Sites.GetByID(c.Request.Context(), tenantID, siteID)
	if err != nil {
		h.logger.Error("failed to get site", zap.Error(err))
		RespondInternalError(c)
		return
	}
	if site == nil {
		RespondNotFound(c, "Site not found")
		return
	}

	start, end, err := h.parseTimeRange(c)
	if err != nil {
		RespondBadRequest(c, "Invalid time range", err.Error())
		return
	}

	// Auto-select resolution
	duration := end.Sub(start)
	resolution := "1h"
	if duration > 48*time.Hour {
		resolution = "1d"
	}
	if qRes := c.Query("resolution"); qRes != "" {
		resolution = qRes
	}

	results, err := h.pgStore.Metrics.QuerySiteMetrics(c.Request.Context(), tenantID, siteID, start, end, resolution)
	if err != nil {
		h.logger.Error("failed to query site metrics",
			zap.String("site_id", siteID.String()),
			zap.Error(err),
		)
		RespondInternalError(c)
		return
	}

	RespondOK(c, gin.H{
		"site_id":    siteID,
		"resolution": resolution,
		"start":      start,
		"end":        end,
		"points":     len(results),
		"data":       results,
	})
}

// GetSiteClients handles GET /api/v1/sites/:id/clients (all live clients in site)
func (h *MetricsHandler) GetSiteClients(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)

	siteID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		RespondBadRequest(c, "Invalid site ID", "")
		return
	}

	site, err := h.pgStore.Sites.GetByID(c.Request.Context(), tenantID, siteID)
	if err != nil {
		h.logger.Error("failed to get site", zap.Error(err))
		RespondInternalError(c)
		return
	}
	if site == nil {
		RespondNotFound(c, "Site not found")
		return
	}

	// Get all devices in this site
	deviceParams := model.DeviceListParams{
		TenantID: tenantID,
		SiteID:   &siteID,
		Limit:    200,
	}
	devices, _, err := h.pgStore.Devices.List(c.Request.Context(), deviceParams)
	if err != nil {
		h.logger.Error("failed to list site devices", zap.Error(err))
		RespondInternalError(c)
		return
	}

	deviceIDs := make([]uuid.UUID, len(devices))
	for i, d := range devices {
		deviceIDs[i] = d.ID
	}

	clients, err := h.telemetry.GetSiteClients(c.Request.Context(), deviceIDs)
	if err != nil {
		h.logger.Error("failed to get site clients", zap.Error(err))
		RespondInternalError(c)
		return
	}

	if clients == nil {
		clients = []model.ClientInfo{}
	}

	RespondOK(c, gin.H{
		"site_id":      siteID,
		"device_count": len(devices),
		"client_count": len(clients),
		"clients":      clients,
	})
}

// ── Helper methods ───────────────────────────────────────────

func (h *MetricsHandler) parseMetricsQuery(c *gin.Context, tenantID, deviceID uuid.UUID) (*model.MetricsQuery, error) {
	start, end, err := h.parseTimeRange(c)
	if err != nil {
		return nil, err
	}

	q := &model.MetricsQuery{
		DeviceID:   deviceID,
		TenantID:   tenantID,
		Start:      start,
		End:        end,
		Resolution: c.Query("resolution"),
	}
	return q, nil
}

func (h *MetricsHandler) parseTimeRange(c *gin.Context) (time.Time, time.Time, error) {
	startStr := c.Query("start")
	endStr := c.Query("end")

	var start, end time.Time
	var err error

	if startStr == "" {
		start = time.Now().Add(-6 * time.Hour)
	} else {
		start, err = parseFlexibleTime(startStr)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("invalid start time: %w", err)
		}
	}

	if endStr == "" {
		end = time.Now()
	} else {
		end, err = parseFlexibleTime(endStr)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("invalid end time: %w", err)
		}
	}

	if end.Before(start) {
		return time.Time{}, time.Time{}, fmt.Errorf("end must be after start")
	}

	if end.Sub(start) > 90*24*time.Hour {
		return time.Time{}, time.Time{}, fmt.Errorf("query range exceeds 90 days")
	}

	return start, end, nil
}

// parseFlexibleTime handles RFC3339 timestamps where + may be URL-decoded as space.
func parseFlexibleTime(s string) (time.Time, error) {
	// Try standard RFC3339 first
	t, err := time.Parse(time.RFC3339, s)
	if err == nil {
		return t, nil
	}

	// Try with space replaced back to +  (URL decoding turns + into space)
	t, err2 := time.Parse(time.RFC3339, strings.Replace(s, " ", "+", 1))
	if err2 == nil {
		return t, nil
	}

	// Try RFC3339Nano
	t, err3 := time.Parse(time.RFC3339Nano, s)
	if err3 == nil {
		return t, nil
	}

	return time.Time{}, err
}
