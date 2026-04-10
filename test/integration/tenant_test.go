
//go:build integration

package integration

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yourorg/cloudctrl/internal/model"
	"github.com/yourorg/cloudctrl/internal/store/postgres"
)

func TestTenantStore(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	store := postgres.NewTenantStore(testPG.Pool)

	t.Run("create and get tenant", func(t *testing.T) {
		tenant := &model.Tenant{
			ID:           uuid.New(),
			Name:         "Test Organization",
			Slug:         "test-org-" + uuid.New().String()[:8],
			Subscription: "enterprise",
			MaxDevices:   1000,
			MaxSites:     15,
			Settings:     map[string]interface{}{},
			Active:       true,
		}

		err := store.Create(ctx, tenant)
		require.NoError(t, err)

		// Get by ID
		got, err := store.GetByID(ctx, tenant.ID)
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, tenant.Name, got.Name)
		assert.Equal(t, tenant.Slug, got.Slug)
		assert.Equal(t, tenant.MaxDevices, got.MaxDevices)
		assert.True(t, got.Active)

		// Get by slug
		got, err = store.GetBySlug(ctx, tenant.Slug)
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, tenant.ID, got.ID)
	})

	t.Run("update tenant", func(t *testing.T) {
		tenant := &model.Tenant{
			ID:           uuid.New(),
			Name:         "Update Test Org",
			Slug:         "update-test-" + uuid.New().String()[:8],
			Subscription: "standard",
			MaxDevices:   100,
			MaxSites:     5,
			Settings:     map[string]interface{}{},
			Active:       true,
		}
		require.NoError(t, store.Create(ctx, tenant))

		newName := "Updated Name"
		newMax := 500
		err := store.Update(ctx, tenant.ID, &model.UpdateTenantInput{
			Name:       &newName,
			MaxDevices: &newMax,
		})
		require.NoError(t, err)

		got, err := store.GetByID(ctx, tenant.ID)
		require.NoError(t, err)
		assert.Equal(t, "Updated Name", got.Name)
		assert.Equal(t, 500, got.MaxDevices)
	})

	t.Run("list tenants", func(t *testing.T) {
		tenants, err := store.List(ctx)
		require.NoError(t, err)
		assert.True(t, len(tenants) >= 2) // We created 2 above
	})

	t.Run("get non-existent tenant returns nil", func(t *testing.T) {
		got, err := store.GetByID(ctx, uuid.New())
		require.NoError(t, err)
		assert.Nil(t, got)
	})

	t.Run("delete tenant", func(t *testing.T) {
		tenant := &model.Tenant{
			ID:           uuid.New(),
			Name:         "Delete Test",
			Slug:         "delete-test-" + uuid.New().String()[:8],
			Subscription: "standard",
			MaxDevices:   10,
			MaxSites:     1,
			Settings:     map[string]interface{}{},
			Active:       true,
		}
		require.NoError(t, store.Create(ctx, tenant))
		require.NoError(t, store.Delete(ctx, tenant.ID))

		got, err := store.GetByID(ctx, tenant.ID)
		require.NoError(t, err)
		assert.Nil(t, got)
	})
}
