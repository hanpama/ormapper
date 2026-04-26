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
}

type backend interface {
	LoadRows(ctx context.Context, op loadRowsOp) (rows, error)
	InsertRows(ctx context.Context, op insertOp) (rows, error)
	UpdateRows(ctx context.Context, op updateOp) (rows, error)
	DeleteRowsByKeys(ctx context.Context, op deleteRowsOp) error
	FetchQuery(ctx context.Context, stmt sqlQuery) (rows, error)
	CountQuery(ctx context.Context, stmt sqlQuery) (int64, error)
}

type loadRowsOp struct {
	schema        string
	table         string
	selectColumns []string
	keyColumns    []string
	keys          []Key
}

type insertOp struct {
	schema                            string
	table                             string
	rows                              []plannedRow
	insertColumns                     []string
	insertIndexes                     []int
	insertColumnsWithGeneratedPrimary []string
	generatedPrimaryColumns           []string
	primaryColumns                    []string
	returningColumns                  []string
	keyFromReturning                  func([]any) Key
}

type updateOp struct {
	schema           string
	table            string
	rows             []plannedRow
	rowColumns       []string
	primaryColumns   []string
	updateColumns    []string
	returningColumns []string
}

type plannedRow struct {
	index  int
	key    Key
	values []any
}

func insertValuesFromRow(insertIndexes []int, values []any) []any {
	projected := make([]any, len(insertIndexes))
	for j, idx := range insertIndexes {
		projected[j] = values[idx]
	}
	return projected
}

type deleteRowsOp struct {
	schema     string
	table      string
	keyColumns []string
	keys       []Key
}
