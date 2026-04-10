package handler

import (
	"encoding/json"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/yourorg/cloudctrl/internal/api/middleware"
	"github.com/yourorg/cloudctrl/internal/configmgr"
	pgstore "github.com/yourorg/cloudctrl/internal/store/postgres"
	"go.uber.org/zap"
)

// ConfigHandler handles all config-related API endpoints.
type ConfigHandler struct {
	pgStore   *pgstore.Store
	configMgr *configmgr.Manager
	logger    *zap.Logger
}

// NewConfigHandler creates a new config handler.
func NewConfigHandler(
	pgStore *pgstore.Store,
	configMgr *configmgr.Manager,
	logger *zap.Logger,
) *ConfigHandler {
	return &ConfigHandler{
		pgStore:   pgStore,
		configMgr: configMgr,
		logger:    logger.Named("config-handler"),
	}
}

// ============================================================
// SITE CONFIG ENDPOINTS
// ============================================================

// GetSiteConfig returns the latest config template for a site.
// GET /api/v1/sites/:id/config
func (h *ConfigHandler) GetSiteConfig(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)

	siteID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		RespondBadRequest(c, "Invalid site ID", "")
		return
	}

	ctx := c.Request.Context()

	// Verify site exists and belongs to tenant
	site, err := h.pgStore.Sites.GetByID(ctx, tenantID, siteID)
	if err != nil {
		h.logger.Error("failed to get site", zap.Error(err))
		RespondInternalError(c)
		return
	}
	if site == nil {
		RespondNotFound(c, "Site not found")
		return
	}

	template, err := h.pgStore.Configs.GetLatestTemplate(ctx, tenantID, siteID)
	if err != nil {
		h.logger.Error("failed to get site config", zap.Error(err))
		RespondInternalError(c)
		return
	}
	if template == nil {
		// No config yet — return empty response
		RespondOK(c, gin.H{
			"site_id": siteID,
			"config":  nil,
			"version": 0,
			"message": "No configuration template defined for this site",
		})
		return
	}

	RespondOK(c, template)
}

// UpdateSiteConfig creates a new config template version for a site.
// PUT /api/v1/sites/:id/config
func (h *ConfigHandler) UpdateSiteConfig(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)
	userID := middleware.GetUserID(c)

	siteID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		RespondBadRequest(c, "Invalid site ID", "")
		return
	}

	var input struct {
		Config      json.RawMessage `json:"config" binding:"required"`
		Description string          `json:"description"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		RespondBadRequest(c, "Invalid input", err.Error())
		return
	}

	// Validate JSON is a valid object
	var configObj map[string]interface{}
	if err := json.Unmarshal(input.Config, &configObj); err != nil {
		RespondBadRequest(c, "Config must be a valid JSON object", err.Error())
		return
	}

	ctx := c.Request.Context()

	// Verify site exists
	site, err := h.pgStore.Sites.GetByID(ctx, tenantID, siteID)
	if err != nil {
		h.logger.Error("failed to get site", zap.Error(err))
		RespondInternalError(c)
		return
	}
	if site == nil {
		RespondNotFound(c, "Site not found")
		return
	}

	var userIDPtr *uuid.UUID
	if userID != uuid.Nil {
		uid := userID
		userIDPtr = &uid
	}

	template, validationResult, err := h.configMgr.UpdateSiteConfig(
		ctx, tenantID, siteID, input.Config, input.Description, userIDPtr,
	)
	if err != nil {
		h.logger.Error("failed to update site config", zap.Error(err))
		RespondInternalError(c)
		return
	}

	if validationResult != nil && validationResult.HasErrors() {
		c.JSON(422, gin.H{
			"success": false,
			"error": gin.H{
				"code":    "CONFIG_VALIDATION_FAILED",
				"message": "Configuration validation failed",
			},
			"data": gin.H{
				"validation": validationResult,
			},
		})
		return
	}

	response := gin.H{
		"template": template,
		"message":  "Configuration updated successfully",
	}
	if validationResult != nil && len(validationResult.Warnings) > 0 {
		response["warnings"] = validationResult.Warnings
	}

	RespondOK(c, response)
}

// GetSiteConfigHistory returns the version history for a site config.
// GET /api/v1/sites/:id/config/history
func (h *ConfigHandler) GetSiteConfigHistory(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)

	siteID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		RespondBadRequest(c, "Invalid site ID", "")
		return
	}

	limit, offset := parsePagination(c)
	ctx := c.Request.Context()

	// Verify site exists
	site, err := h.pgStore.Sites.GetByID(ctx, tenantID, siteID)
	if err != nil || site == nil {
		RespondNotFound(c, "Site not found")
		return
	}

	templates, total, err := h.pgStore.Configs.ListTemplateHistory(ctx, tenantID, siteID, limit, offset)
	if err != nil {
		h.logger.Error("failed to list config history", zap.Error(err))
		RespondInternalError(c)
		return
	}

	RespondList(c, templates, total, offset, limit)
}

// RollbackSiteConfig rolls back a site config to a previous version.
// POST /api/v1/sites/:id/config/rollback
func (h *ConfigHandler) RollbackSiteConfig(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)
	userID := middleware.GetUserID(c)

	siteID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		RespondBadRequest(c, "Invalid site ID", "")
		return
	}

	var input struct {
		Version int64 `json:"version" binding:"required,min=1"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		RespondBadRequest(c, "Invalid input", err.Error())
		return
	}

	ctx := c.Request.Context()

	// Verify site exists
	site, err := h.pgStore.Sites.GetByID(ctx, tenantID, siteID)
	if err != nil || site == nil {
		RespondNotFound(c, "Site not found")
		return
	}

	var userIDPtr *uuid.UUID
	if userID != uuid.Nil {
		uid := userID
		userIDPtr = &uid
	}

	template, err := h.configMgr.RollbackSiteConfig(ctx, tenantID, siteID, input.Version, userIDPtr)
	if err != nil {
		h.logger.Error("failed to rollback site config",
			zap.String("site_id", siteID.String()),
			zap.Int64("target_version", input.Version),
			zap.Error(err),
		)
		RespondBadRequest(c, "Rollback failed", err.Error())
		return
	}

	RespondOK(c, gin.H{
		"template": template,
		"message":  "Configuration rolled back successfully",
	})
}

// ValidateSiteConfig validates a config without persisting it.
// POST /api/v1/sites/:id/config/validate
func (h *ConfigHandler) ValidateSiteConfig(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)

	siteID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		RespondBadRequest(c, "Invalid site ID", "")
		return
	}

	var input struct {
		Config json.RawMessage `json:"config" binding:"required"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		RespondBadRequest(c, "Invalid input", err.Error())
		return
	}

	ctx := c.Request.Context()

	// Verify site exists
	site, err := h.pgStore.Sites.GetByID(ctx, tenantID, siteID)
	if err != nil || site == nil {
		RespondNotFound(c, "Site not found")
		return
	}

	result, err := h.configMgr.ValidateSiteConfig(ctx, tenantID, siteID, input.Config)
	if err != nil {
		h.logger.Error("failed to validate config", zap.Error(err))
		RespondInternalError(c)
		return
	}

	RespondOK(c, gin.H{
		"valid":    result.Valid,
		"errors":   result.Errors,
		"warnings": result.Warnings,
	})
}

// ============================================================
// DEVICE CONFIG ENDPOINTS
// ============================================================

// GetDeviceConfig returns the effective (latest) config for a device.
// GET /api/v1/devices/:id/config
func (h *ConfigHandler) GetDeviceConfig(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)

	deviceID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		RespondBadRequest(c, "Invalid device ID", "")
		return
	}

	ctx := c.Request.Context()

	// Verify device exists
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

	dc, err := h.pgStore.Configs.GetLatestDeviceConfig(ctx, tenantID, deviceID)
	if err != nil {
		h.logger.Error("failed to get device config", zap.Error(err))
		RespondInternalError(c)
		return
	}
	if dc == nil {
		RespondOK(c, gin.H{
			"device_id":              deviceID,
			"config":                 nil,
			"desired_config_version": device.DesiredConfigVersion,
			"applied_config_version": device.AppliedConfigVersion,
			"message":                "No configuration assigned to this device",
		})
		return
	}

	RespondOK(c, gin.H{
		"config":                 dc,
		"desired_config_version": device.DesiredConfigVersion,
		"applied_config_version": device.AppliedConfigVersion,
		"config_in_sync":         device.DesiredConfigVersion == device.AppliedConfigVersion,
	})
}

// GetDeviceOverrides returns the per-device config overrides.
// GET /api/v1/devices/:id/config/overrides
func (h *ConfigHandler) GetDeviceOverrides(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)

	deviceID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		RespondBadRequest(c, "Invalid device ID", "")
		return
	}

	ctx := c.Request.Context()

	// Verify device exists
	device, err := h.pgStore.Devices.GetByID(ctx, tenantID, deviceID)
	if err != nil || device == nil {
		RespondNotFound(c, "Device not found")
		return
	}

	override, err := h.pgStore.Configs.GetDeviceOverrides(ctx, tenantID, deviceID)
	if err != nil {
		h.logger.Error("failed to get device overrides", zap.Error(err))
		RespondInternalError(c)
		return
	}

	if override == nil {
		RespondOK(c, gin.H{
			"device_id": deviceID,
			"overrides": json.RawMessage("{}"),
			"message":   "No overrides defined for this device",
		})
		return
	}

	RespondOK(c, override)
}

// UpdateDeviceOverrides sets per-device config overrides and triggers re-push.
// PUT /api/v1/devices/:id/config/overrides
func (h *ConfigHandler) UpdateDeviceOverrides(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)
	userID := middleware.GetUserID(c)

	deviceID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		RespondBadRequest(c, "Invalid device ID", "")
		return
	}

	var input struct {
		Overrides json.RawMessage `json:"overrides" binding:"required"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		RespondBadRequest(c, "Invalid input", err.Error())
		return
	}

	// Validate overrides is a JSON object
	var overridesObj map[string]interface{}
	if err := json.Unmarshal(input.Overrides, &overridesObj); err != nil {
		RespondBadRequest(c, "Overrides must be a valid JSON object", err.Error())
		return
	}

	ctx := c.Request.Context()

	// Verify device exists and is adopted
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
	if device.SiteID == nil {
		RespondBadRequest(c, "Device has no site assignment", "Device must be adopted before configuring overrides")
		return
	}

	var userIDPtr *uuid.UUID
	if userID != uuid.Nil {
		uid := userID
		userIDPtr = &uid
	}

	dc, validationResult, err := h.configMgr.UpdateDeviceOverrides(
		ctx, tenantID, deviceID, input.Overrides, userIDPtr,
	)
	if err != nil {
		h.logger.Error("failed to update device overrides",
			zap.String("device_id", deviceID.String()),
			zap.Error(err),
		)
		RespondBadRequest(c, "Failed to update overrides", err.Error())
		return
	}

	if validationResult != nil && validationResult.HasErrors() {
		c.JSON(422, gin.H{
			"success": false,
			"error": gin.H{
				"code":    "CONFIG_VALIDATION_FAILED",
				"message": "Merged configuration validation failed",
			},
			"data": gin.H{
				"validation": validationResult,
			},
		})
		return
	}

	response := gin.H{
		"device_config": dc,
		"message":       "Overrides updated and config pushed",
	}
	if validationResult != nil && len(validationResult.Warnings) > 0 {
		response["warnings"] = validationResult.Warnings
	}

	RespondOK(c, response)
}

// DeleteDeviceOverrides removes all per-device overrides and re-pushes base config.
// DELETE /api/v1/devices/:id/config/overrides
func (h *ConfigHandler) DeleteDeviceOverrides(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)
	userID := middleware.GetUserID(c)

	deviceID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		RespondBadRequest(c, "Invalid device ID", "")
		return
	}

	ctx := c.Request.Context()

	// Verify device exists
	device, err := h.pgStore.Devices.GetByID(ctx, tenantID, deviceID)
	if err != nil || device == nil {
		RespondNotFound(c, "Device not found")
		return
	}

	var userIDPtr *uuid.UUID
	if userID != uuid.Nil {
		uid := userID
		userIDPtr = &uid
	}

	dc, validationResult, err := h.configMgr.DeleteDeviceOverrides(ctx, tenantID, deviceID, userIDPtr)
	if err != nil {
		h.logger.Error("failed to delete device overrides",
			zap.String("device_id", deviceID.String()),
			zap.Error(err),
		)
		RespondBadRequest(c, "Failed to delete overrides", err.Error())
		return
	}

	response := gin.H{
		"device_config": dc,
		"message":       "Overrides removed and config re-pushed",
	}
	if validationResult != nil && len(validationResult.Warnings) > 0 {
		response["warnings"] = validationResult.Warnings
	}

	RespondOK(c, response)
}

// GetDeviceConfigHistory returns the config version history for a device.
// GET /api/v1/devices/:id/config/history
func (h *ConfigHandler) GetDeviceConfigHistory(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)

	deviceID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		RespondBadRequest(c, "Invalid device ID", "")
		return
	}

	limit, offset := parsePagination(c)
	ctx := c.Request.Context()

	// Verify device exists
	device, err := h.pgStore.Devices.GetByID(ctx, tenantID, deviceID)
	if err != nil || device == nil {
		RespondNotFound(c, "Device not found")
		return
	}

	configs, total, err := h.pgStore.Configs.ListDeviceConfigHistory(ctx, tenantID, deviceID, limit, offset)
	if err != nil {
		h.logger.Error("failed to list device config history", zap.Error(err))
		RespondInternalError(c)
		return
	}

	RespondList(c, configs, total, offset, limit)
}

// RollbackDeviceConfig rolls back a device config to a previous version.
// POST /api/v1/devices/:id/config/rollback
func (h *ConfigHandler) RollbackDeviceConfig(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)
	userID := middleware.GetUserID(c)

	deviceID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		RespondBadRequest(c, "Invalid device ID", "")
		return
	}

	var input struct {
		Version int64 `json:"version" binding:"required,min=1"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		RespondBadRequest(c, "Invalid input", err.Error())
		return
	}

	ctx := c.Request.Context()

	// Verify device exists
	device, err := h.pgStore.Devices.GetByID(ctx, tenantID, deviceID)
	if err != nil || device == nil {
		RespondNotFound(c, "Device not found")
		return
	}

	var userIDPtr *uuid.UUID
	if userID != uuid.Nil {
		uid := userID
		userIDPtr = &uid
	}

	dc, err := h.configMgr.RollbackDeviceConfig(ctx, tenantID, deviceID, input.Version, userIDPtr)
	if err != nil {
		h.logger.Error("failed to rollback device config",
			zap.String("device_id", deviceID.String()),
			zap.Int64("target_version", input.Version),
			zap.Error(err),
		)
		RespondBadRequest(c, "Rollback failed", err.Error())
		return
	}

	RespondOK(c, gin.H{
		"device_config": dc,
		"message":       "Configuration rolled back successfully",
	})
}

// ForcePushDeviceConfig re-pushes the latest config to a device.
// POST /api/v1/devices/:id/config/push
func (h *ConfigHandler) ForcePushDeviceConfig(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)

	deviceID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		RespondBadRequest(c, "Invalid device ID", "")
		return
	}

	ctx := c.Request.Context()

	// Verify device exists
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

	if err := h.configMgr.ForcePushDeviceConfig(ctx, tenantID, deviceID); err != nil {
		h.logger.Error("failed to force push config",
			zap.String("device_id", deviceID.String()),
			zap.Error(err),
		)
		RespondBadRequest(c, "Force push failed", err.Error())
		return
	}

	RespondOK(c, gin.H{
		"message": "Configuration push initiated",
	})
}

// ============================================================
// HELPERS
// ============================================================

// parsePagination extracts limit and offset from query parameters.
func parsePagination(c *gin.Context) (int, int) {
	limit := 20
	offset := 0

	if l := c.Query("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 {
			limit = parsed
			if limit > 100 {
				limit = 100
			}
		}
	}

	if o := c.Query("offset"); o != "" {
		if parsed, err := strconv.Atoi(o); err == nil && parsed >= 0 {
			offset = parsed
		}
	}

	return limit, offset
}
