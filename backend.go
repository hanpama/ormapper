package ormapper

import (
	"context"
	"database/sql"
)

// DBTX is the minimal database handle required by Mapper.
// Both *sql.DB and *sql.Tx satisfy this interface, so callers usually pass one
// of those values directly.
type DBTX interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// Dialect selects the SQL dialect used by a Mapper.
// Use one of the built-in values such as Postgres or SQLite.
type Dialect interface {
	newBackend(db DBTX) backend
}

type rows interface {
	Next() bool
	Scan(dest ...any) error
	Close() error
	Columns() ([]string, error)
}

type emptyRows struct{}

func (e *emptyRows) Next() bool                 { return false }
func (e *emptyRows) Scan(dest ...any) error     { return nil }
func (e *emptyRows) Close() error               { return nil }
func (e *emptyRows) Columns() ([]string, error) { return nil, nil }

type backend interface {
	LoadByKeys(ctx context.Context, op loadRowsOp) (rows, error)
	LoadByParentKeys(ctx context.Context, op loadRowsOp) (rows, error)
	SelectExistingKeys(ctx context.Context, op keyScanOp) ([]Key, error)
	InsertRows(ctx context.Context, op saveRowsOp, rows []plannedRow) ([]savedRow, error)
	UpdateRows(ctx context.Context, op saveRowsOp, rows []plannedRow) ([]savedRow, error)
	SelectMissingChildren(ctx context.Context, op selectMissingChildrenOp) ([]Key, error)
	DeleteRowsByKeys(ctx context.Context, op deleteRowsOp) error
	FetchQuery(ctx context.Context, stmt sqlQuery) (rows, error)
	CountQuery(ctx context.Context, stmt sqlQuery) (int64, error)
}
