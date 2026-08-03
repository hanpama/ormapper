# agg

`agg` persists plain Go aggregate structs with a small API:

- compile mappings once at startup
- pass `context.Context` plus `*sql.DB` or `*sql.Tx` to each operation
- use `Get`, `Save`, `Delete`, and their `Many` variants for aggregate persistence
- use `NewQuery` for typed reads

`Save` treats the input value as the authoritative aggregate snapshot. It
inserts rows with new manual keys, updates rows with existing keys, and deletes
children missing from the input value. For `agg:"auto"` primary keys, zero
means insert and non-zero means update; a non-zero generated key missing from the
database is a stale entity error.

```go
type Order struct {
	ID    int64 `agg:"auto"`
	Total int64
	Items []*OrderItem
}

type OrderItem struct {
	ID      int64 `agg:"auto"`
	OrderID int64 `agg:"parental"`
	Name    string
	Qty     int
}

mapper := agg.MustCompile(
	agg.Postgres,
	agg.Map(&Order{}, agg.WithTable("orders")),
	agg.Map(&OrderItem{}, agg.WithTable("order_items")),
)

var order *Order
if err := mapper.Get(ctx, tx, &order, agg.NewKey(id)); err != nil {
	return err
}

order.Total = 42
if err := mapper.Save(ctx, tx, order); err != nil {
	return err
}
```

## Copy vendoring

The database-neutral library is contained in `lib.go`. Copy it together with
the backend for the database you use, keeping both files in the same directory
and Go package:

| Database | Files to copy |
|---|---|
| PostgreSQL | `lib.go`, `postgres.go` |
| SQLite | `lib.go`, `sqlite.go` |

The root package and both backends depend only on the Go standard library. The
application continues to choose and import its own `database/sql` driver.

## Tests

The root module has no database driver dependency.

```sh
go test ./...
cd tests/contracts && go test ./...
cd tests/sqlite && go test ./...
docker compose -f tests/docker-compose.yml up -d --wait
cd tests/postgres && go test ./...
```

PostgreSQL listens on host port `17432` to avoid conflicts with local `5432`.
