package shared

import (
	"database/sql"
	"fmt"
	"os"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func GetPostgresDB() (*sql.DB, error) {
	dsn := os.Getenv("AGG_BENCH_POSTGRES_DSN")
	if dsn == "" {
		dsn = "postgres://agg:agg@localhost:17432/agg?sslmode=disable"
	}

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("open postgres: %w", err)
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}

	db.SetMaxOpenConns(16)
	db.SetMaxIdleConns(16)
	return db, nil
}

func SetupPostgresSchema(db *sql.DB) error {
	_, err := db.Exec(`
		DROP TABLE IF EXISTS order_item_lots;
		DROP TABLE IF EXISTS order_notes;
		DROP TABLE IF EXISTS order_items;
		DROP TABLE IF EXISTS orders;
		DROP TABLE IF EXISTS users;

		CREATE TABLE users (
			id BIGSERIAL PRIMARY KEY,
			name TEXT NOT NULL,
			email TEXT NOT NULL,
			age INTEGER NOT NULL
		);

		CREATE TABLE orders (
			id BIGSERIAL PRIMARY KEY,
			customer TEXT NOT NULL,
			total DOUBLE PRECISION NOT NULL
		);

		CREATE TABLE order_items (
			id BIGSERIAL PRIMARY KEY,
			order_id BIGINT NOT NULL REFERENCES orders(id),
			product TEXT NOT NULL,
			quantity INTEGER NOT NULL,
			price DOUBLE PRECISION NOT NULL
		);

		CREATE TABLE order_notes (
			id BIGSERIAL PRIMARY KEY,
			order_id BIGINT NOT NULL REFERENCES orders(id),
			content TEXT NOT NULL
		);
	`)
	return err
}

func CleanupPostgresTables(db *sql.DB) error {
	_, err := db.Exec(`
		TRUNCATE TABLE order_notes, order_items, orders, users RESTART IDENTITY CASCADE;
	`)
	return err
}
