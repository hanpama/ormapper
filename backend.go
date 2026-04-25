package ormapper

import (
	"context"
	"database/sql"
	"reflect"
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
	SelectExistingKeys(ctx context.Context, op keyScanOp) ([]Key, error)
	InsertRows(ctx context.Context, op saveRowsOp, rows []plannedRow) ([]savedRow, error)
	UpdateRows(ctx context.Context, op saveRowsOp, rows []plannedRow) ([]savedRow, error)
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

type keyScanOp struct {
	schema     string
	table      string
	keyColumns []string
	keyTypes   []reflect.Type
	keys       []Key
}

type saveRowsOp struct {
	schema string
	table  string
	layout *saveRowsLayout
}

type saveRow struct {
	values []any
}

type plannedRow struct {
	index int
	key   Key
	row   saveRow
}

type savedRow struct {
	index  int
	values []any
}

type saveRowsLayout struct {
	rowColumns                        []string
	insertColumns                     []string
	insertIndexes                     []int
	insertColumnsWithGeneratedPrimary []string
	updateColumns                     []string
	primaryColumns                    []string
	primaryIndexes                    []int
	primaryTypes                      []reflect.Type
	generatedPrimaryColumns           []string
	generatedPrimaryIndexes           []int
	returningColumns                  []string
	primaryReturningIndexes           []int
}

type deleteRowsOp struct {
	schema     string
	table      string
	keyColumns []string
	keys       []Key
}
