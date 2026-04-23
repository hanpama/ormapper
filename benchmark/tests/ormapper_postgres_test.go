package tests

import (
	"context"
	"testing"

	ormapper "github.com/hanpama/ormapper"
	"github.com/hanpama/ormapper/benchmark/shared"
)

func BenchmarkOrmapper_Postgres_Simple_Insert(b *testing.B) {
	db, cleanup := setupPostgresDB(b)
	defer cleanup()
	mapper := ormapper.MustCompile(ormapper.Postgres, ormapper.Map(&shared.User{}, ormapper.WithTable("users")))
	ctx := context.Background()

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		if err := mapper.Save(ctx, db, shared.NewUser()); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkOrmapper_Postgres_Simple_Select(b *testing.B) {
	db, cleanup := setupPostgresDB(b)
	defer cleanup()
	mapper := ormapper.MustCompile(ormapper.Postgres, ormapper.Map(&shared.User{}, ormapper.WithTable("users")))
	ctx := context.Background()
	ids := seedUsers(b, ctx, db, 100)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		var user *shared.User
		if err := mapper.Get(ctx, db, &user, ormapper.NewKey(ids[i%len(ids)])); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkOrmapper_Postgres_Simple_Update(b *testing.B) {
	db, cleanup := setupPostgresDB(b)
	defer cleanup()
	mapper := ormapper.MustCompile(ormapper.Postgres, ormapper.Map(&shared.User{}, ormapper.WithTable("users")))
	ctx := context.Background()
	ids := seedUsers(b, ctx, db, 100)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		var user *shared.User
		if err := mapper.Get(ctx, db, &user, ormapper.NewKey(ids[i%len(ids)])); err != nil {
			b.Fatal(err)
		}
		user.Age = 30
		if err := mapper.Save(ctx, db, user); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkOrmapper_Postgres_Simple_ReadSlice(b *testing.B) {
	db, cleanup := setupPostgresDB(b)
	defer cleanup()
	mapper := ormapper.MustCompile(ormapper.Postgres, ormapper.Map(&shared.User{}, ormapper.WithTable("users")))
	ctx := context.Background()
	_ = seedUsers(b, ctx, db, 100)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		users, err := ormapper.NewQuery[shared.User](mapper, db, "u").
			Where("u.id > ?", 0).
			FetchMany(ctx, 100)
		if err != nil {
			b.Fatal(err)
		}
		if len(users) != 100 {
			b.Fatalf("expected 100 users, got %d", len(users))
		}
	}
}

func BenchmarkOrmapper_Postgres_Simple_Delete(b *testing.B) {
	db, cleanup := setupPostgresDB(b)
	defer cleanup()
	mapper := ormapper.MustCompile(ormapper.Postgres, ormapper.Map(&shared.User{}, ormapper.WithTable("users")))
	ctx := context.Background()

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		b.StopTimer()
		user := shared.NewUser()
		if err := mapper.Save(ctx, db, user); err != nil {
			b.Fatal(err)
		}
		b.StartTimer()

		if err := mapper.Delete(ctx, db, user); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkOrmapper_Postgres_Aggregate_Insert(b *testing.B) {
	db, cleanup := setupPostgresDB(b)
	defer cleanup()
	mapper := aggregateMapper()
	ctx := context.Background()

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			b.Fatal(err)
		}
		if err := mapper.Save(ctx, tx, shared.NewOrder()); err != nil {
			_ = tx.Rollback()
			b.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkOrmapper_Postgres_Aggregate_Select(b *testing.B) {
	db, cleanup := setupPostgresDB(b)
	defer cleanup()
	mapper := aggregateMapper()
	ctx := context.Background()
	ids := seedOrders(b, ctx, db, 100)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		var order *shared.Order
		if err := mapper.Get(ctx, db, &order, ormapper.NewKey(ids[i%len(ids)])); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkOrmapper_Postgres_Aggregate_Update(b *testing.B) {
	db, cleanup := setupPostgresDB(b)
	defer cleanup()
	mapper := aggregateMapper()
	ctx := context.Background()
	ids := seedOrders(b, ctx, db, 100)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			b.Fatal(err)
		}
		var order *shared.Order
		if err := mapper.Get(ctx, tx, &order, ormapper.NewKey(ids[i%len(ids)])); err != nil {
			_ = tx.Rollback()
			b.Fatal(err)
		}
		shared.MutateOrder(order)
		if err := mapper.Save(ctx, tx, order); err != nil {
			_ = tx.Rollback()
			b.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkOrmapper_Postgres_Aggregate_ReadSlice(b *testing.B) {
	db, cleanup := setupPostgresDB(b)
	defer cleanup()
	mapper := aggregateMapper()
	ctx := context.Background()
	_ = seedOrders(b, ctx, db, 100)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		orders, err := ormapper.NewQuery[shared.Order](mapper, db, "o").
			Where("o.id > ?", 0).
			FetchMany(ctx, 100)
		if err != nil {
			b.Fatal(err)
		}
		if len(orders) != 100 {
			b.Fatalf("expected 100 orders, got %d", len(orders))
		}
	}
}

func BenchmarkOrmapper_Postgres_Aggregate_Delete(b *testing.B) {
	db, cleanup := setupPostgresDB(b)
	defer cleanup()
	mapper := aggregateMapper()
	ctx := context.Background()

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		b.StopTimer()
		order := shared.NewOrder()
		if err := mapper.Save(ctx, db, order); err != nil {
			b.Fatal(err)
		}
		b.StartTimer()

		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			b.Fatal(err)
		}
		if err := mapper.Delete(ctx, tx, &shared.Order{ID: order.ID}); err != nil {
			_ = tx.Rollback()
			b.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			b.Fatal(err)
		}
	}
}

func aggregateMapper() *ormapper.Mapper {
	return ormapper.MustCompile(
		ormapper.Postgres,
		ormapper.Map(&shared.Order{}, ormapper.WithTable("orders")),
		ormapper.Map(&shared.OrderItem{}, ormapper.WithTable("order_items")),
		ormapper.Map(&shared.OrderNote{}, ormapper.WithTable("order_notes")),
	)
}
