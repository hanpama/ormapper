package sqlitetest

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	agg "github.com/hanpama/agg"
	"github.com/hanpama/agg/tests/contracts"
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
		Dialect:     agg.SQLite,
		ResetSchema: resetSQLiteSchema,
		Placeholder: func(int) string { return "?" },
	})
}

// --- Converter tests ---

type Status int

const (
	StatusActive   Status = 0
	StatusInactive Status = 1
)

func statusToDB(s Status) (string, error) {
	switch s {
	case StatusActive:
		return "active", nil
	case StatusInactive:
		return "inactive", nil
	default:
		return "", fmt.Errorf("unknown status: %d", s)
	}
}

func statusFromDB(s string) (Status, error) {
	switch s {
	case "active":
		return StatusActive, nil
	case "inactive":
		return StatusInactive, nil
	default:
		return 0, fmt.Errorf("unknown status: %q", s)
	}
}

type Address struct {
	City   string `json:"city"`
	Street string `json:"street"`
}

func addressToDB(a Address) (string, error) {
	b, err := json.Marshal(a)
	return string(b), err
}

func addressFromDB(s string) (Address, error) {
	var a Address
	err := json.Unmarshal([]byte(s), &a)
	return a, err
}

type converterEntity struct {
	ID      int64 `agg:"auto"`
	Status  Status
	Address Address
}

type staleUpdateEntity struct {
	ID   int64 `agg:"auto"`
	Name string
}

func TestConverterEnumRoundTrip(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	exec(t, db, `CREATE TABLE converter_entities (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		status TEXT NOT NULL,
		address TEXT NOT NULL
	)`)

	mapper := agg.MustCompile(agg.SQLite,
		agg.Map(&converterEntity{}, agg.WithTable("converter_entities"),
			agg.WithConverter("Status", statusToDB, statusFromDB),
			agg.WithConverter("Address", addressToDB, addressFromDB),
		),
	)

	// Insert
	entity := &converterEntity{
		Status:  StatusActive,
		Address: Address{City: "Seoul", Street: "Gangnam"},
	}
	if err := mapper.Save(ctx, db, entity); err != nil {
		t.Fatalf("Save insert: %v", err)
	}
	if entity.ID == 0 {
		t.Fatal("expected auto ID")
	}

	// Verify DB has text values
	var dbStatus, dbAddress string
	if err := db.QueryRowContext(ctx, "SELECT status, address FROM converter_entities WHERE id = ?", entity.ID).Scan(&dbStatus, &dbAddress); err != nil {
		t.Fatalf("raw query: %v", err)
	}
	if dbStatus != "active" {
		t.Fatalf("expected DB status 'active', got %q", dbStatus)
	}
	if !strings.Contains(dbAddress, "Seoul") {
		t.Fatalf("expected DB address to contain 'Seoul', got %q", dbAddress)
	}

	// Load
	var loaded *converterEntity
	if err := mapper.Get(ctx, db, &loaded, agg.NewKey(entity.ID)); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if loaded.Status != StatusActive {
		t.Fatalf("expected StatusActive, got %d", loaded.Status)
	}
	if loaded.Address.City != "Seoul" || loaded.Address.Street != "Gangnam" {
		t.Fatalf("expected Address{Seoul, Gangnam}, got %+v", loaded.Address)
	}

	// Update
	loaded.Status = StatusInactive
	loaded.Address.City = "Busan"
	if err := mapper.Save(ctx, db, loaded); err != nil {
		t.Fatalf("Save update: %v", err)
	}

	// Verify update in DB
	if err := db.QueryRowContext(ctx, "SELECT status, address FROM converter_entities WHERE id = ?", loaded.ID).Scan(&dbStatus, &dbAddress); err != nil {
		t.Fatalf("raw query after update: %v", err)
	}
	if dbStatus != "inactive" {
		t.Fatalf("expected DB status 'inactive', got %q", dbStatus)
	}
	if !strings.Contains(dbAddress, "Busan") {
		t.Fatalf("expected DB address to contain 'Busan', got %q", dbAddress)
	}

	// Reload and verify
	var reloaded *converterEntity
	if err := mapper.Get(ctx, db, &reloaded, agg.NewKey(loaded.ID)); err != nil {
		t.Fatalf("Get after update: %v", err)
	}
	if reloaded.Status != StatusInactive {
		t.Fatalf("expected StatusInactive, got %d", reloaded.Status)
	}
	if reloaded.Address.City != "Busan" {
		t.Fatalf("expected Busan, got %s", reloaded.Address.City)
	}
}

func TestSQLiteUpdateDetectsDeletedRow(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	exec(t, db, `CREATE TABLE stale_update_entities (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL
	)`)

	mapper := agg.MustCompile(agg.SQLite,
		agg.Map(&staleUpdateEntity{}, agg.WithTable("stale_update_entities")),
	)
	entity := &staleUpdateEntity{Name: "created"}
	if err := mapper.Save(ctx, db, entity); err != nil {
		t.Fatalf("Save insert: %v", err)
	}

	exec(t, db, `CREATE TRIGGER delete_before_stale_update
		BEFORE UPDATE ON stale_update_entities
		BEGIN
			DELETE FROM stale_update_entities WHERE id = OLD.id;
			SELECT RAISE(IGNORE);
		END
	`)

	entity.Name = "updated"
	if err := mapper.Save(ctx, db, entity); !errors.Is(err, agg.ErrStaleEntity) {
		t.Fatalf("expected stale entity error, got %v", err)
	}
}

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	name := strings.ReplaceAll(t.Name(), "/", "_")
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", name)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func exec(t *testing.T, db *sql.DB, query string) {
	t.Helper()
	if _, err := db.ExecContext(context.Background(), query); err != nil {
		t.Fatal(err)
	}
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
