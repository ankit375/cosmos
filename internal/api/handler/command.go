package handler

import (
	"encoding/json"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/yourorg/cloudctrl/internal/api/middleware"
	"github.com/yourorg/cloudctrl/internal/command"
	"github.com/yourorg/cloudctrl/internal/model"
	pgstore "github.com/yourorg/cloudctrl/internal/store/postgres"
	"go.uber.org/zap"
)

// CommandHandler handles command API endpoints.
type CommandHandler struct {
	pgStore    *pgstore.Store
	commandMgr *command.Manager
	logger     *zap.Logger
}

// NewCommandHandler creates a new command handler.
func NewCommandHandler(
	pgStore *pgstore.Store,
	commandMgr *command.Manager,
	logger *zap.Logger,
) *CommandHandler {
	return &CommandHandler{
		pgStore:    pgStore,
		commandMgr: commandMgr,
		logger:     logger.Named("command-handler"),
	}
}

// ============================================================
// REBOOT
// ============================================================

// Reboot sends a reboot command to a device.
// POST /api/v1/devices/:id/reboot
func (h *CommandHandler) Reboot(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)
	userID := middleware.GetUserID(c)

	deviceID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		RespondBadRequest(c, "Invalid device ID", "")
		return
	}

	ctx := c.Request.Context()

	// Verify device exists and belongs to tenant
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
		RespondBadRequest(c, "Cannot send commands to decommissioned device", "")
		return
	}

	cmd, err := h.commandMgr.EnqueueCommand(ctx, tenantID, deviceID, "reboot", nil, 1, &userID)
	if err != nil {
		h.logger.Error("failed to enqueue reboot command", zap.Error(err))
		RespondInternalError(c)
		return
	}

	h.logger.Info("reboot command sent",
		zap.String("device_id", deviceID.String()),
		zap.String("command_id", cmd.ID.String()),
		zap.String("user_id", userID.String()),
	)

	RespondOK(c, gin.H{
		"message":    "Reboot command queued",
		"command_id": cmd.ID,
		"status":     cmd.Status,
	})
}

// ============================================================
// LED LOCATE
// ============================================================

// Locate sends an LED locate command to a device.
// POST /api/v1/devices/:id/locate
func (h *CommandHandler) Locate(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)
	userID := middleware.GetUserID(c)

	deviceID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		RespondBadRequest(c, "Invalid device ID", "")
		return
	}

	// Optional params: duration
	var input struct {
		Duration int `json:"duration"` // seconds
	}
	_ = c.ShouldBindJSON(&input)
	if input.Duration <= 0 {
		input.Duration = 30
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
	if device.Status == model.DeviceStatusDecommissioned {
		RespondBadRequest(c, "Cannot send commands to decommissioned device", "")
		return
	}

	payload, _ := json.Marshal(map[string]interface{}{
		"duration": input.Duration,
	})

	cmd, err := h.commandMgr.EnqueueCommand(ctx, tenantID, deviceID, "locate", payload, 3, &userID)
	if err != nil {
		h.logger.Error("failed to enqueue locate command", zap.Error(err))
		RespondInternalError(c)
		return
	}

	RespondOK(c, gin.H{
		"message":    "Locate command queued",
		"command_id": cmd.ID,
		"status":     cmd.Status,
	})
}

// ============================================================
// KICK CLIENT
// ============================================================

// KickClient sends a client kick command to a device.
// POST /api/v1/devices/:id/kick-client
func (h *CommandHandler) KickClient(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)
	userID := middleware.GetUserID(c)

	deviceID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		RespondBadRequest(c, "Invalid device ID", "")
		return
	}

	var input struct {
		MAC    string `json:"mac" binding:"required"`
		Reason string `json:"reason"`
		Ban    int    `json:"ban"` // ban duration in seconds, 0 = no ban
	}
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
	if device.Status == model.DeviceStatusDecommissioned {
		RespondBadRequest(c, "Cannot send commands to decommissioned device", "")
		return
	}

	payload, _ := json.Marshal(map[string]interface{}{
		"mac":    input.MAC,
		"reason": input.Reason,
		"ban":    input.Ban,
	})

	cmd, err := h.commandMgr.EnqueueCommand(ctx, tenantID, deviceID, "kick_client", payload, 2, &userID)
	if err != nil {
		h.logger.Error("failed to enqueue kick client command", zap.Error(err))
		RespondInternalError(c)
		return
	}

	RespondOK(c, gin.H{
		"message":    "Kick client command queued",
		"command_id": cmd.ID,
		"status":     cmd.Status,
	})
}

// ============================================================
// WIFI SCAN
// ============================================================

// Scan triggers a WiFi environment scan on a device.
// POST /api/v1/devices/:id/scan
func (h *CommandHandler) Scan(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)
	userID := middleware.GetUserID(c)

	deviceID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		RespondBadRequest(c, "Invalid device ID", "")
		return
	}

	var input struct {
		Band     string `json:"band"`     // "2g", "5g", "all"
		Duration int    `json:"duration"` // scan duration in seconds
	}
	_ = c.ShouldBindJSON(&input)
	if input.Band == "" {
		input.Band = "all"
	}
	if input.Duration <= 0 {
		input.Duration = 10
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
	if device.Status == model.DeviceStatusDecommissioned {
		RespondBadRequest(c, "Cannot send commands to decommissioned device", "")
		return
	}

	payload, _ := json.Marshal(map[string]interface{}{
		"band":     input.Band,
		"duration": input.Duration,
	})

	cmd, err := h.commandMgr.EnqueueCommand(ctx, tenantID, deviceID, "wifi_scan", payload, 5, &userID)
	if err != nil {
		h.logger.Error("failed to enqueue scan command", zap.Error(err))
		RespondInternalError(c)
		return
	}

	RespondOK(c, gin.H{
		"message":    "WiFi scan command queued",
		"command_id": cmd.ID,
		"status":     cmd.Status,
	})
}

// ============================================================
// LIST COMMANDS
// ============================================================

// ListCommands returns command history for a device.
// GET /api/v1/devices/:id/commands
func (h *CommandHandler) ListCommands(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)

	deviceID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		RespondBadRequest(c, "Invalid device ID", "")
		return
	}

	limit := 50
	offset := 0
	if l := c.Query("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 {
			limit = parsed
		}
	}
	if o := c.Query("offset"); o != "" {
		if parsed, err := strconv.Atoi(o); err == nil && parsed >= 0 {
			offset = parsed
		}
	}

	ctx := c.Request.Context()

	// Verify device belongs to tenant
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

	cmds, total, err := h.commandMgr.GetDeviceCommands(ctx, tenantID, deviceID, limit, offset)
	if err != nil {
		h.logger.Error("failed to list commands", zap.Error(err))
		RespondInternalError(c)
		return
	}

	RespondList(c, cmds, total, offset, limit)
}
