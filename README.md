# ormapper

`ormapper` persists plain Go aggregate structs with a small API:

- compile mappings once at startup
- pass `context.Context` plus `*sql.DB` or `*sql.Tx` to each operation
- use `Get`, `Save`, `Delete`, and their `Many` variants for aggregate persistence
- use `NewQuery` for typed reads

`Save` treats the input value as the authoritative aggregate snapshot. It
inserts rows with new manual keys, updates rows with existing keys, and deletes
children missing from the input value. For `ormapper:"auto"` primary keys, zero
means insert and non-zero means update; a non-zero generated key missing from the
database is a stale entity error.

```go
type Order struct {
	ID    int64 `ormapper:"auto"`
	Total int64
	Items []*OrderItem
}

type OrderItem struct {
	ID      int64 `ormapper:"auto"`
	OrderID int64 `ormapper:"parental"`
	Name    string
	Qty     int
}

mapper := ormapper.MustCompile(
	ormapper.Postgres,
	ormapper.Map(&Order{}, ormapper.WithTable("orders")),
	ormapper.Map(&OrderItem{}, ormapper.WithTable("order_items")),
)

var order *Order
if err := mapper.Get(ctx, tx, &order, ormapper.NewKey(id)); err != nil {
	return err
}

order.Total = 42
if err := mapper.Save(ctx, tx, order); err != nil {
	return err
}
```

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
