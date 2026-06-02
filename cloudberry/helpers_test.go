package cloudberry

import (
	"context"
	"database/sql"
	"database/sql/driver"
)

// Type aliases keep the fakeExecer signature readable in the test files while
// matching the execerContext interface exactly.
type (
	ctxType    = context.Context
	resultType = sql.Result
)

func ctxBackground() context.Context { return context.Background() }

// fakeResult is a no-op sql.Result for the fakeExecer.
type fakeResult struct{}

func (fakeResult) LastInsertId() (int64, error) { return 0, nil }
func (fakeResult) RowsAffected() (int64, error) { return 0, nil }

// Compile-time check that fakeExecer satisfies execerContext.
var _ execerContext = (*fakeExecer)(nil)

// Compile-time check that the driver value types are wired correctly.
var _ driver.Result = fakeResult{}
