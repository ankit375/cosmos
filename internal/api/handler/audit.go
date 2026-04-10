package handler

import (
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/yourorg/cloudctrl/internal/api/middleware"
	"github.com/yourorg/cloudctrl/internal/model"
	pgstore "github.com/yourorg/cloudctrl/internal/store/postgres"
	"go.uber.org/zap"
)

// AuditHandler handles audit log query endpoints.
type AuditHandler struct {
	pg     *pgstore.Store
	logger *zap.Logger
}

// NewAuditHandler creates a new AuditHandler.
func NewAuditHandler(pg *pgstore.Store, logger *zap.Logger) *AuditHandler {
	return &AuditHandler{
		pg:     pg,
		logger: logger,
	}
}

// List handles GET /api/v1/audit
func (h *AuditHandler) List(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)
	ctx := c.Request.Context()

	params := model.AuditListParams{
		TenantID: tenantID,
	}

	// ── Parse query parameters ───────────────────────────────

	if v := c.Query("user_id"); v != "" {
		uid, err := uuid.Parse(v)
		if err != nil {
			RespondBadRequest(c, "INVALID_PARAM", "Invalid user_id format")
			return
		}
		params.UserID = &uid
	}

	if v := c.Query("action"); v != "" {
		params.Action = v
	}

	if v := c.Query("resource_type"); v != "" {
		params.ResourceType = v
	}

	if v := c.Query("resource_id"); v != "" {
		rid, err := uuid.Parse(v)
		if err != nil {
			RespondBadRequest(c, "INVALID_PARAM", "Invalid resource_id format")
			return
		}
		params.ResourceID = &rid
	}

	if v := c.Query("start"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			RespondBadRequest(c, "INVALID_PARAM", "Invalid start time format. Use RFC3339 (e.g. 2024-01-01T00:00:00Z)")
			return
		}
		params.Start = &t
	}

	if v := c.Query("end"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			RespondBadRequest(c, "INVALID_PARAM", "Invalid end time format. Use RFC3339 (e.g. 2024-01-01T00:00:00Z)")
			return
		}
		params.End = &t
	}

	// Pagination
	params.Limit = 50 // default
	if v := c.Query("limit"); v != "" {
		limit, err := strconv.Atoi(v)
		if err != nil || limit < 1 {
			RespondBadRequest(c, "INVALID_PARAM", "limit must be a positive integer")
			return
		}
		if limit > 200 {
			limit = 200
		}
		params.Limit = limit
	}

	params.Offset = 0 // default
	if v := c.Query("offset"); v != "" {
		offset, err := strconv.Atoi(v)
		if err != nil || offset < 0 {
			RespondBadRequest(c, "INVALID_PARAM", "offset must be a non-negative integer")
			return
		}
		params.Offset = offset
	}

	// ── Query ────────────────────────────────────────────────

	entries, total, err := h.pg.Audit.List(ctx, params)
	if err != nil {
		h.logger.Error("list audit log: database error", zap.Error(err))
		RespondInternalError(c, h.logger)
		return
	}

	RespondList(c, entries, total, params.Offset, params.Limit)
}
