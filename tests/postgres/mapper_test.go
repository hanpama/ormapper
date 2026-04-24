package postgrestest

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	ormapper "github.com/hanpama/ormapper"
	"github.com/hanpama/ormapper/tests/contracts"
	_ "github.com/jackc/pgx/v5/stdlib"
)

func TestPostgresContracts(t *testing.T) {
	dsn := os.Getenv("ORMAPPER_POSTGRES_DSN")
	if dsn == "" {
		dsn = "postgres://ormapper:ormapper@localhost:17432/ormapper?sslmode=disable"
	}

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open postgres: %v", err)
	}
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(4)
	t.Cleanup(func() {
		_ = db.Close()
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		t.Skipf("postgres is not available: %v; start it with `docker compose -f ../docker-compose.yml up -d`", err)
	}

	contracts.Run(t, contracts.Fixture{
		Name:        "postgres",
		DB:          db,
		Dialect:     ormapper.Postgres,
		ResetSchema: resetPostgresSchema,
		Placeholder: func(n int) string {
			return fmt.Sprintf("$%d", n)
		},
	})
}

func resetPostgresSchema(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()

	schema := []string{
		`DROP TABLE IF EXISTS order_item_lots`,
		`DROP TABLE IF EXISTS order_items`,
		`DROP TABLE IF EXISTS order_notes`,
		`DROP TABLE IF EXISTS orders`,
		`DROP TABLE IF EXISTS identifying_details`,
		`DROP TABLE IF EXISTS identifying_roots`,
		`DROP TABLE IF EXISTS "special quote"`,
		`DROP TABLE IF EXISTS composite`,
		`DROP TABLE IF EXISTS simple_uuid`,
		`DROP TABLE IF EXISTS simple_auto`,
		`CREATE TABLE simple_auto (
			id BIGSERIAL PRIMARY KEY,
			name TEXT NOT NULL,
			nullable TEXT NULL,
			auto_generated INTEGER NOT NULL DEFAULT 42
		)`,
		`CREATE TABLE simple_uuid (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			nullable TEXT NULL,
			auto_generated INTEGER NOT NULL DEFAULT 42
		)`,
		`CREATE TABLE composite (
			key1 INTEGER NOT NULL,
			key2 INTEGER NOT NULL,
			name TEXT NOT NULL,
			nullable TEXT NULL,
			PRIMARY KEY (key1, key2)
		)`,
		`CREATE TABLE "special quote" (
			id BIGSERIAL PRIMARY KEY,
			"select" TEXT NOT NULL
		)`,
		`CREATE TABLE orders (
			id BIGSERIAL PRIMARY KEY,
			customer_id BIGINT NOT NULL,
			total DOUBLE PRECISION NOT NULL
		)`,
		`CREATE TABLE order_items (
			id BIGSERIAL PRIMARY KEY,
			order_id BIGINT NOT NULL REFERENCES orders(id),
			name TEXT NOT NULL,
			qty INTEGER NOT NULL
		)`,
		`CREATE TABLE order_item_lots (
			id BIGSERIAL PRIMARY KEY,
			order_item_id BIGINT NOT NULL REFERENCES order_items(id),
			code TEXT NOT NULL
		)`,
		`CREATE TABLE order_notes (
			id BIGSERIAL PRIMARY KEY,
			order_id BIGINT NOT NULL REFERENCES orders(id),
			body TEXT NOT NULL
		)`,
		`CREATE TABLE identifying_roots (
			id BIGSERIAL PRIMARY KEY,
			name TEXT NOT NULL
		)`,
		`CREATE TABLE identifying_details (
			root_id BIGINT PRIMARY KEY REFERENCES identifying_roots(id),
			body TEXT NOT NULL
		)`,
	}

	for _, stmt := range schema {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("reset schema statement %q: %v", stmt, err)
		}
	}
}
