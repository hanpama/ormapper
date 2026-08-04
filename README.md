# agg

`agg` persists plain Go aggregate structs through `database/sql`. It has no
runtime dependencies and no session, identity map, or dirty tracking.

The supported backends are PostgreSQL and SQLite.

## Install

```sh
go get github.com/hanpama/agg
```

Your application chooses its own `database/sql` driver. `agg` does not import
one.

## Quick start

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

`Mapper` is immutable after compilation and may be shared by goroutines. A
`Query` is mutable and should remain local to one operation.

## Save semantics and transactions

`Save` treats its argument as the authoritative aggregate snapshot:

- a zero `agg:"auto"` primary key inserts a row and is backfilled
- a non-zero auto primary key updates an existing row
- a missing non-zero auto key returns `ErrStaleEntity`
- a manual key inserts when absent and updates when present
- children omitted from the submitted aggregate are deleted

Saving an aggregate executes multiple SQL statements. Passing `*sql.DB` does
not make the operation atomic. Use `*sql.Tx` when the whole aggregate must
commit or roll back together:

```go
tx, err := db.BeginTx(ctx, nil)
if err != nil {
	return err
}
defer tx.Rollback()

if err := mapper.Save(ctx, tx, order); err != nil {
	return err
}
return tx.Commit()
```

The caller also owns transaction isolation. A concurrent delete between the
existence scan and update is reported as `ErrStaleEntity`.

## Mapping

Mappings are compiled once at startup with `Map` and `Compile` or
`MustCompile`.

| Tag | Meaning |
|---|---|
| `agg:"auto"` | Database-generated field; skipped on insert and update, then backfilled |
| `agg:"primary"` | Primary-key field; a field named `ID` is primary by default |
| `agg:"parental"` | Child foreign key corresponding to the parent primary key |
| `agg:"column:name"` | Override the snake_case column name |
| `agg:"skip_insert"` | Exclude the field from inserts |
| `agg:"skip_update"` | Exclude the field from updates |
| `agg:"-"` | Ignore the field |

Registered `*Child` and `[]*Child` fields are aggregate relations. Relation
graphs must be acyclic. A singular child relation requires the database to
enforce at most one child row per parent.

`WithTable`, `WithSchema`, and `WithConverter` provide mapping options. Key
fields cannot use converters and must be comparable Go values. Composite keys
support up to nine fields.

## Queries

```go
q := agg.NewQuery[Order](mapper, tx, "o")
orders, err := q.
	Where("o.total > ?", 100).
	OrderBy(q.Desc("o.total")).
	FetchMany(ctx, 50)
```

`Where`, `Having`, join conditions, and order expressions accept SQL fragments
with `?` parameters. Write `??` for a literal question mark, including
PostgreSQL JSON operators:

```go
q.Where(`o.payload ?? 'priority'`)
```

SQL fragments and identifiers are developer-authored input. Never concatenate
untrusted input into them; pass values as parameters.

## Errors and limits

Use `errors.Is` with:

- `ErrStaleEntity` for an update target that no longer exists
- `ErrConsistency` for duplicate submitted keys or row-count mismatches
- `ErrUnsupportedSemantic` for a mapping the backend cannot implement

`GetMany`, `SaveMany`, and `DeleteMany` issue database-wide batches. The caller
must keep batches below the selected database's statement and parameter limits.
There is no automatic chunking.

`NewKey`, `MustCompile`, and `NewQuery` panic on invalid programmer input.
Query construction also panics when the number of `?` placeholders and
parameters differs. Use `Compile` when mapping errors must be returned.

`Dialect` is intentionally closed. Module users select `Postgres` or `SQLite`;
copy-vendored users may maintain another backend in the same package.

## Copy vendoring

Copy the database-neutral core with exactly the backend you use, keeping the
files in the same directory and Go package:

| Database | Files |
|---|---|
| PostgreSQL | `lib.go`, `postgres.go` |
| SQLite | `lib.go`, `sqlite.go` |

The full MIT notice and upstream location are embedded in `lib.go`.

## Compatibility

CI tests the minimum Go version declared in `go.mod` and the current Go release.
The database contract suite runs against PostgreSQL 16 with pgx and SQLite with
modernc.org/sqlite. Other `database/sql` drivers are not part of the tested
matrix.

## Development

```sh
go test ./...
(cd tests/contracts && go test ./...)
(cd tests/sqlite && go test ./...)

docker compose -f tests/docker-compose.yml up -d --wait
(cd tests/postgres && go test ./...)
(cd benchmark && go test ./...)
```

PostgreSQL listens on host port `17432`.
