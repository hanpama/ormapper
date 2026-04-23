package tests

import (
	"context"
	"database/sql"
	"testing"

	"github.com/hanpama/ormapper/benchmark/shared"
)

func BenchmarkRaw_Postgres_Simple_Insert(b *testing.B) {
	db, cleanup := setupPostgresDB(b)
	defer cleanup()
	ctx := context.Background()

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		if _, err := db.ExecContext(
			ctx,
			"INSERT INTO users (name, email, age) VALUES ($1, $2, $3)",
			"Alice",
			"alice@example.com",
			30,
		); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRaw_Postgres_Simple_Select(b *testing.B) {
	db, cleanup := setupPostgresDB(b)
	defer cleanup()
	ctx := context.Background()
	ids := seedUsers(b, ctx, db, 100)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		var user shared.User
		if err := db.QueryRowContext(
			ctx,
			"SELECT id, name, email, age FROM users WHERE id = $1",
			ids[i%len(ids)],
		).Scan(&user.ID, &user.Name, &user.Email, &user.Age); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRaw_Postgres_Simple_Update(b *testing.B) {
	db, cleanup := setupPostgresDB(b)
	defer cleanup()
	ctx := context.Background()
	ids := seedUsers(b, ctx, db, 100)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		var user shared.User
		if err := db.QueryRowContext(
			ctx,
			"SELECT id, name, email, age FROM users WHERE id = $1",
			ids[i%len(ids)],
		).Scan(&user.ID, &user.Name, &user.Email, &user.Age); err != nil {
			b.Fatal(err)
		}
		user.Age = 30
		if _, err := db.ExecContext(
			ctx,
			"UPDATE users SET name = $1, email = $2, age = $3 WHERE id = $4",
			user.Name,
			user.Email,
			user.Age,
			user.ID,
		); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRaw_Postgres_Simple_ReadSlice(b *testing.B) {
	db, cleanup := setupPostgresDB(b)
	defer cleanup()
	ctx := context.Background()
	_ = seedUsers(b, ctx, db, 100)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		rows, err := db.QueryContext(ctx, "SELECT id, name, email, age FROM users WHERE id > $1 LIMIT 100", 0)
		if err != nil {
			b.Fatal(err)
		}

		users := make([]*shared.User, 0, 100)
		for rows.Next() {
			user := &shared.User{}
			if err := rows.Scan(&user.ID, &user.Name, &user.Email, &user.Age); err != nil {
				_ = rows.Close()
				b.Fatal(err)
			}
			users = append(users, user)
		}
		if err := rows.Close(); err != nil {
			b.Fatal(err)
		}
		if len(users) != 100 {
			b.Fatalf("expected 100 users, got %d", len(users))
		}
	}
}

func BenchmarkRaw_Postgres_Simple_Delete(b *testing.B) {
	db, cleanup := setupPostgresDB(b)
	defer cleanup()
	ctx := context.Background()

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		b.StopTimer()
		var id int64
		if err := db.QueryRowContext(
			ctx,
			"INSERT INTO users (name, email, age) VALUES ($1, $2, $3) RETURNING id",
			"DeleteMe",
			"delete@example.com",
			99,
		).Scan(&id); err != nil {
			b.Fatal(err)
		}
		b.StartTimer()

		if _, err := db.ExecContext(ctx, "DELETE FROM users WHERE id = $1", id); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRaw_Postgres_Aggregate_Insert(b *testing.B) {
	db, cleanup := setupPostgresDB(b)
	defer cleanup()
	ctx := context.Background()

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			b.Fatal(err)
		}
		if err := rawInsertOrder(ctx, tx, shared.NewOrder()); err != nil {
			_ = tx.Rollback()
			b.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRaw_Postgres_Aggregate_Select(b *testing.B) {
	db, cleanup := setupPostgresDB(b)
	defer cleanup()
	ctx := context.Background()
	ids := seedOrders(b, ctx, db, 100)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		if _, err := rawLoadOrder(ctx, db, ids[i%len(ids)]); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRaw_Postgres_Aggregate_Update(b *testing.B) {
	db, cleanup := setupPostgresDB(b)
	defer cleanup()
	ctx := context.Background()
	ids := seedOrders(b, ctx, db, 100)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			b.Fatal(err)
		}
		order, err := rawLoadOrder(ctx, tx, ids[i%len(ids)])
		if err != nil {
			_ = tx.Rollback()
			b.Fatal(err)
		}
		shared.MutateOrder(order)
		if err := rawSaveOrderSnapshot(ctx, tx, order); err != nil {
			_ = tx.Rollback()
			b.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRaw_Postgres_Aggregate_ReadSlice(b *testing.B) {
	db, cleanup := setupPostgresDB(b)
	defer cleanup()
	ctx := context.Background()
	_ = seedOrders(b, ctx, db, 100)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		orders, err := rawReadOrderSlice(ctx, db, 100)
		if err != nil {
			b.Fatal(err)
		}
		if len(orders) != 100 {
			b.Fatalf("expected 100 orders, got %d", len(orders))
		}
	}
}

func BenchmarkRaw_Postgres_Aggregate_Delete(b *testing.B) {
	db, cleanup := setupPostgresDB(b)
	defer cleanup()
	ctx := context.Background()

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		b.StopTimer()
		order := shared.NewOrder()
		if err := rawInsertOrder(ctx, db, order); err != nil {
			b.Fatal(err)
		}
		b.StartTimer()

		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			b.Fatal(err)
		}
		if err := rawDeleteOrder(ctx, tx, order.ID); err != nil {
			_ = tx.Rollback()
			b.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			b.Fatal(err)
		}
	}
}

type rawDBTX interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func rawInsertOrder(ctx context.Context, db rawDBTX, order *shared.Order) error {
	if err := db.QueryRowContext(
		ctx,
		"INSERT INTO orders (customer, total) VALUES ($1, $2) RETURNING id",
		order.Customer,
		order.Total,
	).Scan(&order.ID); err != nil {
		return err
	}

	for _, item := range order.Items {
		item.OrderID = order.ID
		if err := db.QueryRowContext(
			ctx,
			"INSERT INTO order_items (order_id, product, quantity, price) VALUES ($1, $2, $3, $4) RETURNING id",
			item.OrderID,
			item.Product,
			item.Quantity,
			item.Price,
		).Scan(&item.ID); err != nil {
			return err
		}
	}
	for _, note := range order.Notes {
		note.OrderID = order.ID
		if err := db.QueryRowContext(
			ctx,
			"INSERT INTO order_notes (order_id, content) VALUES ($1, $2) RETURNING id",
			note.OrderID,
			note.Content,
		).Scan(&note.ID); err != nil {
			return err
		}
	}

	return nil
}

func rawLoadOrder(ctx context.Context, db rawDBTX, id int64) (*shared.Order, error) {
	order := &shared.Order{}
	if err := db.QueryRowContext(
		ctx,
		"SELECT id, customer, total FROM orders WHERE id = $1",
		id,
	).Scan(&order.ID, &order.Customer, &order.Total); err != nil {
		return nil, err
	}

	items, err := rawLoadItems(ctx, db, id)
	if err != nil {
		return nil, err
	}
	notes, err := rawLoadNotes(ctx, db, id)
	if err != nil {
		return nil, err
	}
	order.Items = items
	order.Notes = notes
	return order, nil
}

func rawLoadItems(ctx context.Context, db rawDBTX, orderID int64) ([]*shared.OrderItem, error) {
	rows, err := db.QueryContext(
		ctx,
		"SELECT id, order_id, product, quantity, price FROM order_items WHERE order_id = $1",
		orderID,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	items := make([]*shared.OrderItem, 0, 4)
	for rows.Next() {
		item := &shared.OrderItem{}
		if err := rows.Scan(&item.ID, &item.OrderID, &item.Product, &item.Quantity, &item.Price); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, nil
}

func rawLoadNotes(ctx context.Context, db rawDBTX, orderID int64) ([]*shared.OrderNote, error) {
	rows, err := db.QueryContext(
		ctx,
		"SELECT id, order_id, content FROM order_notes WHERE order_id = $1",
		orderID,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	notes := make([]*shared.OrderNote, 0, 2)
	for rows.Next() {
		note := &shared.OrderNote{}
		if err := rows.Scan(&note.ID, &note.OrderID, &note.Content); err != nil {
			return nil, err
		}
		notes = append(notes, note)
	}
	return notes, nil
}

func rawSaveOrderSnapshot(ctx context.Context, db rawDBTX, order *shared.Order) error {
	if _, err := db.ExecContext(
		ctx,
		"UPDATE orders SET customer = $1, total = $2 WHERE id = $3",
		order.Customer,
		order.Total,
		order.ID,
	); err != nil {
		return err
	}

	keepItemIDs := make([]int64, 0, len(order.Items))
	for _, item := range order.Items {
		item.OrderID = order.ID
		if item.ID == 0 {
			if err := db.QueryRowContext(
				ctx,
				"INSERT INTO order_items (order_id, product, quantity, price) VALUES ($1, $2, $3, $4) RETURNING id",
				item.OrderID,
				item.Product,
				item.Quantity,
				item.Price,
			).Scan(&item.ID); err != nil {
				return err
			}
		} else if _, err := db.ExecContext(
			ctx,
			"UPDATE order_items SET product = $1, quantity = $2, price = $3 WHERE id = $4",
			item.Product,
			item.Quantity,
			item.Price,
			item.ID,
		); err != nil {
			return err
		}
		keepItemIDs = append(keepItemIDs, item.ID)
	}

	if len(keepItemIDs) == 0 {
		if _, err := db.ExecContext(ctx, "DELETE FROM order_items WHERE order_id = $1", order.ID); err != nil {
			return err
		}
	} else if _, err := db.ExecContext(
		ctx,
		"DELETE FROM order_items WHERE order_id = $1 AND NOT (id = ANY($2))",
		order.ID,
		keepItemIDs,
	); err != nil {
		return err
	}

	for _, note := range order.Notes {
		note.OrderID = order.ID
		if note.ID == 0 {
			if err := db.QueryRowContext(
				ctx,
				"INSERT INTO order_notes (order_id, content) VALUES ($1, $2) RETURNING id",
				note.OrderID,
				note.Content,
			).Scan(&note.ID); err != nil {
				return err
			}
		} else if _, err := db.ExecContext(
			ctx,
			"UPDATE order_notes SET content = $1 WHERE id = $2",
			note.Content,
			note.ID,
		); err != nil {
			return err
		}
	}

	return nil
}

func rawReadOrderSlice(ctx context.Context, db rawDBTX, limit int) ([]*shared.Order, error) {
	rows, err := db.QueryContext(ctx, "SELECT id, customer, total FROM orders WHERE id > $1 LIMIT $2", 0, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	orders := make([]*shared.Order, 0, limit)
	orderIDs := make([]int64, 0, limit)
	for rows.Next() {
		order := &shared.Order{}
		if err := rows.Scan(&order.ID, &order.Customer, &order.Total); err != nil {
			return nil, err
		}
		orders = append(orders, order)
		orderIDs = append(orderIDs, order.ID)
	}
	if len(orders) == 0 {
		return orders, nil
	}

	itemsByOrder, err := rawLoadItemsForOrders(ctx, db, orderIDs)
	if err != nil {
		return nil, err
	}
	notesByOrder, err := rawLoadNotesForOrders(ctx, db, orderIDs)
	if err != nil {
		return nil, err
	}

	for _, order := range orders {
		order.Items = itemsByOrder[order.ID]
		order.Notes = notesByOrder[order.ID]
	}
	return orders, nil
}

func rawLoadItemsForOrders(ctx context.Context, db rawDBTX, orderIDs []int64) (map[int64][]*shared.OrderItem, error) {
	rows, err := db.QueryContext(
		ctx,
		"SELECT id, order_id, product, quantity, price FROM order_items WHERE order_id = ANY($1)",
		orderIDs,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	itemsByOrder := make(map[int64][]*shared.OrderItem, len(orderIDs))
	for rows.Next() {
		item := &shared.OrderItem{}
		if err := rows.Scan(&item.ID, &item.OrderID, &item.Product, &item.Quantity, &item.Price); err != nil {
			return nil, err
		}
		itemsByOrder[item.OrderID] = append(itemsByOrder[item.OrderID], item)
	}
	return itemsByOrder, nil
}

func rawLoadNotesForOrders(ctx context.Context, db rawDBTX, orderIDs []int64) (map[int64][]*shared.OrderNote, error) {
	rows, err := db.QueryContext(
		ctx,
		"SELECT id, order_id, content FROM order_notes WHERE order_id = ANY($1)",
		orderIDs,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	notesByOrder := make(map[int64][]*shared.OrderNote, len(orderIDs))
	for rows.Next() {
		note := &shared.OrderNote{}
		if err := rows.Scan(&note.ID, &note.OrderID, &note.Content); err != nil {
			return nil, err
		}
		notesByOrder[note.OrderID] = append(notesByOrder[note.OrderID], note)
	}
	return notesByOrder, nil
}

func rawDeleteOrder(ctx context.Context, db rawDBTX, id int64) error {
	if _, err := db.ExecContext(ctx, "DELETE FROM order_notes WHERE order_id = $1", id); err != nil {
		return err
	}
	if _, err := db.ExecContext(ctx, "DELETE FROM order_items WHERE order_id = $1", id); err != nil {
		return err
	}
	if _, err := db.ExecContext(ctx, "DELETE FROM orders WHERE id = $1", id); err != nil {
		return err
	}
	return nil
}
