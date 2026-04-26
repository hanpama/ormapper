package ormapper

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"sort"
	"strconv"
)

type bufferedRows struct {
	data   [][]any
	cursor int
}

func (r *bufferedRows) Next() bool {
	r.cursor++
	return r.cursor <= len(r.data)
}

func (r *bufferedRows) Scan(dest ...any) error {
	row := r.data[r.cursor-1]
	for i, d := range dest {
		dv := reflect.ValueOf(d).Elem()
		val := row[i]
		if val == nil {
			dv.Set(reflect.Zero(dv.Type()))
			continue
		}
		sv := reflect.ValueOf(val)
		if sv.Type().AssignableTo(dv.Type()) {
			dv.Set(sv)
		} else if sv.Type().ConvertibleTo(dv.Type()) {
			dv.Set(sv.Convert(dv.Type()))
		} else {
			dv.Set(sv)
		}
	}
	return nil
}

func (r *bufferedRows) Close() error { return nil }

func scanRowValues(rowSet rows, values []any, dest []any) error {
	for i := range values {
		dest[i] = &values[i]
	}
	if err := rowSet.Scan(dest...); err != nil {
		return err
	}
	for i, value := range values {
		values[i] = normalizeScannedValue(value)
	}
	return nil
}

func int64FromDB(value any) (int64, error) {
	const maxInt64 = int64(^uint64(0) >> 1)
	switch v := value.(type) {
	case int:
		return int64(v), nil
	case int8:
		return int64(v), nil
	case int16:
		return int64(v), nil
	case int32:
		return int64(v), nil
	case int64:
		return v, nil
	case uint:
		if uint64(v) > uint64(maxInt64) {
			return 0, fmt.Errorf("integer value %d overflows int64", v)
		}
		return int64(v), nil
	case uint8:
		return int64(v), nil
	case uint16:
		return int64(v), nil
	case uint32:
		return int64(v), nil
	case uint64:
		if v > uint64(maxInt64) {
			return 0, fmt.Errorf("integer value %d overflows int64", v)
		}
		return int64(v), nil
	default:
		return 0, fmt.Errorf("expected integer value, got %T", value)
	}
}

type sqliteDialect struct{}

func (sqliteDialect) newBackend(db DBTX) backend { return newSQLiteBackend(db) }

// sqliteBackend implements backend for SQLite databases.
type sqliteBackend struct {
	db                      DBTX
	paramIndex              int
	argsBuffer              []any  // Reusable buffer for query parameters
	sqlBuffer               []byte // Reusable buffer for SQL generation
	sqliteSequenceChecked   bool
	sqliteSequenceAvailable bool
}

// newSQLiteBackend creates a new SQLite backend.
func newSQLiteBackend(db DBTX) backend {
	return &sqliteBackend{
		db: db,
	}
}

// resetArgsBuffer resets the argument buffer to length 0, growing capacity if needed.
// Uses 2x growth factor to reduce reallocation frequency.
func (b *sqliteBackend) resetArgsBuffer(minCapacity int) {
	if cap(b.argsBuffer) < minCapacity {
		b.argsBuffer = make([]any, 0, minCapacity*2)
	} else {
		b.argsBuffer = b.argsBuffer[:0]
	}
}

// resetSQLBuffer resets the SQL buffer to length 0, growing capacity if needed.
// Uses 2x growth factor to reduce reallocation frequency.
func (b *sqliteBackend) resetSQLBuffer(minCapacity int) {
	b.sqlBuffer = b.sqlBuffer[:0]
	if cap(b.sqlBuffer) < minCapacity {
		b.sqlBuffer = make([]byte, 0, minCapacity*2)
	}
}

// writeString appends a string to the SQL buffer.
func (b *sqliteBackend) writeString(s string) {
	b.sqlBuffer = append(b.sqlBuffer, s...)
}

// writeByte appends a byte to the SQL buffer.
func (b *sqliteBackend) writeByte(c byte) {
	b.sqlBuffer = append(b.sqlBuffer, c)
}

// quoteIdentifier quotes a SQL identifier by escaping " as "" and wrapping in "
func (b *sqliteBackend) quoteIdentifier(identifier string) {
	b.writeByte('"')
	for i := 0; i < len(identifier); i++ {
		if identifier[i] == '"' {
			b.writeString(`""`)
		} else {
			b.writeByte(identifier[i])
		}
	}
	b.writeByte('"')
}

// sqlString returns the current SQL buffer as a string.
func (b *sqliteBackend) sqlString() string {
	return string(b.sqlBuffer)
}

// queryContext executes a query using the provided database handle.
func (b *sqliteBackend) queryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return b.db.QueryContext(ctx, query, args...)
}

// execContext executes a command using the provided database handle.
func (b *sqliteBackend) execContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return b.db.ExecContext(ctx, query, args...)
}

// queryRowContext executes a query that returns at most one row using the provided database handle.
func (b *sqliteBackend) queryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return b.db.QueryRowContext(ctx, query, args...)
}

func (b *sqliteBackend) hasSQLiteSequence(ctx context.Context) (bool, error) {
	if b.sqliteSequenceChecked {
		return b.sqliteSequenceAvailable, nil
	}

	var exists int
	if err := b.queryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = 'sqlite_sequence')`).Scan(&exists); err != nil {
		return false, err
	}
	b.sqliteSequenceChecked = true
	b.sqliteSequenceAvailable = exists != 0
	return b.sqliteSequenceAvailable, nil
}

func (b *sqliteBackend) renderSQL(sql sqlNode, args *[]any) {
	switch v := sql.(type) {
	case sqlN:
		b.quoteIdentifier(v.part)
	case sqlQN:
		if v.part1 == "" {
			b.quoteIdentifier(v.part2)
		} else {
			b.quoteIdentifier(v.part1)
			b.writeByte('.')
			b.quoteIdentifier(v.part2)
		}
	case sqlText:
		b.writeString(v.text)
	case sqlParam:
		b.paramIndex++
		*args = append(*args, v.value)
		b.writeByte('?')
	case sqlAll:
		b.writeByte('(')
		for i, el := range v.els {
			if i > 0 {
				b.writeString(" AND ")
			}
			b.renderSQL(el, args)
		}
		b.writeByte(')')
	case sqlAny:
		b.writeByte('(')
		for i, el := range v.els {
			if i > 0 {
				b.writeString(" OR ")
			}
			b.renderSQL(el, args)
		}
		b.writeByte(')')
	case sqlEq:
		b.renderSQL(v.left, args)
		b.writeString(" = ")
		b.renderSQL(v.right, args)
	case sqlLt:
		b.renderSQL(v.left, args)
		b.writeString(" < ")
		b.renderSQL(v.right, args)
	case sqlGt:
		b.renderSQL(v.left, args)
		b.writeString(" > ")
		b.renderSQL(v.right, args)
	case sqlIsNull:
		b.renderSQL(v.operand, args)
		b.writeString(" IS NULL")
	case sqlIsNotNull:
		b.renderSQL(v.operand, args)
		b.writeString(" IS NOT NULL")
	case sqlFragment:
		for _, el := range v.els {
			b.renderSQL(el, args)
		}
	}
}

func (b *sqliteBackend) renderSQLQuery(stmt sqlQuery) (string, []any) {
	b.paramIndex = 0
	b.resetArgsBuffer(32)
	b.resetSQLBuffer(512)

	if len(stmt.selectColumns) > 0 {
		b.writeString("SELECT ")
		for i, sel := range stmt.selectColumns {
			if i > 0 {
				b.writeString(", ")
			}
			b.renderSQL(sel, &b.argsBuffer)
		}
	}

	b.writeString(" FROM ")
	b.renderSQL(stmt.fromTable, &b.argsBuffer)
	b.writeString(" AS ")
	b.renderSQL(stmt.fromAlias, &b.argsBuffer)

	for _, join := range stmt.joins {
		b.writeByte(' ')
		b.writeString(join.typ)
		b.writeByte(' ')
		b.renderSQL(join.table, &b.argsBuffer)
		b.writeString(" AS ")
		b.renderSQL(join.alias, &b.argsBuffer)
		b.writeString(" ON ")
		b.renderSQL(join.on, &b.argsBuffer)
	}

	if stmt.where != nil {
		b.writeString(" WHERE ")
		b.renderSQL(*stmt.where, &b.argsBuffer)
	}

	if len(stmt.groupBy) > 0 {
		b.writeString(" GROUP BY ")
		for i, gb := range stmt.groupBy {
			if i > 0 {
				b.writeString(", ")
			}
			b.renderSQL(gb, &b.argsBuffer)
		}
	}

	if stmt.having != nil {
		b.writeString(" HAVING ")
		b.renderSQL(*stmt.having, &b.argsBuffer)
	}

	if len(stmt.orderBys) > 0 {
		b.writeString(" ORDER BY ")
		for i, ob := range stmt.orderBys {
			if i > 0 {
				b.writeString(", ")
			}
			b.renderSQL(ob.expr, &b.argsBuffer)

			if ob.ascending {
				b.writeString(" ASC")
			} else {
				b.writeString(" DESC")
			}

			// SQLite default: ASC → NULLS FIRST, DESC → NULLS LAST (opposite of PostgreSQL)
			// Always render NULLS clause to match PostgreSQL/Oracle standard behavior
			if ob.nullsLast {
				b.writeString(" NULLS LAST")
			} else {
				b.writeString(" NULLS FIRST")
			}
		}
	}

	if stmt.limit != nil {
		b.writeString(" LIMIT ")
		b.renderSQL(*stmt.limit, &b.argsBuffer)
	}

	if stmt.offset != nil {
		b.writeString(" OFFSET ")
		b.renderSQL(*stmt.offset, &b.argsBuffer)
	}

	return b.sqlString(), b.argsBuffer
}

// renderSelect renders a CTE-based SELECT with JOIN pattern
// Query format: WITH keys (col1, col2) AS (VALUES (?,?), (?,?)) SELECT table.col1, table.col2 FROM table JOIN keys ON ...
func (b *sqliteBackend) renderSelect(stmt loadRowsOp, keys []Key) (string, []any) {
	b.paramIndex = 0
	numKeyColumns := len(stmt.keyColumns)
	b.resetArgsBuffer(len(keys) * numKeyColumns)

	b.resetSQLBuffer(512)

	b.writeString("WITH keys (")
	for i, col := range stmt.keyColumns {
		if i > 0 {
			b.writeString(", ")
		}
		b.quoteIdentifier(col)
	}
	b.writeString(") AS (VALUES ")

	for idx, key := range keys {
		if idx > 0 {
			b.writeString(", ")
		}
		b.writeByte('(')
		for j := range stmt.keyColumns {
			if j > 0 {
				b.writeString(", ")
			}
			b.writeByte('?')
			b.paramIndex++
			b.argsBuffer = append(b.argsBuffer, key.At(j))
		}
		b.writeByte(')')
	}
	b.writeString(") ")

	b.writeString("SELECT ")
	for i, col := range stmt.selectColumns {
		if i > 0 {
			b.writeString(", ")
		}
		b.quoteIdentifier(stmt.table)
		b.writeByte('.')
		b.quoteIdentifier(col)
	}

	b.writeString(" FROM ")
	b.quoteIdentifier(stmt.table)

	b.writeString(" JOIN keys ON ")
	for j := range stmt.keyColumns {
		if j > 0 {
			b.writeString(" AND ")
		}
		b.quoteIdentifier(stmt.table)
		b.writeByte('.')
		b.quoteIdentifier(stmt.keyColumns[j])
		b.writeString(" = keys.")
		b.quoteIdentifier(stmt.keyColumns[j])
	}

	return b.sqlString(), b.argsBuffer
}

func (b *sqliteBackend) renderGeneratedInsertRows(op insertOp, useSQLiteSequence bool) (string, []any, error) {
	generatedPrimaryColumns := op.generatedPrimaryColumns
	if len(generatedPrimaryColumns) != 1 {
		return "", nil, fmt.Errorf("sqlite generated insert correlation requires one generated primary key")
	}

	b.paramIndex = 0
	insertColumns := op.insertColumns
	extraArgs := 0
	if useSQLiteSequence {
		extraArgs = 1
	}
	b.resetArgsBuffer(len(op.rows)*len(insertColumns) + extraArgs)
	b.resetSQLBuffer(768)

	b.writeString("WITH new_rows (")
	b.quoteIdentifier("$i")
	for _, col := range insertColumns {
		b.writeString(", ")
		b.quoteIdentifier(col)
	}
	b.writeString(") AS (VALUES ")

	for idx, planned := range op.rows {
		if idx > 0 {
			b.writeString(", ")
		}
		b.writeByte('(')
		b.writeString(strconv.Itoa(idx))
		values := insertValuesFromRow(op.insertIndexes, planned.values)
		for _, val := range values {
			b.writeString(", ")
			b.writeByte('?')
			b.paramIndex++
			b.argsBuffer = append(b.argsBuffer, val)
		}
		b.writeByte(')')
	}
	b.writeByte(')')

	generatedColumn := generatedPrimaryColumns[0]
	b.writeString(", base (")
	b.quoteIdentifier("$base")
	b.writeString(") AS (SELECT ")
	if useSQLiteSequence {
		b.writeString("MAX(COALESCE((SELECT seq FROM sqlite_sequence WHERE name = ?), 0), COALESCE((SELECT MAX(")
		b.quoteIdentifier(generatedColumn)
		b.writeString(") FROM ")
		b.quoteIdentifier(op.table)
		b.writeString("), 0))")
		b.argsBuffer = append(b.argsBuffer, op.table)
	} else {
		b.writeString("COALESCE(MAX(")
		b.quoteIdentifier(generatedColumn)
		b.writeString("), 0) FROM ")
		b.quoteIdentifier(op.table)
	}
	b.writeByte(')')

	b.writeString(", allocated AS (SELECT ")
	b.quoteIdentifier("$i")
	b.writeString(", ")
	b.quoteIdentifier("$base")
	b.writeString(" + ROW_NUMBER() OVER (ORDER BY ")
	b.quoteIdentifier("$i")
	b.writeString(") AS ")
	b.quoteIdentifier(generatedColumn)
	for _, col := range insertColumns {
		b.writeString(", ")
		b.quoteIdentifier(col)
	}
	b.writeString(" FROM new_rows CROSS JOIN base)")

	b.writeString(" INSERT INTO ")
	b.quoteIdentifier(op.table)
	b.writeString(" (")
	insertWithGenerated := op.insertColumnsWithGeneratedPrimary
	for i, col := range insertWithGenerated {
		if i > 0 {
			b.writeString(", ")
		}
		b.quoteIdentifier(col)
	}
	b.writeString(") SELECT ")
	for i, col := range insertWithGenerated {
		if i > 0 {
			b.writeString(", ")
		}
		b.quoteIdentifier(col)
	}
	b.writeString(" FROM allocated ORDER BY ")
	b.quoteIdentifier("$i")

	if len(op.returningColumns) > 0 {
		b.writeString(" RETURNING ")
		for i, col := range op.returningColumns {
			if i > 0 {
				b.writeString(", ")
			}
			b.quoteIdentifier(col)
		}
	}

	return b.sqlString(), b.argsBuffer, nil
}

func (b *sqliteBackend) renderInsertRows(op insertOp) (string, []any) {
	b.paramIndex = 0
	insertColumns := op.insertColumns
	b.resetArgsBuffer(len(op.rows) * len(insertColumns))
	b.resetSQLBuffer(512)

	b.writeString("WITH new_rows (")
	for i, col := range insertColumns {
		if i > 0 {
			b.writeString(", ")
		}
		b.quoteIdentifier(col)
	}
	b.writeString(") AS (VALUES ")

	for idx, planned := range op.rows {
		if idx > 0 {
			b.writeString(", ")
		}
		b.writeByte('(')
		values := insertValuesFromRow(op.insertIndexes, planned.values)
		for j, val := range values {
			if j > 0 {
				b.writeString(", ")
			}
			b.writeByte('?')
			b.paramIndex++
			b.argsBuffer = append(b.argsBuffer, val)
		}
		b.writeByte(')')
	}
	b.writeString(") ")

	b.writeString("INSERT INTO ")
	b.quoteIdentifier(op.table)
	b.writeString(" (")
	for i, col := range insertColumns {
		if i > 0 {
			b.writeString(", ")
		}
		b.quoteIdentifier(col)
	}
	b.writeString(") SELECT ")
	for i, col := range insertColumns {
		if i > 0 {
			b.writeString(", ")
		}
		b.quoteIdentifier(col)
	}
	b.writeString(" FROM new_rows")

	if len(op.returningColumns) > 0 {
		b.writeString(" RETURNING ")
		for i, col := range op.returningColumns {
			if i > 0 {
				b.writeString(", ")
			}
			b.quoteIdentifier(col)
		}
	}

	return b.sqlString(), b.argsBuffer
}

func (b *sqliteBackend) renderSingleUpdateRow(op updateOp, values []any) (string, []any) {
	b.resetSQLBuffer(256)
	b.resetArgsBuffer(len(op.rowColumns))

	updateColumns := op.updateColumns
	if len(updateColumns) == 0 {
		updateColumns = op.primaryColumns[:1]
	}

	// Build column index map
	colIndex := make(map[string]int, len(op.rowColumns))
	for i, col := range op.rowColumns {
		colIndex[col] = i
	}

	b.writeString("UPDATE ")
	b.quoteIdentifier(op.table)
	b.writeString(" SET ")
	for i, col := range updateColumns {
		if i > 0 {
			b.writeString(", ")
		}
		b.quoteIdentifier(col)
		b.writeString(" = ?")
		b.argsBuffer = append(b.argsBuffer, values[colIndex[col]])
	}
	b.writeString(" WHERE ")
	for i, col := range op.primaryColumns {
		if i > 0 {
			b.writeString(" AND ")
		}
		b.quoteIdentifier(col)
		b.writeString(" = ?")
		b.argsBuffer = append(b.argsBuffer, values[colIndex[col]])
	}
	if len(op.returningColumns) > 0 {
		b.writeString(" RETURNING ")
		for i, col := range op.returningColumns {
			if i > 0 {
				b.writeString(", ")
			}
			b.quoteIdentifier(col)
		}
	}

	return b.sqlString(), b.argsBuffer
}

// renderDelete renders a CTE-based DELETE with subquery pattern
// Query format: WITH keys (id) AS (VALUES (?), (?)) DELETE FROM table WHERE id IN (SELECT id FROM keys)
func (b *sqliteBackend) renderDelete(stmt deleteRowsOp, keys []Key) (string, []any) {
	b.paramIndex = 0
	numKeyColumns := len(stmt.keyColumns)
	b.resetArgsBuffer(len(keys) * numKeyColumns)

	b.resetSQLBuffer(512)

	b.writeString("WITH keys (")
	for i, col := range stmt.keyColumns {
		if i > 0 {
			b.writeString(", ")
		}
		b.quoteIdentifier(col)
	}
	b.writeString(") AS (VALUES ")

	for idx, key := range keys {
		if idx > 0 {
			b.writeString(", ")
		}
		b.writeByte('(')
		for j := range stmt.keyColumns {
			if j > 0 {
				b.writeString(", ")
			}
			b.writeByte('?')
			b.paramIndex++
			b.argsBuffer = append(b.argsBuffer, key.At(j))
		}
		b.writeByte(')')
	}
	b.writeString(") ")

	b.writeString("DELETE FROM ")
	b.quoteIdentifier(stmt.table)
	b.writeString(" WHERE ")

	if numKeyColumns == 1 {
		b.quoteIdentifier(stmt.keyColumns[0])
		b.writeString(" IN (SELECT ")
		b.quoteIdentifier(stmt.keyColumns[0])
		b.writeString(" FROM keys)")
	} else {
		b.writeByte('(')
		for i, col := range stmt.keyColumns {
			if i > 0 {
				b.writeString(", ")
			}
			b.quoteIdentifier(col)
		}
		b.writeString(") IN (SELECT ")
		for i, col := range stmt.keyColumns {
			if i > 0 {
				b.writeString(", ")
			}
			b.quoteIdentifier(col)
		}
		b.writeString(" FROM keys)")
	}

	return b.sqlString(), b.argsBuffer
}

func (b *sqliteBackend) LoadRows(ctx context.Context, op loadRowsOp) (rows, error) {
	query, args := b.renderSelect(op, op.keys)
	return b.queryContext(ctx, query, args...)
}

func (b *sqliteBackend) InsertRows(ctx context.Context, op insertOp) (rows, error) {
	if len(op.generatedPrimaryColumns) > 0 {
		return b.insertGeneratedRows(ctx, op)
	}
	return b.insertManualRows(ctx, op)
}

func (b *sqliteBackend) insertGeneratedRows(ctx context.Context, op insertOp) (rows, error) {
	useSQLiteSequence, err := b.hasSQLiteSequence(ctx)
	if err != nil {
		return nil, err
	}
	query, args, err := b.renderGeneratedInsertRows(op, useSQLiteSequence)
	if err != nil {
		return nil, err
	}
	rowSet, err := b.queryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rowSet.Close() }()

	// Buffer all rows
	returningCount := len(op.returningColumns)
	dest := make([]any, returningCount)
	type keyedRow struct {
		pk     int64
		values []any
	}
	var keyed []keyedRow
	for rowSet.Next() {
		values := make([]any, returningCount)
		if err := scanRowValues(rowSet, values, dest); err != nil {
			return nil, err
		}
		// PK is always at column 0 (primary key fields are first in returningColumns)
		pk, err := int64FromDB(values[0])
		if err != nil {
			return nil, err
		}
		keyed = append(keyed, keyedRow{pk: pk, values: values})
	}
	// Sort by PK (ascending = input order due to ROW_NUMBER)
	sort.Slice(keyed, func(i, j int) bool { return keyed[i].pk < keyed[j].pk })

	result := make([][]any, len(keyed))
	for i, kr := range keyed {
		result[i] = kr.values
	}
	return &bufferedRows{data: result}, nil
}

func (b *sqliteBackend) insertManualRows(ctx context.Context, op insertOp) (rows, error) {
	query, args := b.renderInsertRows(op)
	rowSet, err := b.queryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rowSet.Close() }()

	inputByKey := make(map[Key]int, len(op.rows))
	for i, row := range op.rows {
		inputByKey[row.key] = i
	}

	returningCount := len(op.returningColumns)
	dest := make([]any, returningCount)
	scanned := make([][]any, len(op.rows))
	for rowSet.Next() {
		values := make([]any, returningCount)
		if err := scanRowValues(rowSet, values, dest); err != nil {
			return nil, err
		}
		key := op.keyFromReturning(values)
		idx, ok := inputByKey[key]
		if !ok {
			return nil, fmt.Errorf("returned key %v does not match any input row", key)
		}
		scanned[idx] = values
	}
	return &bufferedRows{data: scanned}, nil
}

func (b *sqliteBackend) UpdateRows(ctx context.Context, op updateOp) (rows, error) {
	returningCount := len(op.returningColumns)
	result := make([][]any, 0, len(op.rows))

	for _, planned := range op.rows {
		query, args := b.renderSingleUpdateRow(op, planned.values)
		rowSet, err := b.queryContext(ctx, query, args...)
		if err != nil {
			return nil, err
		}
		values := make([]any, returningCount)
		dest := make([]any, returningCount)
		if rowSet.Next() {
			if err := scanRowValues(rowSet, values, dest); err != nil {
				_ = rowSet.Close()
				return nil, err
			}
		}
		_ = rowSet.Close()
		result = append(result, values)
	}

	return &bufferedRows{data: result}, nil
}

func (b *sqliteBackend) DeleteRowsByKeys(ctx context.Context, op deleteRowsOp) error {
	return b.deleteRows(ctx, op)
}

func (b *sqliteBackend) deleteRows(ctx context.Context, op deleteRowsOp) error {
	if len(op.keys) == 0 {
		return nil
	}

	query, args := b.renderDelete(op, op.keys)

	_, err := b.execContext(ctx, query, args...)
	return err
}

func (b *sqliteBackend) FetchQuery(ctx context.Context, stmt sqlQuery) (rows, error) {
	query, args := b.renderSQLQuery(stmt)
	return b.queryContext(ctx, query, args...)
}

func (b *sqliteBackend) CountQuery(ctx context.Context, stmt sqlQuery) (int64, error) {
	innerStmt := stmt
	innerStmt.limit = nil
	innerStmt.offset = nil

	innerQuery, args := b.renderSQLQuery(innerStmt)
	countQuery := fmt.Sprintf("SELECT COUNT(*) FROM (%s) AS count_subquery", innerQuery)

	var count int64
	err := b.queryRowContext(ctx, countQuery, args...).Scan(&count)
	return count, err
}
