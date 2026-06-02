package cloudberry

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Flavor identifies the database product behind a connection.
type Flavor string

const (
	// FlavorCloudberry indicates an Apache Cloudberry (Incubating) server.
	FlavorCloudberry Flavor = "cloudberry"
	// FlavorGreenplum indicates a Greenplum Database server.
	FlavorGreenplum Flavor = "greenplum"
	// FlavorPostgres indicates a vanilla PostgreSQL server.
	FlavorPostgres Flavor = "postgres"
	// FlavorUnknown indicates the product could not be determined.
	FlavorUnknown Flavor = "unknown"
)

// VersionInfo describes the server product and version behind a connection.
type VersionInfo struct {
	// Raw is the full version() string.
	Raw string
	// Flavor is the detected product family.
	Flavor Flavor
	// Major/Minor/Patch are the parsed product version numbers. For Cloudberry
	// and Greenplum these reflect the MPP product version (e.g. 2.1.0), not the
	// embedded PostgreSQL version.
	Major int
	Minor int
	Patch int
}

// IsCloudberry reports whether the server is Apache Cloudberry.
func (v VersionInfo) IsCloudberry() bool { return v.Flavor == FlavorCloudberry }

// IsGreenplum reports whether the server is Greenplum.
func (v VersionInfo) IsGreenplum() bool { return v.Flavor == FlavorGreenplum }

// IsMPP reports whether the server is an MPP product (Cloudberry or Greenplum)
// rather than vanilla PostgreSQL.
func (v VersionInfo) IsMPP() bool { return v.IsCloudberry() || v.IsGreenplum() }

var (
	cloudberryRe = regexp.MustCompile(`(?i)Cloudberry\s+(\d+)\.(\d+)\.(\d+)`)
	greenplumRe  = regexp.MustCompile(`(?i)Greenplum Database\s+(\d+)\.(\d+)\.(\d+)`)
)

// pinger is the subset of *sql.DB / *sql.Conn used to query the version.
type queryRower interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// DetectVersion queries the server and returns its product and version. The
// querier may be a *sql.DB or a *sql.Conn.
func DetectVersion(ctx context.Context, q queryRower) (VersionInfo, error) {
	var raw string
	if err := q.QueryRowContext(ctx, "SELECT version()").Scan(&raw); err != nil {
		return VersionInfo{}, fmt.Errorf("cloudberry: detecting version: %w", err)
	}
	return parseVersion(raw), nil
}

// parseVersion parses a version() string into a VersionInfo.
func parseVersion(raw string) VersionInfo {
	info := VersionInfo{Raw: raw, Flavor: FlavorUnknown}
	if m := cloudberryRe.FindStringSubmatch(raw); m != nil {
		info.Flavor = FlavorCloudberry
		info.Major, info.Minor, info.Patch = atoi(m[1]), atoi(m[2]), atoi(m[3])
		return info
	}
	if m := greenplumRe.FindStringSubmatch(raw); m != nil {
		info.Flavor = FlavorGreenplum
		info.Major, info.Minor, info.Patch = atoi(m[1]), atoi(m[2]), atoi(m[3])
		return info
	}
	if strings.Contains(strings.ToLower(raw), "postgresql") {
		info.Flavor = FlavorPostgres
	}
	return info
}

func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

// SegmentRole identifies a segment's role in the cluster.
type SegmentRole string

const (
	// RolePrimary is a primary segment (or the coordinator, content -1).
	RolePrimary SegmentRole = "p"
	// RoleMirror is a mirror segment.
	RoleMirror SegmentRole = "m"
)

// Segment describes one entry from gp_segment_configuration.
type Segment struct {
	Content       int
	Role          SegmentRole
	PreferredRole SegmentRole
	Status        string // "u" (up) or "d" (down)
	Mode          string // "s" (synced) or "n" (not in sync)
	Hostname      string
	Address       string
	Port          int
	DataDir       string
}

// IsCoordinator reports whether this segment entry is the coordinator (content -1).
func (s Segment) IsCoordinator() bool { return s.Content == -1 }

// IsUp reports whether the segment is up.
func (s Segment) IsUp() bool { return s.Status == "u" }

// GetSegmentConfiguration returns the cluster segment configuration from
// gp_segment_configuration. It requires an MPP (Cloudberry/Greenplum) server.
func GetSegmentConfiguration(ctx context.Context, db *sql.DB) ([]Segment, error) {
	const q = `SELECT content, role, preferred_role, status, mode,
		hostname, address, port, datadir
		FROM gp_segment_configuration
		ORDER BY content, role`
	rows, err := db.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("cloudberry: querying gp_segment_configuration: %w", err)
	}
	defer rows.Close()

	var segs []Segment
	for rows.Next() {
		var s Segment
		if err := rows.Scan(&s.Content, &s.Role, &s.PreferredRole, &s.Status,
			&s.Mode, &s.Hostname, &s.Address, &s.Port, &s.DataDir); err != nil {
			return nil, fmt.Errorf("cloudberry: scanning segment row: %w", err)
		}
		segs = append(segs, s)
	}
	return segs, rows.Err()
}

// SetSerializable sets the session's transaction isolation to serializable.
// On Cloudberry/Greenplum the server transparently falls back to repeatable
// read (serializable is not yet supported); this helper hides that detail and
// is a no-op-equivalent on those servers.
func SetSerializable(ctx context.Context, execer execerContext) error {
	if _, err := execer.ExecContext(ctx, "SET transaction_isolation TO 'serializable'"); err != nil {
		return fmt.Errorf("cloudberry: setting serializable isolation: %w", err)
	}
	return nil
}

// execerContext is the subset of *sql.DB / *sql.Conn / *sql.Tx used for Exec.
type execerContext interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// ExportSnapshot exports a transaction snapshot id from tx, for use by sibling
// connections that want a consistent view via SetTransactionSnapshot. tx must
// already be in an open transaction.
func ExportSnapshot(ctx context.Context, tx *sql.Tx) (string, error) {
	var snap string
	if err := tx.QueryRowContext(ctx, "SELECT pg_export_snapshot()").Scan(&snap); err != nil {
		return "", fmt.Errorf("cloudberry: exporting snapshot: %w", err)
	}
	return snap, nil
}

// SetTransactionSnapshot makes tx use the snapshot exported by ExportSnapshot,
// giving it the same consistent view. tx must be a freshly-begun transaction.
func SetTransactionSnapshot(ctx context.Context, tx *sql.Tx, snapshotID string) error {
	// The snapshot id is a server-generated token (digits and hyphens); reject
	// anything else to avoid injection into this non-parameterizable statement.
	if !snapshotIDRe.MatchString(snapshotID) {
		return fmt.Errorf("cloudberry: invalid snapshot id %q", snapshotID)
	}
	stmt := fmt.Sprintf("SET TRANSACTION SNAPSHOT '%s'", snapshotID)
	if _, err := tx.ExecContext(ctx, stmt); err != nil {
		return fmt.Errorf("cloudberry: setting transaction snapshot: %w", err)
	}
	return nil
}

var snapshotIDRe = regexp.MustCompile(`^[0-9A-Fa-f]+-[0-9A-Fa-f]+-[0-9A-Fa-f]+$`)

// CopyToProgramOnSegment runs a parallel, per-segment COPY that pipes each
// segment's slice of the table to an external program. The program string must
// contain the literal token <SEGID>, which Cloudberry substitutes with each
// segment's content id at execution time (this is the standard gpbackup data
// path). table should be a schema-qualified, already-quoted identifier.
//
// Example:
//
//	CopyToProgramOnSegment(ctx, db, "public.customers",
//	    "cat > /backup/seg_<SEGID>.dat")
func CopyToProgramOnSegment(ctx context.Context, execer execerContext, table, program string) error {
	if !strings.Contains(program, "<SEGID>") {
		return fmt.Errorf("cloudberry: program for ON SEGMENT copy must contain <SEGID>")
	}
	stmt := fmt.Sprintf("COPY %s TO PROGRAM %s ON SEGMENT", table, quoteLiteral(program))
	if _, err := execer.ExecContext(ctx, stmt); err != nil {
		return fmt.Errorf("cloudberry: COPY TO PROGRAM ON SEGMENT: %w", err)
	}
	return nil
}

// CopyFromProgramOnSegment runs a parallel, per-segment COPY that loads each
// segment's slice of the table from an external program. As with
// CopyToProgramOnSegment, program must contain the <SEGID> token. This is the
// gprestore data path.
func CopyFromProgramOnSegment(ctx context.Context, execer execerContext, table, program string) error {
	if !strings.Contains(program, "<SEGID>") {
		return fmt.Errorf("cloudberry: program for ON SEGMENT copy must contain <SEGID>")
	}
	stmt := fmt.Sprintf("COPY %s FROM PROGRAM %s ON SEGMENT", table, quoteLiteral(program))
	if _, err := execer.ExecContext(ctx, stmt); err != nil {
		return fmt.Errorf("cloudberry: COPY FROM PROGRAM ON SEGMENT: %w", err)
	}
	return nil
}

// quoteLiteral single-quotes and escapes a string literal for inclusion in SQL.
func quoteLiteral(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}
