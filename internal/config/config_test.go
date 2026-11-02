package config_test

import (
	"testing"

	"github.com/2017fighting/oh-my-mdc/internal/config"
)

func TestLoadDefaultsWhenEnvIsEmpty(t *testing.T) {
	t.Setenv("MDC_DATABASE_URL", "")
	t.Setenv("MDC_LISTEN", "")
	t.Setenv("MDC_DB_PASSWORD", "")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if got, want := cfg.DatabaseURL, config.DefaultDatabaseURL; got != want {
		t.Errorf("DatabaseURL = %q, want %q", got, want)
	}
	if got, want := cfg.Listen, ":8080"; got != want {
		t.Errorf("Listen = %q, want %q", got, want)
	}
}

func TestLoadTakesDatabaseURLAndListenFromEnv(t *testing.T) {
	t.Setenv("MDC_DATABASE_URL", "postgres://u:p@db:5432/x?sslmode=require")
	t.Setenv("MDC_LISTEN", "127.0.0.1:9999")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if got, want := cfg.DatabaseURL, "postgres://u:p@db:5432/x?sslmode=require"; got != want {
		t.Errorf("DatabaseURL = %q, want %q", got, want)
	}
	if got, want := cfg.Listen, "127.0.0.1:9999"; got != want {
		t.Errorf("Listen = %q, want %q", got, want)
	}
}

func TestLoadSplicesPasswordEnvIntoDSN(t *testing.T) {
	t.Setenv("MDC_DATABASE_URL", "postgres://oh_my_mdc@pg.example:5432/oh_my_mdc?sslmode=disable")
	t.Setenv("MDC_DB_PASSWORD", "s3cr3t")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	want := "postgres://oh_my_mdc:s3cr3t@pg.example:5432/oh_my_mdc?sslmode=disable"
	if cfg.DatabaseURL != want {
		t.Errorf("DatabaseURL = %q, want %q", cfg.DatabaseURL, want)
	}
}

func TestLoadRejectsUnparsableDatabaseURL(t *testing.T) {
	t.Setenv("MDC_DATABASE_URL", "postgres://bad host/db")

	if _, err := config.Load(); err == nil {
		t.Fatal("Load() error = nil, want error naming the unparsable MDC_DATABASE_URL")
	}
}
