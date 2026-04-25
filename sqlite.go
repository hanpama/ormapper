package ormapper

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
)

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
func (b *sqliteBackend) renderSelect(stmt loadRowsOp, chunk [][]any) (string, []any) {
	b.paramIndex = 0
	numKeyColumns := len(stmt.keyColumns)
	b.resetArgsBuffer(len(chunk) * numKeyColumns)

	b.resetSQLBuffer(512)

	b.writeString("WITH keys (")
	for i, col := range stmt.keyColumns {
		if i > 0 {
			b.writeString(", ")
		}
		b.quoteIdentifier(col)
	}
	b.writeString(") AS (VALUES ")

	for idx, row := range chunk {
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
			b.argsBuffer = append(b.argsBuffer, row[j])
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

func (b *sqliteBackend) renderSelectExistingKeys(stmt keyScanOp, chunk [][]any) (string, []any) {
	b.paramIndex = 0
	numKeyColumns := len(stmt.keyColumns)
	b.resetArgsBuffer(len(chunk) * numKeyColumns)
	b.resetSQLBuffer(512)

	b.writeString("WITH keys (")
	for i, col := range stmt.keyColumns {
		if i > 0 {
			b.writeString(", ")
		}
		b.quoteIdentifier(col)
	}
	b.writeString(") AS (VALUES ")

	for idx, row := range chunk {
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
			b.argsBuffer = append(b.argsBuffer, row[j])
		}
		b.writeByte(')')
	}
	b.writeString(") ")

	b.writeString("SELECT ")
	for i, col := range stmt.keyColumns {
		if i > 0 {
			b.writeString(", ")
		}
		b.writeString("keys.")
		b.quoteIdentifier(col)
	}

	b.writeString(" FROM keys JOIN ")
	b.quoteIdentifier(stmt.table)
	b.writeString(" ON ")
	for i, col := range stmt.keyColumns {
		if i > 0 {
			b.writeString(" AND ")
		}
		b.quoteIdentifier(stmt.table)
		b.writeByte('.')
		b.quoteIdentifier(col)
		b.writeString(" = keys.")
		b.quoteIdentifier(col)
	}

	return b.sqlString(), b.argsBuffer
}

func (b *sqliteBackend) renderGeneratedInsertRows(stmt saveRowsOp, indexes []int, rows []saveRow, useSQLiteSequence bool) (string, []any, error) {
	generatedPrimaryFields := stmt.generatedPrimaryFields()
	if len(generatedPrimaryFields) != 1 {
		return "", nil, fmt.Errorf("sqlite generated insert correlation requires one generated primary key")
	}

	b.paramIndex = 0
	insertColumns := stmt.insertColumns()
	extraArgs := 0
	if useSQLiteSequence {
		extraArgs = 1
	}
	b.resetArgsBuffer(len(rows)*len(insertColumns) + extraArgs)
	b.resetSQLBuffer(768)

	b.writeString("WITH new_rows (")
	b.quoteIdentifier("$i")
	for _, col := range insertColumns {
		b.writeString(", ")
		b.quoteIdentifier(col)
	}
	b.writeString(") AS (VALUES ")

	for idx, row := range rows {
		if idx > 0 {
			b.writeString(", ")
		}
		b.writeByte('(')
		b.writeString(strconv.Itoa(indexes[idx]))
		values := stmt.insertValuesForRow(row)
		for _, val := range values {
			b.writeString(", ")
			b.writeByte('?')
			b.paramIndex++
			b.argsBuffer = append(b.argsBuffer, val)
		}
		b.writeByte(')')
	}
	b.writeByte(')')

	generatedColumn := generatedPrimaryFields[0].column
	b.writeString(", base (")
	b.quoteIdentifier("$base")
	b.writeString(") AS (SELECT ")
	if useSQLiteSequence {
		b.writeString("MAX(COALESCE((SELECT seq FROM sqlite_sequence WHERE name = ?), 0), COALESCE((SELECT MAX(")
		b.quoteIdentifier(generatedColumn)
		b.writeString(") FROM ")
		b.quoteIdentifier(stmt.table)
		b.writeString("), 0))")
		b.argsBuffer = append(b.argsBuffer, stmt.table)
	} else {
		b.writeString("COALESCE(MAX(")
		b.quoteIdentifier(generatedColumn)
		b.writeString("), 0) FROM ")
		b.quoteIdentifier(stmt.table)
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
	b.quoteIdentifier(stmt.table)
	b.writeString(" (")
	insertWithGenerated := stmt.insertColumnsWithGeneratedPrimary()
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

	if len(stmt.returning) > 0 {
		b.writeString(" RETURNING ")
		for i, col := range stmt.returning {
			if i > 0 {
				b.writeString(", ")
			}
			b.quoteIdentifier(col)
		}
	}

	return b.sqlString(), b.argsBuffer, nil
}

func (b *sqliteBackend) renderInsertRows(stmt saveRowsOp, rows []saveRow) (string, []any) {
	b.paramIndex = 0
	insertColumns := stmt.insertColumns()
	b.resetArgsBuffer(len(rows) * len(insertColumns))
	b.resetSQLBuffer(512)

	b.writeString("WITH new_rows (")
	for i, col := range insertColumns {
		if i > 0 {
			b.writeString(", ")
		}
		b.quoteIdentifier(col)
	}
	b.writeString(") AS (VALUES ")

	for idx, row := range rows {
		if idx > 0 {
			b.writeString(", ")
		}
		b.writeByte('(')
		values := stmt.insertValuesForRow(row)
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
	b.quoteIdentifier(stmt.table)
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

	if len(stmt.returning) > 0 {
		b.writeString(" RETURNING ")
		for i, col := range stmt.returning {
			if i > 0 {
				b.writeString(", ")
			}
			b.quoteIdentifier(col)
		}
	}

	return b.sqlString(), b.argsBuffer
}

func (b *sqliteBackend) renderUpdateRows(stmt saveRowsOp, rows []saveRow) (string, []any) {
	b.paramIndex = 0
	rowColumns := stmt.rowColumns()
	keyColumns := stmt.conflictColumns()
	updateColumns := stmt.updateColumns()
	if len(updateColumns) == 0 {
		updateColumns = keyColumns[:1]
	}
	b.resetArgsBuffer(len(rows) * len(rowColumns))
	b.resetSQLBuffer(512)

	b.writeString("WITH new_rows (")
	for i, col := range rowColumns {
		if i > 0 {
			b.writeString(", ")
		}
		b.quoteIdentifier(col)
	}
	b.writeString(") AS (VALUES ")

	for idx, row := range rows {
		if idx > 0 {
			b.writeString(", ")
		}
		b.writeByte('(')
		for j := range rowColumns {
			if j > 0 {
				b.writeString(", ")
			}
			b.writeByte('?')
			b.paramIndex++
			b.argsBuffer = append(b.argsBuffer, row.values[j])
		}
		b.writeByte(')')
	}
	b.writeString(") UPDATE ")
	b.quoteIdentifier(stmt.table)
	b.writeString(" SET ")
	for i, col := range updateColumns {
		if i > 0 {
			b.writeString(", ")
		}
		b.quoteIdentifier(col)
		b.writeString(" = new_rows.")
		b.quoteIdentifier(col)
	}
	b.writeString(" FROM new_rows WHERE ")
	for i, col := range keyColumns {
		if i > 0 {
			b.writeString(" AND ")
		}
		b.quoteIdentifier(stmt.table)
		b.writeByte('.')
		b.quoteIdentifier(col)
		b.writeString(" = new_rows.")
		b.quoteIdentifier(col)
	}

	if len(stmt.returning) > 0 {
		b.writeString(" RETURNING ")
		for i, col := range stmt.returning {
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
func (b *sqliteBackend) renderDelete(stmt deleteRowsOp, chunk [][]any) (string, []any) {
	b.paramIndex = 0
	numKeyColumns := len(stmt.keyColumns)
	b.resetArgsBuffer(len(chunk) * numKeyColumns)

	b.resetSQLBuffer(512)

	b.writeString("WITH keys (")
	for i, col := range stmt.keyColumns {
		if i > 0 {
			b.writeString(", ")
		}
		b.quoteIdentifier(col)
	}
	b.writeString(") AS (VALUES ")

	for idx, row := range chunk {
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
			b.argsBuffer = append(b.argsBuffer, row[j])
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

func (b *sqliteBackend) LoadByKeys(ctx context.Context, op loadRowsOp) (rows, error) {
	if len(op.keys) == 0 {
		return &emptyRows{}, nil
	}

	query, args := b.renderSelect(op, rowsFromKeys(op.keys))
	return b.queryContext(ctx, query, args...)
}

func (b *sqliteBackend) LoadByParentKeys(ctx context.Context, op loadRowsOp) (rows, error) {
	return b.LoadByKeys(ctx, op)
}

func (b *sqliteBackend) SelectExistingKeys(ctx context.Context, op keyScanOp) ([]Key, error) {
	if len(op.keys) == 0 {
		return nil, nil
	}

	query, args := b.renderSelectExistingKeys(op, rowsFromKeys(op.keys))
	rowSet, err := b.queryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rowSet.Close() }()
	return scanTypedKeys(rowSet, op.keyTypes)
}

func (b *sqliteBackend) InsertRows(ctx context.Context, op saveRowsOp, rows []plannedRow) ([]savedRow, error) {
	if len(rows) == 0 {
		return nil, nil
	}

	indexes, saveRows := splitPlannedRows(rows)
	if len(op.generatedPrimaryFields()) > 0 {
		return b.insertGeneratedRows(ctx, op, indexes, saveRows)
	}

	query, args := b.renderInsertRows(op, saveRows)
	rowSet, err := b.queryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rowSet.Close() }()

	saved, err := scanKeyedSavedRows(op, indexes, saveRows, rowSet)
	if err != nil {
		return nil, err
	}
	if len(saved) != len(rows) {
		return nil, fmt.Errorf("%w: expected %d inserted rows, got %d", ErrConsistency, len(rows), len(saved))
	}
	return saved, nil
}

func (b *sqliteBackend) insertGeneratedRows(ctx context.Context, op saveRowsOp, indexes []int, rows []saveRow) ([]savedRow, error) {
	if len(rows) == 0 {
		return nil, nil
	}

	useSQLiteSequence, err := b.hasSQLiteSequence(ctx)
	if err != nil {
		return nil, err
	}

	query, args, err := b.renderGeneratedInsertRows(op, indexes, rows, useSQLiteSequence)
	if err != nil {
		return nil, err
	}
	rowSet, err := b.queryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rowSet.Close() }()

	valueRows, err := scanValueRows(rowSet, len(op.returning))
	if err != nil {
		return nil, err
	}
	if len(valueRows) != len(rows) {
		return nil, fmt.Errorf("expected %d returned rows, got %d", len(rows), len(valueRows))
	}
	return savedRowsBySingleIntKeyOrder(op, indexes, valueRows)
}

func (b *sqliteBackend) UpdateRows(ctx context.Context, op saveRowsOp, rows []plannedRow) ([]savedRow, error) {
	if len(rows) == 0 {
		return nil, nil
	}

	indexes, saveRows := splitPlannedRows(rows)
	query, args := b.renderUpdateRows(op, saveRows)
	rowSet, err := b.queryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rowSet.Close() }()

	saved, err := scanKeyedSavedRows(op, indexes, saveRows, rowSet)
	if err != nil {
		return nil, err
	}
	if len(saved) != len(rows) {
		return nil, fmt.Errorf("%w: expected %d updated rows, got %d", ErrStaleEntity, len(rows), len(saved))
	}
	return saved, nil
}

func (b *sqliteBackend) DeleteRowsByKeys(ctx context.Context, op deleteRowsOp) error {
	return b.deleteRows(ctx, op)
}

func (b *sqliteBackend) deleteRows(ctx context.Context, op deleteRowsOp) error {
	if len(op.keys) == 0 {
		return nil
	}

	query, args := b.renderDelete(op, rowsFromKeys(op.keys))

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
