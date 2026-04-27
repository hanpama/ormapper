package sqlitetest

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"

	agg "github.com/hanpama/agg"
	_ "modernc.org/sqlite"
)

type benchOrder struct {
	ID         int64 `agg:"auto"`
	CustomerID int64
	Total      float64
	Items      []*benchOrderItem
	Notes      []*benchOrderNote
}

type benchOrderItem struct {
	ID      int64 `agg:"auto"`
	OrderID int64 `agg:"parental"`
	Name    string
	Qty     int
}

type benchOrderNote struct {
	ID      int64 `agg:"auto"`
	OrderID int64 `agg:"parental"`
	Body    string
}

func setupBenchDB(b *testing.B) *sql.DB {
	b.Helper()
	name := strings.ReplaceAll(b.Name(), "/", "_")
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", name)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		b.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	b.Cleanup(func() { _ = db.Close() })

	for _, stmt := range []string{
		`CREATE TABLE orders (id INTEGER PRIMARY KEY AUTOINCREMENT, customer_id INTEGER NOT NULL, total REAL NOT NULL)`,
		`CREATE TABLE order_items (id INTEGER PRIMARY KEY AUTOINCREMENT, order_id INTEGER NOT NULL REFERENCES orders(id), name TEXT NOT NULL, qty INTEGER NOT NULL)`,
		`CREATE TABLE order_notes (id INTEGER PRIMARY KEY AUTOINCREMENT, order_id INTEGER NOT NULL REFERENCES orders(id), body TEXT NOT NULL)`,
	} {
		if _, err := db.ExecContext(context.Background(), stmt); err != nil {
			b.Fatal(err)
		}
	}
	return db
}

func benchMapper() *agg.Mapper {
	return agg.MustCompile(
		agg.SQLite,
		agg.Map(&benchOrder{}, agg.WithTable("orders")),
		agg.Map(&benchOrderItem{}, agg.WithTable("order_items")),
		agg.Map(&benchOrderNote{}, agg.WithTable("order_notes")),
	)
}

func newBenchOrder() *benchOrder {
	return &benchOrder{
		CustomerID: 1,
		Total:      299.97,
		Items: []*benchOrderItem{
			{Name: "A", Qty: 1},
			{Name: "B", Qty: 2},
			{Name: "C", Qty: 3},
		},
		Notes: []*benchOrderNote{
			{Body: "note 1"},
			{Body: "note 2"},
		},
	}
}

func BenchmarkAggregate_Insert(b *testing.B) {
	db := setupBenchDB(b)
	mapper := benchMapper()
	ctx := context.Background()

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if err := mapper.Save(ctx, db, newBenchOrder()); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkAggregate_Select(b *testing.B) {
	db := setupBenchDB(b)
	mapper := benchMapper()
	ctx := context.Background()

	ids := make([]int64, 100)
	for i := range ids {
		o := newBenchOrder()
		if err := mapper.Save(ctx, db, o); err != nil {
			b.Fatal(err)
		}
		ids[i] = o.ID
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		var order *benchOrder
		if err := mapper.Get(ctx, db, &order, agg.NewKey(ids[i%len(ids)])); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkAggregate_Update(b *testing.B) {
	db := setupBenchDB(b)
	mapper := benchMapper()
	ctx := context.Background()

	ids := make([]int64, 100)
	for i := range ids {
		o := newBenchOrder()
		if err := mapper.Save(ctx, db, o); err != nil {
			b.Fatal(err)
		}
		ids[i] = o.ID
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			b.Fatal(err)
		}
		var order *benchOrder
		if err := mapper.Get(ctx, tx, &order, agg.NewKey(ids[i%len(ids)])); err != nil {
			_ = tx.Rollback()
			b.Fatal(err)
		}
		order.Total += 1
		if len(order.Items) > 1 {
			order.Items = order.Items[:len(order.Items)-1]
		}
		order.Items = append(order.Items, &benchOrderItem{Name: "New", Qty: 1})
		if err := mapper.Save(ctx, tx, order); err != nil {
			_ = tx.Rollback()
			b.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkAggregate_Delete(b *testing.B) {
	db := setupBenchDB(b)
	mapper := benchMapper()
	ctx := context.Background()

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		o := newBenchOrder()
		if err := mapper.Save(ctx, db, o); err != nil {
			b.Fatal(err)
		}
		b.StartTimer()

		if err := mapper.Delete(ctx, db, &benchOrder{ID: o.ID}); err != nil {
			b.Fatal(err)
		}
	}
}
