package cloudberry

import (
	"context"
	"database/sql"
	"os"
	"testing"
)

// Integration tests run only when CLOUDBERRY_TEST_DSN is set, e.g.:
//
//	CLOUDBERRY_TEST_DSN="host=127.0.0.1 port=15432 user=gpadmin dbname=mydb sslmode=disable password=..." \
//	    go test ./cloudberry -run Integration -count=1 -v
func integrationDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("CLOUDBERRY_TEST_DSN")
	if dsn == "" {
		t.Skip("CLOUDBERRY_TEST_DSN not set; skipping integration test")
	}
	return dsn
}

func openDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open(DriverName, integrationDSN(t))
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	db.SetMaxOpenConns(4)
	if err := db.Ping(); err != nil {
		_ = db.Close()
		t.Fatalf("Ping: %v", err)
	}
	return db
}

func TestIntegration_Connect_And_Query(t *testing.T) {
	db := openDB(t)
	defer db.Close()

	var one int
	if err := db.QueryRow("SELECT 1").Scan(&one); err != nil {
		t.Fatalf("SELECT 1: %v", err)
	}
	if one != 1 {
		t.Fatalf("SELECT 1 = %d", one)
	}
}

func TestIntegration_DetectVersion(t *testing.T) {
	db := openDB(t)
	defer db.Close()

	v, err := DetectVersion(context.Background(), db)
	if err != nil {
		t.Fatalf("DetectVersion: %v", err)
	}
	t.Logf("server: flavor=%s version=%d.%d.%d raw=%q", v.Flavor, v.Major, v.Minor, v.Patch, v.Raw)
	if v.Flavor == FlavorUnknown {
		t.Errorf("could not detect server flavor")
	}
}

func TestIntegration_SegmentConfiguration(t *testing.T) {
	db := openDB(t)
	defer db.Close()

	v, err := DetectVersion(context.Background(), db)
	if err != nil {
		t.Fatalf("DetectVersion: %v", err)
	}
	if !v.IsMPP() {
		t.Skip("not an MPP server; skipping segment configuration test")
	}

	segs, err := GetSegmentConfiguration(context.Background(), db)
	if err != nil {
		t.Fatalf("GetSegmentConfiguration: %v", err)
	}
	if len(segs) == 0 {
		t.Fatal("expected at least one segment")
	}
	var coord, primaries int
	for _, s := range segs {
		if s.IsCoordinator() {
			coord++
		} else if s.Role == RolePrimary {
			primaries++
		}
	}
	t.Logf("segments: %d total, %d coordinator, %d primaries", len(segs), coord, primaries)
	if coord != 1 {
		t.Errorf("expected exactly one coordinator, got %d", coord)
	}
}

// TestIntegration_SerializableSnapshotDispatch reproduces the exact session
// pattern MPP backup tooling uses: a serializable session that dispatches
// pg_database_size (and a count) to the segments. With the simple-protocol
// driver this should behave like psql/libpq.
func TestIntegration_SerializableSnapshotDispatch(t *testing.T) {
	db := openDB(t)
	defer db.Close()

	ctx := context.Background()
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatalf("Conn: %v", err)
	}
	defer conn.Close()

	if err := SetSerializable(ctx, conn); err != nil {
		t.Fatalf("SetSerializable: %v", err)
	}
	var dbsize string
	if err := conn.QueryRowContext(ctx,
		"SELECT pg_size_pretty(pg_database_size(current_database())) AS dbsize").Scan(&dbsize); err != nil {
		t.Fatalf("dbsize query (segment dispatch): %v", err)
	}
	t.Logf("database size = %s", dbsize)
}

// TestIntegration_CopyToProgramOnSegment exercises the parallel per-segment
// COPY path used by backup tooling.
func TestIntegration_CopyToProgramOnSegment(t *testing.T) {
	if os.Getenv("CLOUDBERRY_TEST_COPY_TABLE") == "" {
		t.Skip("CLOUDBERRY_TEST_COPY_TABLE not set; skipping COPY ON SEGMENT test")
	}
	db := openDB(t)
	defer db.Close()

	table := os.Getenv("CLOUDBERRY_TEST_COPY_TABLE")
	err := CopyToProgramOnSegment(context.Background(), db, table,
		"cat > /tmp/cbsql_copytest_<SEGID>.dat")
	if err != nil {
		t.Fatalf("CopyToProgramOnSegment: %v", err)
	}
	t.Logf("COPY %s TO PROGRAM ON SEGMENT succeeded", table)
}
