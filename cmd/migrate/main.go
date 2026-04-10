package main

import (
	"flag"
	"fmt"
	"os"
	"strconv"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/yourorg/cloudctrl/internal/config"
)

func main() {
	configPath := flag.String("config", "configs/controller.dev.yaml", "Path to configuration file")
	direction := flag.String("direction", "up", "Migration direction: up, down, force, version")
	steps := flag.Int("steps", 0, "Number of steps (0 = all)")
	forceVersion := flag.Int("force-version", -1, "Force migration version (use with -direction=force)")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load config: %v\n", err)
		os.Exit(1)
	}

	dsn := cfg.Database.DSN()
	migrationsPath := "file://internal/store/postgres/migrations"

	m, err := migrate.New(migrationsPath, dsn)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create migrate instance: %v\n", err)
		os.Exit(1)
	}
	defer m.Close()

	switch *direction {
	case "up":
		if *steps > 0 {
			err = m.Steps(*steps)
		} else {
			err = m.Up()
		}
	case "down":
		if *steps > 0 {
			err = m.Steps(-(*steps))
		} else {
			err = m.Down()
		}
	case "force":
		if *forceVersion < 0 {
			fmt.Fprintf(os.Stderr, "Must specify -force-version with -direction=force\n")
			os.Exit(1)
		}
		err = m.Force(*forceVersion)
	case "version":
		version, dirty, verErr := m.Version()
		if verErr != nil {
			fmt.Fprintf(os.Stderr, "Error getting version: %v\n", verErr)
			os.Exit(1)
		}
		fmt.Printf("Version: %d, Dirty: %v\n", version, dirty)
		return
	default:
		fmt.Fprintf(os.Stderr, "Unknown direction: %s\n", *direction)
		os.Exit(1)
	}

	if err != nil && err != migrate.ErrNoChange {
		fmt.Fprintf(os.Stderr, "Migration error: %v\n", err)
		os.Exit(1)
	}

	if err == migrate.ErrNoChange {
		fmt.Println("No migrations to apply")
	} else {
		version, _, _ := m.Version()
		fmt.Printf("Migrations applied successfully. Current version: %s\n", strconv.FormatUint(uint64(version), 10))
	}
}