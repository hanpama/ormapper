package tests

import (
	"context"
	"database/sql"
	"testing"

	"github.com/hanpama/agg/benchmark/shared"
)

func setupPostgresDB(b *testing.B) (*sql.DB, func()) {
	b.Helper()

	db, err := shared.GetPostgresDB()
	if err != nil {
		b.Skipf("PostgreSQL not available: %v", err)
	}
	if err := shared.SetupPostgresSchema(db); err != nil {
		_ = db.Close()
		b.Fatal(err)
	}

	cleanup := func() {
		_ = shared.CleanupPostgresTables(db)
		_ = db.Close()
	}
	return db, cleanup
}

func seedUsers(b *testing.B, ctx context.Context, db *sql.DB, n int) []int64 {
	b.Helper()

	ids := make([]int64, n)
	for i := 0; i < n; i++ {
		err := db.QueryRowContext(
			ctx,
			"INSERT INTO users (name, email, age) VALUES ($1, $2, $3) RETURNING id",
			"User",
			"user@example.com",
			20+(i%50),
		).Scan(&ids[i])
		if err != nil {
			b.Fatal(err)
		}
	}
	return ids
}

func seedOrders(b *testing.B, ctx context.Context, db *sql.DB, n int) []int64 {
	b.Helper()

	ids := make([]int64, n)
	for i := 0; i < n; i++ {
		order := shared.NewOrder()
		err := db.QueryRowContext(
			ctx,
			"INSERT INTO orders (customer, total) VALUES ($1, $2) RETURNING id",
			order.Customer,
			order.Total,
		).Scan(&ids[i])
		if err != nil {
			b.Fatal(err)
		}

		for _, item := range order.Items {
			if _, err := db.ExecContext(
				ctx,
				"INSERT INTO order_items (order_id, product, quantity, price) VALUES ($1, $2, $3, $4)",
				ids[i],
				item.Product,
				item.Quantity,
				item.Price,
			); err != nil {
				b.Fatal(err)
			}
		}
		for _, note := range order.Notes {
			if _, err := db.ExecContext(
				ctx,
				"INSERT INTO order_notes (order_id, content) VALUES ($1, $2)",
				ids[i],
				note.Content,
			); err != nil {
				b.Fatal(err)
			}
		}
	}
	return ids
}
