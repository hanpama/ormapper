package sqlitetest

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"

	ormapper "github.com/hanpama/ormapper"
	"github.com/hanpama/ormapper/tests/contracts"
	_ "modernc.org/sqlite"
)

func TestSQLiteContracts(t *testing.T) {
	name := strings.ReplaceAll(t.Name(), "/", "_")
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", name)

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	t.Cleanup(func() {
		_ = db.Close()
	})

	contracts.Run(t, contracts.Fixture{
		Name:        "sqlite",
		DB:          db,
		Dialect:     ormapper.SQLite,
		ResetSchema: resetSQLiteSchema,
		Placeholder: func(int) string { return "?" },
	})
}

func resetSQLiteSchema(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()

	schema := []string{
		`PRAGMA foreign_keys = OFF`,
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
		`PRAGMA foreign_keys = ON`,
		`CREATE TABLE simple_auto (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
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
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			"select" TEXT NOT NULL
		)`,
		`CREATE TABLE orders (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			customer_id INTEGER NOT NULL,
			total REAL NOT NULL
		)`,
		`CREATE TABLE order_items (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			order_id INTEGER NOT NULL REFERENCES orders(id),
			name TEXT NOT NULL,
			qty INTEGER NOT NULL
		)`,
		`CREATE TABLE order_item_lots (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			order_item_id INTEGER NOT NULL REFERENCES order_items(id),
			code TEXT NOT NULL
		)`,
		`CREATE TABLE order_notes (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			order_id INTEGER NOT NULL REFERENCES orders(id),
			body TEXT NOT NULL
		)`,
		`CREATE TABLE identifying_roots (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL
		)`,
		`CREATE TABLE identifying_details (
			root_id INTEGER PRIMARY KEY REFERENCES identifying_roots(id),
			body TEXT NOT NULL
		)`,
	}

	for _, stmt := range schema {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("reset schema statement %q: %v", stmt, err)
		}
	}
}
