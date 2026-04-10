package handler

import (
	"fmt"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/yourorg/cloudctrl/internal/api/middleware"
	devpkg "github.com/yourorg/cloudctrl/internal/device"
	"github.com/yourorg/cloudctrl/internal/model"
	pgstore "github.com/yourorg/cloudctrl/internal/store/postgres"
	ws "github.com/yourorg/cloudctrl/internal/websocket"
	"go.uber.org/zap"
)

type DeviceHandler struct {
	pgStore *pgstore.Store
	hub     *ws.Hub
	logger  *zap.Logger
}

func NewDeviceHandler(pgStore *pgstore.Store, hub *ws.Hub, logger *zap.Logger) *DeviceHandler {
	return &DeviceHandler{
		pgStore: pgStore,
		hub:     hub,
		logger:  logger.Named("device-handler"),
	}
}

func (h *DeviceHandler) List(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)

	var params model.DeviceListParams
	if err := c.ShouldBindQuery(&params); err != nil {
		RespondBadRequest(c, "Invalid query parameters", err.Error())
		return
	}
	params.TenantID = tenantID

	devices, total, err := h.pgStore.Devices.List(c.Request.Context(), params)
	if err != nil {
		h.logger.Error("failed to list devices", zap.Error(err))
		RespondInternalError(c)
		return
	}

	RespondList(c, devices, total, params.Offset, params.Limit)
}

func (h *DeviceHandler) Get(c *gin.Context) {
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

	RespondOK(c, device)
}

func (h *DeviceHandler) Update(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)

	deviceID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		RespondBadRequest(c, "Invalid device ID", "")
		return
	}

	var input model.UpdateDeviceInput
	if err := c.ShouldBindJSON(&input); err != nil {
		RespondBadRequest(c, "Invalid input", err.Error())
		return
	}

	ctx := c.Request.Context()

	// Verify device exists and isn't decommissioned (NEW)
	device, err := h.pgStore.Devices.GetByID(ctx, tenantID, deviceID)
	if err != nil {
		h.logger.Error("failed to get device", zap.Error(err))
		RespondInternalError(c)
		return
	}
	if device == nil {
		RespondNotFound(c, "Device not found")
		return
	}
	if devpkg.IsTerminal(device.Status) {
		RespondBadRequest(c, "Cannot update decommissioned device", "")
		return
	}

	if err := h.pgStore.Devices.Update(ctx, tenantID, deviceID, &input); err != nil {
		h.logger.Error("failed to update device", zap.Error(err))
		RespondInternalError(c)
		return
	}

	device, _ = h.pgStore.Devices.GetByID(ctx, tenantID, deviceID)
	RespondOK(c, device)
}

func (h *DeviceHandler) Delete(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)
	userID := middleware.GetUserID(c)

	deviceID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		RespondBadRequest(c, "Invalid device ID", "")
		return
	}

	ctx := c.Request.Context()

	// Verify device exists (NEW)
	device, err := h.pgStore.Devices.GetByID(ctx, tenantID, deviceID)
	if err != nil {
		h.logger.Error("failed to get device", zap.Error(err))
		RespondInternalError(c)
		return
	}
	if device == nil {
		RespondNotFound(c, "Device not found")
		return
	}
	if device.Status == model.DeviceStatusDecommissioned {
		RespondBadRequest(c, "Device is already decommissioned", "")
		return
	}

	// Validate state transition (NEW)
	if err := devpkg.ValidateTransition(device.Status, model.DeviceStatusDecommissioned); err != nil {
		RespondBadRequest(c, "Cannot decommission device in current state", err.Error())
		return
	}

	// Close WebSocket connection if active
	if conn := h.hub.GetConnection(deviceID); conn != nil {
		conn.Close()
	}

	// Soft delete in DB
	if err := h.pgStore.Devices.Delete(ctx, tenantID, deviceID); err != nil {
		h.logger.Error("failed to delete device", zap.Error(err))
		RespondInternalError(c)
		return
	}

	// Clean up state store (NEW)
	h.hub.StateStore().Delete(deviceID)

	// Emit decommission event (NEW)
	go h.hub.EventEmitter().DeviceDecommissioned(ctx, tenantID, deviceID, userID)

	h.logger.Info("device decommissioned",
		zap.String("device_id", deviceID.String()),
		zap.String("user_id", userID.String()),
	)

	RespondOK(c, gin.H{"message": "Device decommissioned"})
}

func (h *DeviceHandler) Adopt(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)

	deviceID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		RespondBadRequest(c, "Invalid device ID", "")
		return
	}

	var input model.AdoptDeviceInput
	if err := c.ShouldBindJSON(&input); err != nil {
		RespondBadRequest(c, "Invalid input", err.Error())
		return
	}

	ctx := c.Request.Context()

	device, err := h.pgStore.Devices.GetByID(ctx, tenantID, deviceID)
	if err != nil {
		h.logger.Error("failed to get device", zap.Error(err))
		RespondInternalError(c)
		return
	}
	if device == nil {
		RespondNotFound(c, "Device not found")
		return
	}
	if device.Status != model.DeviceStatusPendingAdopt {
		RespondBadRequest(c, "Device is not pending adoption",
			"current status: "+string(device.Status))
		return
	}

	// Validate state transition (NEW)
	if err := devpkg.ValidateTransition(device.Status, model.DeviceStatusProvisioning); err != nil {
		RespondBadRequest(c, "Invalid state transition", err.Error())
		return
	}

	site, err := h.pgStore.Sites.GetByID(ctx, tenantID, input.SiteID)
	if err != nil {
		h.logger.Error("failed to get site", zap.Error(err))
		RespondInternalError(c)
		return
	}
	if site == nil {
		RespondBadRequest(c, "Site not found", "")
		return
	}

	count, err := h.pgStore.Devices.CountByTenant(ctx, tenantID)
	if err != nil {
		h.logger.Error("failed to count devices", zap.Error(err))
		RespondInternalError(c)
		return
	}

	tenant, err := h.pgStore.Tenants.GetByID(ctx, tenantID)
	if err != nil || tenant == nil {
		RespondInternalError(c)
		return
	}
	if count >= tenant.MaxDevices {
		RespondBadRequest(c, "Tenant device limit reached",
			fmt.Sprintf("maximum devices allowed: %d", tenant.MaxDevices))
		return
	}

	name := input.Name
	if name == "" {
		name = device.Model + "-" + device.MAC[len(device.MAC)-5:]
	}

	token, err := h.hub.HandleAdoption(ctx, tenantID, deviceID, input.SiteID, name)
	if err != nil {
		h.logger.Error("failed to adopt device", zap.Error(err))
		RespondInternalError(c)
		return
	}

	// Return the updated device
	updatedDevice, _ := h.pgStore.Devices.GetByID(ctx, tenantID, deviceID)

	RespondOK(c, gin.H{
		"message":      "Device adopted successfully",
		"device":       updatedDevice,
		"device_token": token,
	})
}

func (h *DeviceHandler) Move(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)

	deviceID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		RespondBadRequest(c, "Invalid device ID", "")
		return
	}

	var input model.MoveDeviceInput
	if err := c.ShouldBindJSON(&input); err != nil {
		RespondBadRequest(c, "Invalid input", err.Error())
		return
	}

	ctx := c.Request.Context()

	// Verify device exists and is adopted (NEW)
	device, err := h.pgStore.Devices.GetByID(ctx, tenantID, deviceID)
	if err != nil {
		h.logger.Error("failed to get device", zap.Error(err))
		RespondInternalError(c)
		return
	}
	if device == nil {
		RespondNotFound(c, "Device not found")
		return
	}
	if !devpkg.IsAdopted(device.Status) {
		RespondBadRequest(c, "Device must be adopted before moving", "")
		return
	}

	site, err := h.pgStore.Sites.GetByID(ctx, tenantID, input.SiteID)
	if err != nil || site == nil {
		RespondBadRequest(c, "Site not found", "")
		return
	}

	_, err = h.pgStore.Pool.Exec(ctx, `
		UPDATE devices SET site_id = \$1, updated_at = NOW()
		WHERE id = \$2 AND tenant_id = \$3`,
		input.SiteID, deviceID, tenantID)
	if err != nil {
		h.logger.Error("failed to move device", zap.Error(err))
		RespondInternalError(c)
		return
	}

	h.hub.StateStore().Update(deviceID, func(state *ws.DeviceState) {
		state.SiteID = &input.SiteID
		state.Dirty = true
	})

	RespondOK(c, gin.H{"message": "Device moved successfully"})
}

func (h *DeviceHandler) ListPending(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)

	devices, err := h.pgStore.Devices.GetPendingAdopt(c.Request.Context(), tenantID)
	if err != nil {
		h.logger.Error("failed to list pending devices", zap.Error(err))
		RespondInternalError(c)
		return
	}

	RespondOK(c, devices)
}

func (h *DeviceHandler) Stats(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)

	var siteID *uuid.UUID
	if sid := c.Query("site_id"); sid != "" {
		parsed, err := uuid.Parse(sid)
		if err != nil {
			RespondBadRequest(c, "Invalid site_id", "")
			return
		}
		siteID = &parsed
	}

	stats, err := h.pgStore.Devices.GetStats(c.Request.Context(), tenantID, siteID)
	if err != nil {
		h.logger.Error("failed to get device stats", zap.Error(err))
		RespondInternalError(c)
		return
	}

	connectedCount := 0
	if siteID != nil {
		connectedCount = len(h.hub.GetBySite(*siteID))
	} else {
		connectedCount = len(h.hub.GetByTenant(tenantID))
	}

	RespondOK(c, gin.H{
		"stats":     stats,
		"connected": connectedCount,
	})
}

func (h *DeviceHandler) LiveStatus(c *gin.Context) {
	deviceID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		RespondBadRequest(c, "Invalid device ID", "")
		return
	}

	state := h.hub.StateStore().Get(deviceID)
	if state == nil {
		RespondNotFound(c, "Device state not found (device may not have connected)")
		return
	}

	tenantID := middleware.GetTenantID(c)
	if state.TenantID != tenantID {
		RespondNotFound(c, "Device not found")
		return
	}

	connected := h.hub.IsConnected(deviceID)

	RespondOK(c, gin.H{
		"device_id":              state.DeviceID,
		"status":                 state.Status,
		"connected":              connected,
		"firmware_version":       state.FirmwareVersion,
		"desired_config_version": state.DesiredConfigVersion,
		"applied_config_version": state.AppliedConfigVersion,
		"ip_address":             state.IPAddress,
		"uptime":                 state.Uptime,
		"client_count":           state.ClientCount,
		"cpu_usage":              state.CPUUsage,
		"memory_used":            state.MemoryUsed,
		"memory_total":           state.MemoryTotal,
		"last_heartbeat":         state.LastHeartbeat,
		"last_metrics":           state.LastMetrics,
	})
}
