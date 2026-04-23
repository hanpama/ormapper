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
	Select(ctx context.Context, op selectOp) (rows, error)
	Insert(ctx context.Context, op insertOp) (rows, error)
	Upsert(ctx context.Context, op upsertOp) (rows, error)
	Update(ctx context.Context, op updateOp) error
	Delete(ctx context.Context, op deleteOp) error
	FetchQuery(ctx context.Context, stmt sqlQuery) (rows, error)
	CountQuery(ctx context.Context, stmt sqlQuery) (int64, error)
}

// Dialect selects the SQL dialect used by a Mapper.
// Use one of the built-in values such as Postgres or SQLite.
type Dialect interface {
	newBackend(db DBTX) backend
}

type postgresDialect struct{}
type sqliteDialect struct{}

// Postgres renders PostgreSQL SQL.
var Postgres Dialect = postgresDialect{}

// SQLite renders SQLite SQL.
var SQLite Dialect = sqliteDialect{}

func (postgresDialect) newBackend(db DBTX) backend { return newPostgreSQLBackend(db) }
func (sqliteDialect) newBackend(db DBTX) backend   { return newSQLiteBackend(db) }
