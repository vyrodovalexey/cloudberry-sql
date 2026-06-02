package cloudberry

import (
	"database/sql"
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestDriverRegistered(t *testing.T) {
	found := false
	for _, name := range sql.Drivers() {
		if name == DriverName {
			found = true
		}
	}
	if !found {
		t.Fatalf("driver %q not registered; have %v", DriverName, sql.Drivers())
	}
}

func TestOpenConnector_ValidDSN(t *testing.T) {
	d := &Driver{}
	c, err := d.OpenConnector("host=localhost port=5432 user=gpadmin dbname=mydb sslmode=disable")
	if err != nil {
		t.Fatalf("OpenConnector: %v", err)
	}
	if c == nil {
		t.Fatal("OpenConnector returned nil connector")
	}
	if c.Driver() != d {
		t.Fatal("connector.Driver did not return the originating driver")
	}
}

func TestOpenConnector_InvalidDSN(t *testing.T) {
	d := &Driver{}
	if _, err := d.OpenConnector("=not a valid dsn="); err == nil {
		t.Fatal("expected error for invalid DSN, got nil")
	}
}

func TestApplyCloudberryConnConfig(t *testing.T) {
	cfg, err := pgx.ParseConfig("host=localhost user=gpadmin dbname=mydb")
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	// Sanity: pgx default is the extended protocol; our config must change it.
	applyCloudberryConnConfig(cfg)

	if cfg.DefaultQueryExecMode != pgx.QueryExecModeSimpleProtocol {
		t.Errorf("DefaultQueryExecMode = %v, want simple protocol", cfg.DefaultQueryExecMode)
	}
	if cfg.StatementCacheCapacity != 0 {
		t.Errorf("StatementCacheCapacity = %d, want 0", cfg.StatementCacheCapacity)
	}
	if cfg.DescriptionCacheCapacity != 0 {
		t.Errorf("DescriptionCacheCapacity = %d, want 0", cfg.DescriptionCacheCapacity)
	}
}

func TestNewConnectorAndOpen(t *testing.T) {
	c, err := NewConnector("host=localhost user=gpadmin dbname=mydb sslmode=disable")
	if err != nil {
		t.Fatalf("NewConnector: %v", err)
	}
	if c == nil {
		t.Fatal("NewConnector returned nil")
	}

	db, err := Open("host=localhost user=gpadmin dbname=mydb sslmode=disable")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if db == nil {
		t.Fatal("Open returned nil *sql.DB")
	}
	_ = db.Close()
}

func TestOpen_InvalidDSN(t *testing.T) {
	if _, err := Open("=bad="); err == nil {
		t.Fatal("expected error for invalid DSN")
	}
}
