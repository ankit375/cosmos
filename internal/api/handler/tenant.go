package handler

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/yourorg/cloudctrl/internal/api/middleware"
	"github.com/yourorg/cloudctrl/internal/model"
	pgstore "github.com/yourorg/cloudctrl/internal/store/postgres"
	redisstore "github.com/yourorg/cloudctrl/internal/store/redis"
	"go.uber.org/zap"
)

// slugRegex validates tenant slugs: lowercase alphanumeric + hyphens.
var slugRegex = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*[a-z0-9]$`)

// TenantHandler handles tenant management endpoints.
// All endpoints require super_admin role.
type TenantHandler struct {
	pg     *pgstore.Store
	redis  *redisstore.Store
	logger *zap.Logger
}

// NewTenantHandler creates a new TenantHandler.
func NewTenantHandler(pg *pgstore.Store, redis *redisstore.Store, logger *zap.Logger) *TenantHandler {
	return &TenantHandler{
		pg:     pg,
		redis:  redis,
		logger: logger,
	}
}

// List handles GET /api/v1/tenants
func (h *TenantHandler) List(c *gin.Context) {
	ctx := c.Request.Context()

	tenants, err := h.pg.Tenants.List(ctx)
	if err != nil {
		h.logger.Error("list tenants: database error", zap.Error(err))
		RespondInternalError(c, h.logger)
		return
	}

	RespondList(c, tenants, len(tenants), 0, len(tenants))
}

// Get handles GET /api/v1/tenants/:id
func (h *TenantHandler) Get(c *gin.Context) {
	ctx := c.Request.Context()

	tenantID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		RespondError(c, 400, "INVALID_ID", "Invalid tenant ID format")
		return
	}

	tenant, err := h.pg.Tenants.GetByID(ctx, tenantID)
	if err != nil {
		h.logger.Error("get tenant: database error", zap.Error(err))
		RespondInternalError(c, h.logger)
		return
	}
	if tenant == nil {
		RespondNotFound(c, "Tenant")
		return
	}

	// Enrich with limits info
	limits, err := h.pg.Tenants.GetLimits(ctx, tenantID)
	if err != nil {
		h.logger.Warn("get tenant: could not fetch limits", zap.Error(err))
	}

	result := gin.H{
		"tenant": tenant,
	}
	if limits != nil {
		result["limits"] = limits
	}

	RespondOK(c, result)
}

// Create handles POST /api/v1/tenants
func (h *TenantHandler) Create(c *gin.Context) {
	ctx := c.Request.Context()

	var input model.CreateTenantInput
	if !BindAndValidate(c, &input) {
		return
	}

	// Validate slug format
	slug := strings.ToLower(strings.TrimSpace(input.Slug))
	if len(slug) < 2 || len(slug) > 63 {
		RespondValidationError(c, []ValidationError{
			{Field: "slug", Message: "slug must be between 2 and 63 characters"},
		})
		return
	}
	if !slugRegex.MatchString(slug) {
		RespondValidationError(c, []ValidationError{
			{Field: "slug", Message: "slug must contain only lowercase letters, numbers, and hyphens"},
		})
		return
	}

	// Check slug uniqueness
	existing, err := h.pg.Tenants.GetBySlug(ctx, slug)
	if err != nil {
		h.logger.Error("create tenant: check slug", zap.Error(err))
		RespondInternalError(c, h.logger)
		return
	}
	if existing != nil {
		RespondConflict(c, "A tenant with this slug already exists")
		return
	}

	// Defaults
	subscription := input.Subscription
	if subscription == "" {
		subscription = "standard"
	}
	maxDevices := input.MaxDevices
	if maxDevices == 0 {
		maxDevices = 100
	}
	maxSites := input.MaxSites
	if maxSites == 0 {
		maxSites = 15
	}
	maxUsers := input.MaxUsers
	if maxUsers == 0 {
		maxUsers = 50
	}

	tenant := &model.Tenant{
		ID:           uuid.New(),
		Name:         input.Name,
		Slug:         slug,
		Subscription: subscription,
		MaxDevices:   maxDevices,
		MaxSites:     maxSites,
		MaxUsers:     maxUsers,
		Settings:     map[string]interface{}{},
		Active:       true,
	}

	if err := h.pg.Tenants.Create(ctx, tenant); err != nil {
		if IsUniqueViolation(err) {
			RespondConflict(c, "A tenant with this slug already exists")
			return
		}
		h.logger.Error("create tenant: database error", zap.Error(err))
		RespondInternalError(c, h.logger)
		return
	}

	h.logger.Info("tenant created",
		zap.String("tenant_id", tenant.ID.String()),
		zap.String("slug", tenant.Slug),
		zap.String("created_by", middleware.GetUserID(c).String()),
	)

	middleware.SetAuditDetails(c, map[string]interface{}{
		"tenant_name": tenant.Name,
		"slug":        tenant.Slug,
	})

	RespondCreated(c, tenant)
}

// Update handles PUT /api/v1/tenants/:id
func (h *TenantHandler) Update(c *gin.Context) {
	ctx := c.Request.Context()

	tenantID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		RespondError(c, 400, "INVALID_ID", "Invalid tenant ID format")
		return
	}

	var input model.UpdateTenantInput
	if !BindAndValidate(c, &input) {
		return
	}

	// Check tenant exists
	existing, err := h.pg.Tenants.GetByID(ctx, tenantID)
	if err != nil {
		h.logger.Error("update tenant: lookup", zap.Error(err))
		RespondInternalError(c, h.logger)
		return
	}
	if existing == nil {
		RespondNotFound(c, "Tenant")
		return
	}

	if input.MaxSites != nil || input.MaxDevices != nil || input.MaxUsers != nil {
		limits, err := h.pg.Tenants.GetLimits(ctx, tenantID)
		if err != nil {
			h.logger.Error("update tenant: get limits", zap.Error(err))
			RespondInternalError(c, h.logger)
			return
		}

		if input.MaxSites != nil && *input.MaxSites < limits.CurrentSites {
			RespondBadRequest(c, "LIMIT_BELOW_USAGE",
				fmt.Sprintf("Cannot set max_sites below current usage (%d/%d in use)", limits.CurrentSites, *input.MaxSites))
			return
		}
		if input.MaxDevices != nil && *input.MaxDevices < limits.CurrentDevices {
			RespondBadRequest(c, "LIMIT_BELOW_USAGE",
				fmt.Sprintf("Cannot set max_devices below current usage (%d/%d in use)", limits.CurrentDevices, *input.MaxDevices))
			return
		}
		if input.MaxUsers != nil && *input.MaxUsers < limits.CurrentUsers {
			RespondBadRequest(c, "LIMIT_BELOW_USAGE",
				fmt.Sprintf("Cannot set max_users below current usage (%d/%d in use)", limits.CurrentUsers, *input.MaxUsers))
			return
		}
	}

	if err := h.pg.Tenants.Update(ctx, tenantID, &input); err != nil {
		h.logger.Error("update tenant: database error", zap.Error(err))
		RespondInternalError(c, h.logger)
		return
	}

	// Fetch updated tenant
	updated, err := h.pg.Tenants.GetByID(ctx, tenantID)
	if err != nil {
		h.logger.Error("update tenant: fetch updated", zap.Error(err))
		RespondInternalError(c, h.logger)
		return
	}

	h.logger.Info("tenant updated",
		zap.String("tenant_id", tenantID.String()),
		zap.String("updated_by", middleware.GetUserID(c).String()),
	)

	RespondOK(c, updated)
}

// Delete handles DELETE /api/v1/tenants/:id
func (h *TenantHandler) Delete(c *gin.Context) {
	ctx := c.Request.Context()

	tenantID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		RespondError(c, 400, "INVALID_ID", "Invalid tenant ID format")
		return
	}

	// Check tenant exists
	existing, err := h.pg.Tenants.GetByID(ctx, tenantID)
	if err != nil {
		h.logger.Error("delete tenant: lookup", zap.Error(err))
		RespondInternalError(c, h.logger)
		return
	}
	if existing == nil {
		RespondNotFound(c, "Tenant")
		return
	}

	// Prevent deleting a tenant that the calling super_admin belongs to
	callerTenantID := middleware.GetTenantID(c)
	if callerTenantID == tenantID {
		RespondBadRequest(c, "SELF_TENANT_DELETION", "Cannot delete your own tenant")
		return
	}

	// Check if tenant has active devices
	limits, err := h.pg.Tenants.GetLimits(ctx, tenantID)
	if err != nil {
		h.logger.Error("delete tenant: get limits", zap.Error(err))
		RespondInternalError(c, h.logger)
		return
	}
	if limits.CurrentDevices > 0 {
		RespondBadRequest(c, "TENANT_HAS_DEVICES",
			"Cannot delete tenant with active devices. Decommission all devices first.")
		return
	}

	if err := h.pg.Tenants.Delete(ctx, tenantID); err != nil {
		h.logger.Error("delete tenant: database error", zap.Error(err))
		RespondInternalError(c, h.logger)
		return
	}

	h.logger.Info("tenant deleted",
		zap.String("tenant_id", tenantID.String()),
		zap.String("tenant_name", existing.Name),
		zap.String("deleted_by", middleware.GetUserID(c).String()),
	)

	RespondOK(c, gin.H{"message": "Tenant deleted"})
}

// GetLimits handles GET /api/v1/tenants/:id/limits
func (h *TenantHandler) GetLimits(c *gin.Context) {
	ctx := c.Request.Context()

	tenantID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		RespondError(c, 400, "INVALID_ID", "Invalid tenant ID format")
		return
	}

	limits, err := h.pg.Tenants.GetLimits(ctx, tenantID)
	if err != nil {
		h.logger.Error("get tenant limits", zap.Error(err))
		RespondInternalError(c, h.logger)
		return
	}

	RespondOK(c, limits)
}

