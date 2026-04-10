package handler

import (
	"fmt"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/yourorg/cloudctrl/internal/api/middleware"
	"github.com/yourorg/cloudctrl/internal/model"
	pgstore "github.com/yourorg/cloudctrl/internal/store/postgres"
	redisstore "github.com/yourorg/cloudctrl/internal/store/redis"
	"go.uber.org/zap"
)

// SiteHandler handles site CRUD endpoints.
type SiteHandler struct {
	pg     *pgstore.Store
	redis  *redisstore.Store
	logger *zap.Logger
}

// NewSiteHandler creates a new SiteHandler.
func NewSiteHandler(pg *pgstore.Store, redis *redisstore.Store, logger *zap.Logger) *SiteHandler {
	return &SiteHandler{
		pg:     pg,
		redis:  redis,
		logger: logger,
	}
}

// List handles GET /api/v1/sites
func (h *SiteHandler) List(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)
	ctx := c.Request.Context()

	sites, err := h.pg.Sites.List(ctx, tenantID)
	if err != nil {
		h.logger.Error("list sites: database error", zap.Error(err))
		RespondInternalError(c, h.logger)
		return
	}

	RespondList(c, sites, len(sites), 0, len(sites))
}

// Get handles GET /api/v1/sites/:id
func (h *SiteHandler) Get(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)
	ctx := c.Request.Context()

	siteID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		RespondError(c, 400, "INVALID_ID", "Invalid site ID format")
		return
	}

	site, err := h.pg.Sites.GetByID(ctx, tenantID, siteID)
	if err != nil {
		h.logger.Error("get site: database error", zap.Error(err))
		RespondInternalError(c, h.logger)
		return
	}
	if site == nil {
		RespondNotFound(c, "Site")
		return
	}

	RespondOK(c, site)
}

// Create handles POST /api/v1/sites
func (h *SiteHandler) Create(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)
	ctx := c.Request.Context()

	var input model.CreateSiteInput
	if !BindAndValidate(c, &input) {
		return
	}

	// Check tenant site limit
	if err := h.pg.Tenants.CheckSiteLimit(ctx, tenantID); err != nil {
		h.logger.Warn("create site: limit reached",
			zap.String("tenant_id", tenantID.String()),
			zap.Error(err),
		)
		RespondError(c, 403, "LIMIT_EXCEEDED", err.Error())
		return
	}

	// Check site name uniqueness within tenant
	existingSites, err := h.pg.Sites.List(ctx, tenantID)
	if err != nil {
		h.logger.Error("create site: list existing", zap.Error(err))
		RespondInternalError(c, h.logger)
		return
	}
	for _, s := range existingSites {
		if s.Name == input.Name {
			RespondConflict(c, "A site with this name already exists in this tenant")
			return
		}
	}

	// Defaults
	timezone := input.Timezone
	if timezone == "" {
		timezone = "UTC"
	}
	countryCode := input.CountryCode
	if countryCode == "" {
		countryCode = "US"
	}

	site := &model.Site{
		ID:          uuid.New(),
		TenantID:    tenantID,
		Name:        input.Name,
		Description: input.Description,
		Address:     input.Address,
		Timezone:    timezone,
		CountryCode: countryCode,
		Latitude:    input.Latitude,
		Longitude:   input.Longitude,
		AutoAdopt:   input.AutoAdopt,
		AutoUpgrade: input.AutoUpgrade,
		Settings:    map[string]interface{}{},
	}

	if err := h.pg.Sites.Create(ctx, site); err != nil {
		if IsUniqueViolation(err) {
			RespondConflict(c, "A site with this name already exists in this tenant")
			return
		}
		h.logger.Error("create site: database error", zap.Error(err))
		RespondInternalError(c, h.logger)
		return
	}

	h.logger.Info("site created",
		zap.String("site_id", site.ID.String()),
		zap.String("site_name", site.Name),
		zap.String("tenant_id", tenantID.String()),
		zap.String("created_by", middleware.GetUserID(c).String()),
	)

	middleware.SetAuditDetails(c, map[string]interface{}{
		"site_name": site.Name,
	})

	RespondCreated(c, site)
}

// Update handles PUT /api/v1/sites/:id
func (h *SiteHandler) Update(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)
	ctx := c.Request.Context()

	siteID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		RespondError(c, 400, "INVALID_ID", "Invalid site ID format")
		return
	}

	var input model.UpdateSiteInput
	if !BindAndValidate(c, &input) {
		return
	}

	// Check site exists in this tenant
	existing, err := h.pg.Sites.GetByID(ctx, tenantID, siteID)
	if err != nil {
		h.logger.Error("update site: lookup", zap.Error(err))
		RespondInternalError(c, h.logger)
		return
	}
	if existing == nil {
		RespondNotFound(c, "Site")
		return
	}

	// If name is changing, check uniqueness
	if input.Name != nil && *input.Name != existing.Name {
		sites, err := h.pg.Sites.List(ctx, tenantID)
		if err != nil {
			h.logger.Error("update site: check name uniqueness", zap.Error(err))
			RespondInternalError(c, h.logger)
			return
		}
		for _, s := range sites {
			if s.Name == *input.Name && s.ID != siteID {
				RespondConflict(c, "A site with this name already exists in this tenant")
				return
			}
		}
	}

	if err := h.pg.Sites.Update(ctx, tenantID, siteID, &input); err != nil {
		if IsUniqueViolation(err) {
			RespondConflict(c, "A site with this name already exists in this tenant")
			return
		}
		h.logger.Error("update site: database error", zap.Error(err))
		RespondInternalError(c, h.logger)
		return
	}

	// Fetch updated site
	updated, err := h.pg.Sites.GetByID(ctx, tenantID, siteID)
	if err != nil {
		h.logger.Error("update site: fetch updated", zap.Error(err))
		RespondInternalError(c, h.logger)
		return
	}

	h.logger.Info("site updated",
		zap.String("site_id", siteID.String()),
		zap.String("tenant_id", tenantID.String()),
		zap.String("updated_by", middleware.GetUserID(c).String()),
	)

	RespondOK(c, updated)
}

// Delete handles DELETE /api/v1/sites/:id
func (h *SiteHandler) Delete(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)
	ctx := c.Request.Context()

	siteID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		RespondError(c, 400, "INVALID_ID", "Invalid site ID format")
		return
	}

	// Check site exists
	existing, err := h.pg.Sites.GetByID(ctx, tenantID, siteID)
	if err != nil {
		h.logger.Error("delete site: lookup", zap.Error(err))
		RespondInternalError(c, h.logger)
		return
	}
	if existing == nil {
		RespondNotFound(c, "Site")
		return
	}

	// Check if site has devices
	stats, err := h.pg.Sites.GetStats(ctx, tenantID, siteID)
	if err != nil {
		h.logger.Error("delete site: get stats", zap.Error(err))
		RespondInternalError(c, h.logger)
		return
	}
	if stats.TotalDevices > 0 {
		RespondBadRequest(c, "SITE_HAS_DEVICES",
			fmt.Sprintf("Cannot delete site with %d active devices. Move or decommission devices first.", stats.TotalDevices))
		return
	}

	if err := h.pg.Sites.Delete(ctx, tenantID, siteID); err != nil {
		h.logger.Error("delete site: database error", zap.Error(err))
		RespondInternalError(c, h.logger)
		return
	}

	h.logger.Info("site deleted",
		zap.String("site_id", siteID.String()),
		zap.String("site_name", existing.Name),
		zap.String("tenant_id", tenantID.String()),
		zap.String("deleted_by", middleware.GetUserID(c).String()),
	)

	RespondOK(c, gin.H{"message": "Site deleted"})
}

// Stats handles GET /api/v1/sites/:id/stats
func (h *SiteHandler) Stats(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)
	ctx := c.Request.Context()

	siteID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		RespondError(c, 400, "INVALID_ID", "Invalid site ID format")
		return
	}

	// Verify site exists in tenant
	site, err := h.pg.Sites.GetByID(ctx, tenantID, siteID)
	if err != nil {
		h.logger.Error("site stats: lookup", zap.Error(err))
		RespondInternalError(c, h.logger)
		return
	}
	if site == nil {
		RespondNotFound(c, "Site")
		return
	}

	stats, err := h.pg.Sites.GetStats(ctx, tenantID, siteID)
	if err != nil {
		h.logger.Error("site stats: database error", zap.Error(err))
		RespondInternalError(c, h.logger)
		return
	}

	RespondOK(c, stats)
}
