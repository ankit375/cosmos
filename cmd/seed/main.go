package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/google/uuid"
	"github.com/yourorg/cloudctrl/internal/config"
	"github.com/yourorg/cloudctrl/internal/model"
	pgstore "github.com/yourorg/cloudctrl/internal/store/postgres"
	"github.com/yourorg/cloudctrl/pkg/crypto"
	"github.com/yourorg/cloudctrl/pkg/logger"
	"go.uber.org/zap"
)

func main() {
	configPath := flag.String("config", "configs/controller.dev.yaml", "Path to config file")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load config: %v\n", err)
		os.Exit(1)
	}

	log, err := logger.New(cfg.Log.Level, cfg.Log.Format, cfg.Log.Output)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to init logger: %v\n", err)
		os.Exit(1)
	}
	defer log.Sync()

	ctx := context.Background()

	pg, err := pgstore.New(ctx, cfg.Database, log.Named("postgres"))
	if err != nil {
		log.Fatal("failed to connect to database", zap.Error(err))
	}
	defer pg.Close()

	// ── Seed default tenant ──────────────────────────────────
	tenantID := uuid.New()
	tenant := &model.Tenant{
		ID:          tenantID,
		Name:        "Default Organization",
		Slug:        "default",
		Subscription: "standard",
		MaxDevices:  100,
		MaxSites:    15,
		Active:      true,
	}

	log.Info("seeding default tenant", zap.String("slug", tenant.Slug))
	if err := pg.Tenants.Create(ctx, tenant); err != nil {
		// Check if already exists
		existing, lookupErr := pg.Tenants.GetBySlug(ctx, "default")
		if lookupErr != nil || existing == nil {
			log.Fatal("failed to seed tenant", zap.Error(err))
		}
		tenantID = existing.ID
		log.Info("tenant already exists", zap.String("id", tenantID.String()))
	} else {
		log.Info("tenant created", zap.String("id", tenantID.String()))
	}

	// ── Seed admin user ──────────────────────────────────────
	adminEmail := cfg.Dev.SeedAdminEmail
	adminPassword := cfg.Dev.SeedAdminPassword

	if adminEmail == "" {
		adminEmail = "admin@cloudctrl.local"
	}
	if adminPassword == "" {
		adminPassword = "admin123456"
	}

	// Check if admin already exists
	existingUser, err := pg.Users.GetByEmail(ctx, tenantID, adminEmail)
	if err != nil {
		log.Fatal("failed to check existing admin", zap.Error(err))
	}
	if existingUser != nil {
		log.Info("admin user already exists",
			zap.String("email", adminEmail),
			zap.String("id", existingUser.ID.String()),
		)
		os.Exit(0)
	}

	passwordHash, err := crypto.HashPassword(adminPassword)
	if err != nil {
		log.Fatal("failed to hash password", zap.Error(err))
	}

	adminUser := &model.User{
		ID:           uuid.New(),
		TenantID:     tenantID,
		Email:        adminEmail,
		PasswordHash: passwordHash,
		Name:         "System Admin",
		Role:         model.RoleAdmin,
		Active:       true,
	}

	if err := pg.Users.Create(ctx, adminUser); err != nil {
		log.Fatal("failed to seed admin user", zap.Error(err))
	}

	log.Info("admin user created",
		zap.String("id", adminUser.ID.String()),
		zap.String("email", adminEmail),
		zap.String("tenant_id", tenantID.String()),
	)

	fmt.Println("\n✅ Seed complete!")
	fmt.Printf("   Tenant:   %s (%s)\n", tenant.Name, tenantID)
	fmt.Printf("   Admin:    %s\n", adminEmail)
	fmt.Printf("   Password: %s\n", adminPassword)
	fmt.Println("\n⚠️  Change the admin password after first login!")
}
