package ormapper

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
)

type postgresDialect struct{}

func (postgresDialect) newBackend(db DBTX) backend { return newPostgreSQLBackend(db) }

// postgreSQLBackend implements backend for PostgreSQL databases.
type postgreSQLBackend struct {
	db         DBTX
	paramIndex int
	argsBuffer []any  // Reusable buffer for query parameters
	sqlBuffer  []byte // Reusable buffer for SQL generation
}

// newPostgreSQLBackend creates a new PostgreSQL backend.
func newPostgreSQLBackend(db DBTX) backend {
	return &postgreSQLBackend{
		db: db,
	}
}

// resetArgsBuffer resets the argument buffer to length 0, growing capacity if needed.
// Uses 2x growth factor to reduce reallocation frequency.
func (b *postgreSQLBackend) resetArgsBuffer(minCapacity int) {
	if cap(b.argsBuffer) < minCapacity {
		b.argsBuffer = make([]any, 0, minCapacity*2)
	} else {
		b.argsBuffer = b.argsBuffer[:0]
	}
}

// resetSQLBuffer resets the SQL buffer to length 0, growing capacity if needed.
// Uses 2x growth factor to reduce reallocation frequency.
func (b *postgreSQLBackend) resetSQLBuffer(minCapacity int) {
	b.sqlBuffer = b.sqlBuffer[:0]
	if cap(b.sqlBuffer) < minCapacity {
		b.sqlBuffer = make([]byte, 0, minCapacity*2)
	}
}

// writeString appends a string to the SQL buffer.
func (b *postgreSQLBackend) writeString(s string) {
	b.sqlBuffer = append(b.sqlBuffer, s...)
}

// writeByte appends a byte to the SQL buffer.
func (b *postgreSQLBackend) writeByte(c byte) {
	b.sqlBuffer = append(b.sqlBuffer, c)
}

// quoteIdentifier quotes a SQL identifier by escaping " as "" and wrapping in "
func (b *postgreSQLBackend) quoteIdentifier(identifier string) {
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

// quoteTable quotes a table reference, optionally with schema
func (b *postgreSQLBackend) quoteTable(schema, table string) {
	if schema != "" {
		b.quoteIdentifier(schema)
		b.writeByte('.')
	}
	b.quoteIdentifier(table)
}

func pgSerialSequenceTableName(schema, table string) string {
	if schema == "" {
		return pgIdentifierText(table)
	}
	return pgIdentifierText(schema) + "." + pgIdentifierText(table)
}

func pgIdentifierText(identifier string) string {
	quoted := make([]byte, 0, len(identifier)+2)
	quoted = append(quoted, '"')
	for i := 0; i < len(identifier); i++ {
		if identifier[i] == '"' {
			quoted = append(quoted, '"', '"')
		} else {
			quoted = append(quoted, identifier[i])
		}
	}
	quoted = append(quoted, '"')
	return string(quoted)
}

// sqlString returns the current SQL buffer as a string.
func (b *postgreSQLBackend) sqlString() string {
	return string(b.sqlBuffer)
}

// queryContext executes a query using the provided database handle.
func (b *postgreSQLBackend) queryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return b.db.QueryContext(ctx, query, args...)
}

// execContext executes a command using the provided database handle.
func (b *postgreSQLBackend) execContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return b.db.ExecContext(ctx, query, args...)
}

// queryRowContext executes a query that returns at most one row using the provided database handle.
func (b *postgreSQLBackend) queryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return b.db.QueryRowContext(ctx, query, args...)
}

func (b *postgreSQLBackend) renderSQL(sql sqlNode, args *[]any) {
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
		b.writeByte('$')
		b.writeString(strconv.Itoa(b.paramIndex))
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

func (b *postgreSQLBackend) renderSQLQuery(stmt sqlQuery) (string, []any) {
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

			// PostgreSQL default: ASC → NULLS LAST, DESC → NULLS FIRST
			// Only render NULLS clause if it differs from the default
			if ob.ascending && !ob.nullsLast {
				// ASC with NULLS FIRST (non-default)
				b.writeString(" NULLS FIRST")
			} else if !ob.ascending && ob.nullsLast {
				// DESC with NULLS LAST (non-default)
				b.writeString(" NULLS LAST")
			}
			// Skip NULLS clause when it matches PostgreSQL default
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

// renderSelect renders a CTE-based SELECT with JOIN for PostgreSQL
// Query format: WITH "$k" (col1, col2) AS (VALUES ($1, $2), ($3, $4))
//
//	SELECT table.* FROM table JOIN "$k" ON table.col1 = "$k".col1 AND table.col2 = "$k".col2
func (b *postgreSQLBackend) renderSelect(stmt loadRowsOp, keys []Key) (string, []any) {
	b.paramIndex = 0
	numKeyColumns := len(stmt.keyColumns)
	b.resetArgsBuffer(len(keys) * numKeyColumns)

	b.resetSQLBuffer(512)

	b.writeString("WITH \"$k\" (")
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
			b.paramIndex++
			if idx == 0 {
				// First row: use COALESCE to infer type from table schema
				b.writeString("COALESCE((NULL::")
				b.quoteTable(stmt.schema, stmt.table)
				b.writeString(").")
				b.quoteIdentifier(stmt.keyColumns[j])
				b.writeString(", $")
				b.writeString(strconv.Itoa(b.paramIndex))
				b.writeByte(')')
			} else {
				// Subsequent rows: just use placeholder
				b.writeByte('$')
				b.writeString(strconv.Itoa(b.paramIndex))
			}
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
	b.quoteTable(stmt.schema, stmt.table)

	b.writeString(" JOIN \"$k\" ON ")
	for j, col := range stmt.keyColumns {
		if j > 0 {
			b.writeString(" AND ")
		}
		b.quoteIdentifier(stmt.table)
		b.writeByte('.')
		b.quoteIdentifier(col)
		b.writeString(" = \"$k\".")
		b.quoteIdentifier(col)
	}

	return b.sqlString(), b.argsBuffer
}

func (b *postgreSQLBackend) renderGeneratedInsertRows(op insertOp) (string, []any) {
	b.paramIndex = 0
	insertColumns := op.insertColumns
	generatedPrimaryColumns := op.generatedPrimaryColumns
	b.resetArgsBuffer(len(op.rows)*len(insertColumns) + len(generatedPrimaryColumns)*2)
	b.resetSQLBuffer(768)

	b.writeString("WITH \"$r\" (")
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
		for j, val := range values {
			b.writeString(", ")
			b.paramIndex++
			if idx == 0 {
				b.writeString("COALESCE((NULL::")
				b.quoteTable(op.schema, op.table)
				b.writeString(").")
				b.quoteIdentifier(insertColumns[j])
				b.writeString(", $")
				b.writeString(strconv.Itoa(b.paramIndex))
				b.writeByte(')')
			} else {
				b.writeByte('$')
				b.writeString(strconv.Itoa(b.paramIndex))
			}
			b.argsBuffer = append(b.argsBuffer, val)
		}
		b.writeByte(')')
	}
	b.writeByte(')')

	b.writeString(", \"$a\" AS (SELECT ")
	b.quoteIdentifier("$i")
	for _, col := range generatedPrimaryColumns {
		b.writeString(", nextval(pg_get_serial_sequence($")
		b.paramIndex++
		b.writeString(strconv.Itoa(b.paramIndex))
		b.writeString(", $")
		b.paramIndex++
		b.writeString(strconv.Itoa(b.paramIndex))
		b.writeString(")::regclass) AS ")
		b.quoteIdentifier(col)
		b.argsBuffer = append(b.argsBuffer, pgSerialSequenceTableName(op.schema, op.table), col)
	}
	for _, col := range insertColumns {
		b.writeString(", ")
		b.quoteIdentifier(col)
	}
	b.writeString(" FROM \"$r\")")

	b.writeString(", \"$ins\" AS (INSERT INTO ")
	b.quoteTable(op.schema, op.table)
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
	b.writeString(" FROM \"$a\" ORDER BY ")
	b.quoteIdentifier("$i")
	b.writeString(" RETURNING ")
	for i, col := range op.returningColumns {
		if i > 0 {
			b.writeString(", ")
		}
		b.quoteIdentifier(op.table)
		b.writeByte('.')
		b.quoteIdentifier(col)
	}
	b.writeByte(')')

	b.writeString(" SELECT ")
	for i, col := range op.returningColumns {
		if i > 0 {
			b.writeString(", ")
		}
		b.writeString("\"$ins\".")
		b.quoteIdentifier(col)
	}
	b.writeString(" FROM \"$ins\" JOIN \"$a\" ON ")
	for i, col := range generatedPrimaryColumns {
		if i > 0 {
			b.writeString(" AND ")
		}
		b.writeString("\"$ins\".")
		b.quoteIdentifier(col)
		b.writeString(" = \"$a\".")
		b.quoteIdentifier(col)
	}
	b.writeString(" ORDER BY \"$a\".")
	b.quoteIdentifier("$i")

	return b.sqlString(), b.argsBuffer
}

func (b *postgreSQLBackend) renderInsertRows(op insertOp) (string, []any) {
	b.paramIndex = 0
	insertColumns := op.insertColumns
	b.resetArgsBuffer(len(op.rows) * len(insertColumns))
	b.resetSQLBuffer(512)

	b.writeString("WITH \"$r\" (")
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
		for j, val := range values {
			b.writeString(", ")
			b.paramIndex++
			if idx == 0 {
				b.writeString("COALESCE((NULL::")
				b.quoteTable(op.schema, op.table)
				b.writeString(").")
				b.quoteIdentifier(insertColumns[j])
				b.writeString(", $")
				b.writeString(strconv.Itoa(b.paramIndex))
				b.writeByte(')')
			} else {
				b.writeByte('$')
				b.writeString(strconv.Itoa(b.paramIndex))
			}
			b.argsBuffer = append(b.argsBuffer, val)
		}
		b.writeByte(')')
	}
	b.writeString("), \"$ins\" AS (INSERT INTO ")
	b.quoteTable(op.schema, op.table)
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
	b.writeString(" FROM \"$r\" ORDER BY ")
	b.quoteIdentifier("$i")
	b.writeString(" RETURNING ")
	for i, col := range op.returningColumns {
		if i > 0 {
			b.writeString(", ")
		}
		b.quoteIdentifier(op.table)
		b.writeByte('.')
		b.quoteIdentifier(col)
	}
	b.writeString(") SELECT ")
	for i, col := range op.returningColumns {
		if i > 0 {
			b.writeString(", ")
		}
		b.writeString("\"$ins\".")
		b.quoteIdentifier(col)
	}
	b.writeString(" FROM \"$ins\" JOIN \"$r\" ON ")
	for i, col := range op.primaryColumns {
		if i > 0 {
			b.writeString(" AND ")
		}
		b.writeString("\"$ins\".")
		b.quoteIdentifier(col)
		b.writeString(" = \"$r\".")
		b.quoteIdentifier(col)
	}
	b.writeString(" ORDER BY \"$r\".")
	b.quoteIdentifier("$i")

	return b.sqlString(), b.argsBuffer
}

func (b *postgreSQLBackend) renderUpdateRows(op updateOp) (string, []any) {
	b.paramIndex = 0
	rowColumns := op.rowColumns
	keyColumns := op.primaryColumns
	updateColumns := op.updateColumns
	if len(updateColumns) == 0 {
		updateColumns = keyColumns[:1]
	}
	b.resetArgsBuffer(len(op.rows) * len(rowColumns))
	b.resetSQLBuffer(512)

	b.writeString("WITH \"$r\" (")
	b.quoteIdentifier("$i")
	for _, col := range rowColumns {
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
		for j := range rowColumns {
			b.writeString(", ")
			b.paramIndex++
			if idx == 0 {
				b.writeString("COALESCE((NULL::")
				b.quoteTable(op.schema, op.table)
				b.writeString(").")
				b.quoteIdentifier(rowColumns[j])
				b.writeString(", $")
				b.writeString(strconv.Itoa(b.paramIndex))
				b.writeByte(')')
			} else {
				b.writeByte('$')
				b.writeString(strconv.Itoa(b.paramIndex))
			}
			b.argsBuffer = append(b.argsBuffer, planned.values[j])
		}
		b.writeByte(')')
	}
	b.writeString("), \"$upd\" AS (UPDATE ")
	b.quoteTable(op.schema, op.table)
	b.writeString(" SET ")
	for i, col := range updateColumns {
		if i > 0 {
			b.writeString(", ")
		}
		b.quoteIdentifier(col)
		b.writeString(" = \"$r\".")
		b.quoteIdentifier(col)
	}
	b.writeString(" FROM \"$r\" WHERE ")
	for i, col := range keyColumns {
		if i > 0 {
			b.writeString(" AND ")
		}
		b.quoteIdentifier(op.table)
		b.writeByte('.')
		b.quoteIdentifier(col)
		b.writeString(" = \"$r\".")
		b.quoteIdentifier(col)
	}
	b.writeString(" RETURNING ")
	for i, col := range op.returningColumns {
		if i > 0 {
			b.writeString(", ")
		}
		b.quoteIdentifier(op.table)
		b.writeByte('.')
		b.quoteIdentifier(col)
	}
	b.writeString(") SELECT ")
	for i, col := range op.returningColumns {
		if i > 0 {
			b.writeString(", ")
		}
		b.writeString("\"$upd\".")
		b.quoteIdentifier(col)
	}
	b.writeString(" FROM \"$upd\" JOIN \"$r\" ON ")
	for i, col := range keyColumns {
		if i > 0 {
			b.writeString(" AND ")
		}
		b.writeString("\"$upd\".")
		b.quoteIdentifier(col)
		b.writeString(" = \"$r\".")
		b.quoteIdentifier(col)
	}
	b.writeString(" ORDER BY \"$r\".")
	b.quoteIdentifier("$i")

	return b.sqlString(), b.argsBuffer
}

// renderDelete renders a CTE-based DELETE with UNION pattern
// Query format: WITH keys AS (SELECT id FROM table WHERE FALSE UNION ALL VALUES ($1), ($2))
//
//	DELETE FROM table WHERE id IN (SELECT id FROM keys)
func (b *postgreSQLBackend) renderDelete(stmt deleteRowsOp, keys []Key) (string, []any) {
	b.paramIndex = 0
	numKeyColumns := len(stmt.keyColumns)
	b.resetArgsBuffer(len(keys) * numKeyColumns)

	b.resetSQLBuffer(512)

	b.writeString("WITH \"$k\" (")
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
			b.paramIndex++
			if idx == 0 {
				// First row: use COALESCE to infer type from table schema
				b.writeString("COALESCE((NULL::")
				b.quoteTable(stmt.schema, stmt.table)
				b.writeString(").")
				b.quoteIdentifier(stmt.keyColumns[j])
				b.writeString(", $")
				b.writeString(strconv.Itoa(b.paramIndex))
				b.writeByte(')')
			} else {
				// Subsequent rows: just use placeholder
				b.writeByte('$')
				b.writeString(strconv.Itoa(b.paramIndex))
			}
			b.argsBuffer = append(b.argsBuffer, key.At(j))
		}
		b.writeByte(')')
	}
	b.writeString(") ")

	b.writeString("DELETE FROM ")
	b.quoteTable(stmt.schema, stmt.table)
	b.writeString(" WHERE ")

	if numKeyColumns == 1 {
		b.quoteIdentifier(stmt.keyColumns[0])
		b.writeString(" IN (SELECT ")
		b.quoteIdentifier(stmt.keyColumns[0])
		b.writeString(" FROM \"$k\")")
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
		b.writeString(" FROM \"$k\")")
	}

	return b.sqlString(), b.argsBuffer
}

func (b *postgreSQLBackend) LoadRows(ctx context.Context, op loadRowsOp) (rows, error) {
	query, args := b.renderSelect(op, op.keys)
	return b.queryContext(ctx, query, args...)
}

func (b *postgreSQLBackend) InsertRows(ctx context.Context, op insertOp) (rows, error) {
	var query string
	var args []any
	if len(op.generatedPrimaryColumns) > 0 {
		query, args = b.renderGeneratedInsertRows(op)
	} else {
		query, args = b.renderInsertRows(op)
	}
	return b.queryContext(ctx, query, args...)
}

func (b *postgreSQLBackend) UpdateRows(ctx context.Context, op updateOp) (rows, error) {
	query, args := b.renderUpdateRows(op)
	return b.queryContext(ctx, query, args...)
}

func (b *postgreSQLBackend) DeleteRowsByKeys(ctx context.Context, op deleteRowsOp) error {
	return b.deleteRows(ctx, op)
}

func (b *postgreSQLBackend) deleteRows(ctx context.Context, op deleteRowsOp) error {
	if len(op.keys) == 0 {
		return nil
	}

	query, args := b.renderDelete(op, op.keys)

	_, err := b.execContext(ctx, query, args...)
	return err
}

func (b *postgreSQLBackend) FetchQuery(ctx context.Context, stmt sqlQuery) (rows, error) {
	query, args := b.renderSQLQuery(stmt)
	return b.queryContext(ctx, query, args...)
}

func (b *postgreSQLBackend) CountQuery(ctx context.Context, stmt sqlQuery) (int64, error) {
	innerStmt := stmt
	innerStmt.limit = nil
	innerStmt.offset = nil

	innerQuery, args := b.renderSQLQuery(innerStmt)
	countQuery := fmt.Sprintf("SELECT COUNT(*) FROM (%s) AS count_subquery", innerQuery)

	var count int64
	err := b.queryRowContext(ctx, countQuery, args...).Scan(&count)
	return count, err
}
