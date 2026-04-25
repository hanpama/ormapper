// Package ormapper persists plain Go aggregates with a small public API.
//
// The intended workflow is:
//
//  1. Compile a Mapper from your entity structs.
//  2. Pass context plus *sql.DB or *sql.Tx to each operation.
//  3. Use Get, Save, Delete, and their Many variants for aggregate persistence.
//  4. Use NewQuery for typed reads.
//
// ormapper does not expose a long-lived session. The caller owns transaction
// scope by deciding whether each call receives a *sql.DB or a *sql.Tx.
//
// Mapping rules:
//
//   - The default table name is the snake_case struct name.
//   - A field named ID is treated as the primary key by default.
//   - The tag `ormapper:"auto"` marks a field as database-generated.
//   - The tag `ormapper:"parental"` marks the child foreign key that points to the parent.
//   - Registered `*Child` and `[]*Child` fields are treated as aggregate children.
//   - Map(..., WithTable(...), WithSchema(...)) overrides table naming.
//
// Save semantics:
//
// Save treats the input as the authoritative aggregate snapshot. It inserts rows
// with new manual keys, updates rows with existing keys, and deletes children
// missing from the input value. For auto primary keys, zero means insert and
// non-zero means update.
//
// Example:
//
//	type Order struct {
//	    ID    int64 `ormapper:"auto"`
//	    Total int64
//	    Items []*OrderItem
//	}
//
//	type OrderItem struct {
//	    ID      int64 `ormapper:"auto"`
//	    OrderID int64 `ormapper:"parental"`
//	    Name    string
//	    Qty     int
//	}
//
//	mapper := ormapper.MustCompile(
//	    ormapper.Postgres,
//	    ormapper.Map(&Order{}, ormapper.WithTable("orders")),
//	    ormapper.Map(&OrderItem{}, ormapper.WithTable("order_items")),
//	)
//
//	var order *Order
//	if err := mapper.Get(ctx, tx, &order, ormapper.NewKey(id)); err != nil {
//	    return err
//	}
//
//	order.Total = 42
//	if err := mapper.Save(ctx, tx, order); err != nil {
//	    return err
//	}
//
//	query := ormapper.NewQuery[Order](mapper, tx, "o")
//	found, err := query.Where("o.total > ?", 0).FetchAll(ctx)
package ormapper

// Postgres renders PostgreSQL SQL.
var Postgres Dialect = postgresDialect{}

// SQLite renders SQLite SQL.
var SQLite Dialect = sqliteDialect{}
