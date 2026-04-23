package ormapper

import (
	"context"
	"database/sql"
	"fmt"
)

// sqliteBackend implements backend for SQLite databases.
type sqliteBackend struct {
	db         DBTX
	paramIndex int
	argsBuffer []any  // Reusable buffer for query parameters
	sqlBuffer  []byte // Reusable buffer for SQL generation
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

func (b *sqliteBackend) renderSQL(sql sqlNode, args *[]any) {
	switch v := sql.(type) {
	case sqlN:
		b.quoteIdentifier(v.Part)
	case sqlQN:
		if v.Part1 == "" {
			b.quoteIdentifier(v.Part2)
		} else {
			b.quoteIdentifier(v.Part1)
			b.writeByte('.')
			b.quoteIdentifier(v.Part2)
		}
	case sqlText:
		b.writeString(v.Text)
	case sqlParam:
		b.paramIndex++
		*args = append(*args, v.Value)
		b.writeByte('?')
	case sqlAll:
		b.writeByte('(')
		for i, el := range v.Els {
			if i > 0 {
				b.writeString(" AND ")
			}
			b.renderSQL(el, args)
		}
		b.writeByte(')')
	case sqlAny:
		b.writeByte('(')
		for i, el := range v.Els {
			if i > 0 {
				b.writeString(" OR ")
			}
			b.renderSQL(el, args)
		}
		b.writeByte(')')
	case sqlEq:
		b.renderSQL(v.Left, args)
		b.writeString(" = ")
		b.renderSQL(v.Right, args)
	case sqlLt:
		b.renderSQL(v.Left, args)
		b.writeString(" < ")
		b.renderSQL(v.Right, args)
	case sqlGt:
		b.renderSQL(v.Left, args)
		b.writeString(" > ")
		b.renderSQL(v.Right, args)
	case sqlIsNull:
		b.renderSQL(v.Operand, args)
		b.writeString(" IS NULL")
	case sqlIsNotNull:
		b.renderSQL(v.Operand, args)
		b.writeString(" IS NOT NULL")
	case sqlFragment:
		for _, el := range v.Els {
			b.renderSQL(el, args)
		}
	}
}

func (b *sqliteBackend) renderSQLQuery(stmt sqlQuery) (string, []any) {
	b.paramIndex = 0
	b.resetArgsBuffer(32)
	b.resetSQLBuffer(512)

	if len(stmt.Select) > 0 {
		b.writeString("SELECT ")
		for i, sel := range stmt.Select {
			if i > 0 {
				b.writeString(", ")
			}
			b.renderSQL(sel, &b.argsBuffer)
		}
	}

	b.writeString(" FROM ")
	b.renderSQL(stmt.FromTable, &b.argsBuffer)
	b.writeString(" AS ")
	b.renderSQL(stmt.FromAlias, &b.argsBuffer)

	for _, join := range stmt.Joins {
		b.writeByte(' ')
		b.writeString(join.Type)
		b.writeByte(' ')
		b.renderSQL(join.Table, &b.argsBuffer)
		b.writeString(" AS ")
		b.renderSQL(join.Alias, &b.argsBuffer)
		b.writeString(" ON ")
		b.renderSQL(join.On, &b.argsBuffer)
	}

	if stmt.Where != nil {
		b.writeString(" WHERE ")
		b.renderSQL(*stmt.Where, &b.argsBuffer)
	}

	if len(stmt.GroupBy) > 0 {
		b.writeString(" GROUP BY ")
		for i, gb := range stmt.GroupBy {
			if i > 0 {
				b.writeString(", ")
			}
			b.renderSQL(gb, &b.argsBuffer)
		}
	}

	if stmt.Having != nil {
		b.writeString(" HAVING ")
		b.renderSQL(*stmt.Having, &b.argsBuffer)
	}

	if len(stmt.OrderBys) > 0 {
		b.writeString(" ORDER BY ")
		for i, ob := range stmt.OrderBys {
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

	if stmt.Limit != nil {
		b.writeString(" LIMIT ")
		b.renderSQL(*stmt.Limit, &b.argsBuffer)
	}

	if stmt.Offset != nil {
		b.writeString(" OFFSET ")
		b.renderSQL(*stmt.Offset, &b.argsBuffer)
	}

	return b.sqlString(), b.argsBuffer
}

// renderSelect renders a CTE-based SELECT with JOIN pattern
// Query format: WITH keys (col1, col2) AS (VALUES (?,?), (?,?)) SELECT table.col1, table.col2 FROM table JOIN keys ON ...
func (b *sqliteBackend) renderSelect(stmt selectOp, chunk [][]any) (string, []any) {
	b.paramIndex = 0
	numKeyColumns := len(stmt.KeyColumns)
	b.resetArgsBuffer(len(chunk) * numKeyColumns)

	b.resetSQLBuffer(512)

	b.writeString("WITH keys (")
	for i, col := range stmt.KeyColumns {
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
		for j := range stmt.KeyColumns {
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
	for i, col := range stmt.Select {
		if i > 0 {
			b.writeString(", ")
		}
		b.quoteIdentifier(stmt.FromTable)
		b.writeByte('.')
		b.quoteIdentifier(col)
	}

	b.writeString(" FROM ")
	b.quoteIdentifier(stmt.FromTable)

	b.writeString(" JOIN keys ON ")
	for j := range stmt.KeyColumns {
		if j > 0 {
			b.writeString(" AND ")
		}
		b.quoteIdentifier(stmt.FromTable)
		b.writeByte('.')
		b.quoteIdentifier(stmt.KeyColumns[j])
		b.writeString(" = keys.")
		b.quoteIdentifier(stmt.KeyColumns[j])
	}

	return b.sqlString(), b.argsBuffer
}

// renderInsert renders a CTE-based INSERT SELECT pattern with RETURNING
// Query format: WITH new_rows (col1, col2) AS (VALUES (?,?), (?,?)) INSERT INTO table SELECT ... FROM new_rows RETURNING ...
func (b *sqliteBackend) renderInsert(stmt insertOp, chunk [][]any) (string, []any) {
	b.paramIndex = 0
	numColumns := len(stmt.Insert)
	b.resetArgsBuffer(len(chunk) * numColumns)

	b.resetSQLBuffer(512)

	b.writeString("WITH new_rows (")
	for i, col := range stmt.Insert {
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
		for j := range stmt.Insert {
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

	b.writeString("INSERT INTO ")
	b.quoteIdentifier(stmt.IntoTable)
	b.writeString(" (")
	for i, col := range stmt.Insert {
		if i > 0 {
			b.writeString(", ")
		}
		b.quoteIdentifier(col)
	}
	b.writeString(") SELECT ")
	for i, col := range stmt.Insert {
		if i > 0 {
			b.writeString(", ")
		}
		b.quoteIdentifier(col)
	}
	b.writeString(" FROM new_rows")

	if len(stmt.Returning) > 0 {
		b.writeString(" RETURNING ")
		for i, col := range stmt.Returning {
			if i > 0 {
				b.writeString(", ")
			}
			b.quoteIdentifier(col)
		}
	}

	return b.sqlString(), b.argsBuffer
}

// renderUpdate renders a CTE-based UPDATE FROM pattern
// Query format: WITH new_values (id, name, email) AS (VALUES (?,?,?), (?,?,?))
//
//	UPDATE table SET name = new_values.name FROM new_values WHERE table.id = new_values.id
func (b *sqliteBackend) renderUpdate(stmt updateOp, setChunk, whereChunk [][]any) (string, []any) {
	b.paramIndex = 0

	numColumns := len(stmt.Sets) + len(stmt.Where)
	b.resetArgsBuffer(len(setChunk) * numColumns)

	b.resetSQLBuffer(512)

	b.writeString("WITH new_values (")
	for i, col := range stmt.Sets {
		if i > 0 {
			b.writeString(", ")
		}
		b.quoteIdentifier(col)
	}
	for _, col := range stmt.Where {
		b.writeString(", ")
		b.quoteIdentifier(col)
	}
	b.writeString(") AS (VALUES ")

	for idx := range setChunk {
		if idx > 0 {
			b.writeString(", ")
		}
		b.writeByte('(')
		for j, val := range setChunk[idx] {
			if j > 0 {
				b.writeString(", ")
			}
			b.writeByte('?')
			b.paramIndex++
			b.argsBuffer = append(b.argsBuffer, val)
		}
		for _, val := range whereChunk[idx] {
			b.writeString(", ")
			b.writeByte('?')
			b.paramIndex++
			b.argsBuffer = append(b.argsBuffer, val)
		}
		b.writeByte(')')
	}
	b.writeString(") ")

	b.writeString("UPDATE ")
	b.quoteIdentifier(stmt.Table)
	b.writeString(" SET ")
	for i, col := range stmt.Sets {
		if i > 0 {
			b.writeString(", ")
		}
		b.quoteIdentifier(col)
		b.writeString(" = new_values.")
		b.quoteIdentifier(col)
	}

	b.writeString(" FROM new_values")

	b.writeString(" WHERE ")
	for i, col := range stmt.Where {
		if i > 0 {
			b.writeString(" AND ")
		}
		b.quoteIdentifier(stmt.Table)
		b.writeByte('.')
		b.quoteIdentifier(col)
		b.writeString(" = new_values.")
		b.quoteIdentifier(col)
	}

	return b.sqlString(), b.argsBuffer
}

// renderDelete renders a CTE-based DELETE with subquery pattern
// Query format: WITH keys (id) AS (VALUES (?), (?)) DELETE FROM table WHERE id IN (SELECT id FROM keys)
func (b *sqliteBackend) renderDelete(stmt deleteOp, chunk [][]any) (string, []any) {
	b.paramIndex = 0
	numKeyColumns := len(stmt.KeyColumns)
	b.resetArgsBuffer(len(chunk) * numKeyColumns)

	b.resetSQLBuffer(512)

	b.writeString("WITH keys (")
	for i, col := range stmt.KeyColumns {
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
		for j := range stmt.KeyColumns {
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
	b.quoteIdentifier(stmt.FromTable)
	b.writeString(" WHERE ")

	if numKeyColumns == 1 {
		b.quoteIdentifier(stmt.KeyColumns[0])
		b.writeString(" IN (SELECT ")
		b.quoteIdentifier(stmt.KeyColumns[0])
		b.writeString(" FROM keys)")
	} else {
		b.writeByte('(')
		for i, col := range stmt.KeyColumns {
			if i > 0 {
				b.writeString(", ")
			}
			b.quoteIdentifier(col)
		}
		b.writeString(") IN (SELECT ")
		for i, col := range stmt.KeyColumns {
			if i > 0 {
				b.writeString(", ")
			}
			b.quoteIdentifier(col)
		}
		b.writeString(" FROM keys)")
	}

	return b.sqlString(), b.argsBuffer
}

func (b *sqliteBackend) Select(ctx context.Context, stmt selectOp) (rows, error) {
	if len(stmt.Keys) == 0 {
		// Return empty result set for empty keys
		return &emptyRows{}, nil
	}
	// Convert Keys to [][]any for rendering
	values := make([][]any, len(stmt.Keys))
	for i, k := range stmt.Keys {
		values[i] = make([]any, k.Length())
		for j := 0; j < k.Length(); j++ {
			values[i][j] = k.At(j)
		}
	}

	query, args := b.renderSelect(stmt, values)
	return b.queryContext(ctx, query, args...)
}

func (b *sqliteBackend) Insert(ctx context.Context, stmt insertOp) (rows, error) {
	if len(stmt.Values) == 0 {
		// Return empty result set for empty batch
		return &emptyRows{}, nil
	}
	query, args := b.renderInsert(stmt, stmt.Values)
	return b.queryContext(ctx, query, args...)
}

func (b *sqliteBackend) Update(ctx context.Context, stmt updateOp) error {
	if len(stmt.SetValues) == 0 {
		// No-op for empty batch
		return nil
	}
	query, args := b.renderUpdate(stmt, stmt.SetValues, stmt.WhereValues)
	_, err := b.execContext(ctx, query, args...)
	return err
}

func (b *sqliteBackend) Delete(ctx context.Context, stmt deleteOp) error {
	if len(stmt.Keys) == 0 {
		// No-op for empty batch
		return nil
	}
	// Convert Keys to [][]any for rendering
	values := make([][]any, len(stmt.Keys))
	for i, k := range stmt.Keys {
		values[i] = make([]any, k.Length())
		for j := 0; j < k.Length(); j++ {
			values[i][j] = k.At(j)
		}
	}

	query, args := b.renderDelete(stmt, values)

	_, err := b.execContext(ctx, query, args...)
	return err
}

func (b *sqliteBackend) FetchQuery(ctx context.Context, stmt sqlQuery) (rows, error) {
	query, args := b.renderSQLQuery(stmt)
	return b.queryContext(ctx, query, args...)
}

func (b *sqliteBackend) CountQuery(ctx context.Context, stmt sqlQuery) (int64, error) {
	innerStmt := stmt
	innerStmt.Limit = nil
	innerStmt.Offset = nil

	innerQuery, args := b.renderSQLQuery(innerStmt)
	countQuery := fmt.Sprintf("SELECT COUNT(*) FROM (%s) AS count_subquery", innerQuery)

	var count int64
	err := b.queryRowContext(ctx, countQuery, args...).Scan(&count)
	return count, err
}
