// Package agg persists plain Go aggregates with a small public API.
//
// The intended workflow is:
//
//  1. Compile a Mapper from your entity structs.
//  2. Pass context plus *sql.DB or *sql.Tx to each operation.
//  3. Use Get, Save, Delete, and their Many variants for aggregate persistence.
//  4. Use NewQuery for typed reads.
//
// agg does not expose a long-lived session. The caller owns transaction
// scope by deciding whether each call receives a *sql.DB or a *sql.Tx.
//
// # Struct tags
//
// Fields are configured with the `agg` struct tag:
//
//   - `agg:"auto"` — database-generated field (skip insert/update, backfill from RETURNING).
//   - `agg:"primary"` — explicit primary key (default: a field named ID).
//   - `agg:"parental"` — child foreign key pointing to the parent.
//   - `agg:"column:name"` — override the column name (default: snake_case of field name).
//   - `agg:"skip_insert"` — exclude from INSERT.
//   - `agg:"skip_update"` — exclude from UPDATE.
//   - `agg:"-"` — ignore the field entirely.
//
// Tags can be combined: `agg:"primary,parental"`.
//
// # Mapping options
//
//   - WithTable overrides the table name.
//   - WithSchema sets the schema prefix.
//   - WithConverter registers a bidirectional type converter for a field.
//
// Registered *Child and []*Child fields are treated as aggregate children
// automatically when the child type is also compiled.
//
// # Save semantics
//
// Save treats the input as the authoritative aggregate snapshot. It inserts rows
// with new manual keys, updates rows with existing keys, and deletes children
// missing from the input value. For auto primary keys, zero means insert and
// non-zero means update.
//
// # Example
//
//	type Order struct {
//	    ID    int64 `agg:"auto"`
//	    Total int64
//	    Items []*OrderItem
//	}
//
//	type OrderItem struct {
//	    ID      int64 `agg:"auto"`
//	    OrderID int64 `agg:"parental"`
//	    Name    string
//	    Qty     int
//	}
//
//	mapper := MustCompile(
//	    Postgres,
//	    Map(&Order{}, WithTable("orders")),
//	    Map(&OrderItem{}, WithTable("order_items")),
//	)
//
//	var order *Order
//	if err := mapper.Get(ctx, tx, &order, NewKey(id)); err != nil {
//	    return err
//	}
//
//	order.Total = 42
//	if err := mapper.Save(ctx, tx, order); err != nil {
//	    return err
//	}
//
//	query := NewQuery[Order](mapper, tx, "o")
//	found, err := query.Where("o.total > ?", 0).FetchAll(ctx)
package agg

// Postgres renders PostgreSQL SQL.
var Postgres Dialect = postgresDialect{}

// SQLite renders SQLite SQL.
var SQLite Dialect = sqliteDialect{}
