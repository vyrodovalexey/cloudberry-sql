# cloudberry-sql

A `database/sql` driver for [Apache Cloudberry (Incubating)](https://cloudberry.apache.org/)
and Greenplum-derived MPP databases.

The driver wraps the [`pgx/v5`](https://github.com/jackc/pgx) `stdlib` driver but
configures the connection to behave like the reference C client (libpq / `psql`):

- forces the PostgreSQL **simple (text) query protocol** for all queries, and
- disables server-side **prepared-statement / description caching**.

## Why

Cloudberry coordinates work across segment backends. Some Cloudberry server
builds mishandle the PostgreSQL **extended** query protocol (Parse/Bind/Execute
with cached prepared statements) when commands are dispatched to segments. The
C client `libpq` (used by `psql`) issues statements through the simple query
protocol and does not cache prepared statements. This driver mirrors that
behaviour so Go MPP tooling (e.g. `gpbackup`/`gprestore`) talks to segments the
same way `psql` does.

## Install

```bash
go get github.com/vyrodovalexey/cloudberry-sql/cloudberry
```

## Usage

```go
import (
    "database/sql"

    _ "github.com/vyrodovalexey/cloudberry-sql/cloudberry"
)

db, err := sql.Open("cloudberry",
    "host=localhost port=5432 user=gpadmin dbname=mydb sslmode=disable")
```

The driver registers under the name `cloudberry`, and also under `pgx` (for
drop-in compatibility with tooling that hard-codes the pgx driver name) when
that name is not already taken.

### Cloudberry helpers

The `cloudberry` package also exposes MPP-aware helpers:

| Helper | Purpose |
|---|---|
| `DetectVersion` | Detect product flavor (Cloudberry / Greenplum / Postgres) and version |
| `GetSegmentConfiguration` | Read `gp_segment_configuration` |
| `SetSerializable` | Request serializable isolation (server falls back to repeatable read on MPP) |
| `ExportSnapshot` / `SetTransactionSnapshot` | Synchronize a consistent snapshot across sibling connections |
| `CopyToProgramOnSegment` / `CopyFromProgramOnSegment` | Parallel per-segment `COPY ... TO/FROM PROGRAM ... ON SEGMENT` (the gpbackup/gprestore data path; the program string must contain the `<SEGID>` token) |

## Testing

Unit tests run with no database:

```bash
go test ./...
```

Integration tests run against a live cluster when `CLOUDBERRY_TEST_DSN` is set:

```bash
CLOUDBERRY_TEST_DSN="host=127.0.0.1 port=5432 user=gpadmin dbname=mydb sslmode=disable password=..." \
CLOUDBERRY_TEST_COPY_TABLE="public.customers" \
    go test ./cloudberry -run Integration -count=1 -v
```

## License

Apache License 2.0.
