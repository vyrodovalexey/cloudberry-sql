// Package cloudberry provides a database/sql driver for Apache Cloudberry
// (Incubating) and Greenplum-derived MPP databases.
//
// The driver wraps the pgx/v5 stdlib driver but configures the connection to
// behave like the C libpq client (psql): it forces the PostgreSQL *simple*
// (text) query protocol and disables server-side prepared-statement caching.
//
// Motivation
//
// Cloudberry coordinates work across segment backends. Some Cloudberry server
// builds mishandle the PostgreSQL *extended* query protocol (Parse/Bind/Execute
// with cached prepared statements) when commands are dispatched to segments,
// which can terminate a segment backend. The reference C client (libpq, used by
// psql) issues statements through the simple query protocol and does not cache
// prepared statements, and does not trigger that behaviour. This driver mirrors
// that behaviour so MPP tooling written in Go (e.g. gpbackup/gprestore) talks to
// segments the same way psql does.
//
// Usage
//
//	import (
//	    "database/sql"
//	    _ "github.com/vyrodovalexey/cloudberry-sql/cloudberry"
//	)
//
//	db, err := sql.Open("cloudberry", "host=localhost port=5432 user=gpadmin dbname=mydb sslmode=disable")
//
// The driver is registered under the names "cloudberry" and (for drop-in
// compatibility with tools that hard-code the pgx driver name) "pgx".
package cloudberry

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

// DriverName is the primary name this driver registers under.
const DriverName = "cloudberry"

// CompatDriverName is an additional name registered for drop-in compatibility
// with tooling (such as cloudberry-go-libs / gpbackup) that opens the database
// using the historical "pgx" driver name.
const CompatDriverName = "pgx"

// Driver is the database/sql driver for Cloudberry. It implements
// driver.Driver and driver.DriverContext.
type Driver struct{}

var (
	_ driver.Driver        = (*Driver)(nil)
	_ driver.DriverContext = (*Driver)(nil)
)

func init() {
	d := &Driver{}
	sql.Register(DriverName, d)
	// Register under the compatibility name too. sql.Register panics if the
	// name is already taken (e.g. the real pgx stdlib driver was imported),
	// so guard the compatibility registration.
	registerCompat(d)
}

// registerCompat registers the driver under CompatDriverName, ignoring a
// panic if that name is already registered by another driver.
func registerCompat(d *Driver) {
	defer func() { _ = recover() }()
	for _, name := range sql.Drivers() {
		if name == CompatDriverName {
			return
		}
	}
	sql.Register(CompatDriverName, d)
}

// Open returns a new connection to the database using a DSN. It satisfies
// driver.Driver. The DSN accepts the standard libpq keyword/value or URL
// connection-string formats understood by pgx.
func (d *Driver) Open(dsn string) (driver.Conn, error) {
	connector, err := d.OpenConnector(dsn)
	if err != nil {
		return nil, err
	}
	return connector.Connect(context.Background())
}

// OpenConnector parses the DSN and returns a driver.Connector configured for
// Cloudberry-safe (simple-protocol, no statement cache) connections.
func (d *Driver) OpenConnector(dsn string) (driver.Connector, error) {
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("cloudberry: parsing DSN: %w", err)
	}
	applyCloudberryConnConfig(cfg)
	return &connector{
		driver: d,
		config: cfg,
	}, nil
}

// applyCloudberryConnConfig mutates a pgx ConnConfig so the connection behaves
// like libpq/psql: simple (text) query protocol and no prepared-statement
// caching. This is the core of the Cloudberry compatibility behaviour.
func applyCloudberryConnConfig(cfg *pgx.ConnConfig) {
	// Force the simple (text) query protocol for all queries. This makes the
	// wire traffic match what psql/libpq sends, avoiding the extended-protocol
	// path that some Cloudberry server builds mishandle when dispatching to
	// segments.
	cfg.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol

	// Disable automatic prepared-statement caching. Cached statements rely on
	// the extended protocol and can produce cache-lookup failures on Cloudberry
	// (the catalog/snapshot a cached plan was built against can change between
	// dispatches). "describe" caching also issues a Parse, so disable both.
	cfg.StatementCacheCapacity = 0
	cfg.DescriptionCacheCapacity = 0
}

// connector implements driver.Connector for the Cloudberry driver.
type connector struct {
	driver *Driver
	config *pgx.ConnConfig
}

var _ driver.Connector = (*connector)(nil)

// Connect opens a new Cloudberry connection. The returned driver.Conn is the
// pgx stdlib connection (which already implements the full set of database/sql
// optional interfaces, including driver.Pinger and the COPY support exposed via
// pgx). The connection has been configured for simple-protocol operation.
func (c *connector) Connect(ctx context.Context) (driver.Conn, error) {
	// Clone the config so concurrent Connect calls do not share mutable state.
	cfg := c.config.Copy()
	applyCloudberryConnConfig(cfg)
	conn, err := stdlib.GetConnector(*cfg).Connect(ctx)
	if err != nil {
		return nil, err
	}
	return conn, nil
}

// Driver returns the underlying driver.
func (c *connector) Driver() driver.Driver { return c.driver }

// NewConnector builds a driver.Connector for the given DSN. It is a convenience
// wrapper around sql.OpenDB for callers that want to construct a *sql.DB
// directly without registering/looking up the driver by name.
func NewConnector(dsn string) (driver.Connector, error) {
	return (&Driver{}).OpenConnector(dsn)
}

// Open is a convenience helper that returns a *sql.DB backed by this driver.
func Open(dsn string) (*sql.DB, error) {
	connector, err := NewConnector(dsn)
	if err != nil {
		return nil, err
	}
	return sql.OpenDB(connector), nil
}
