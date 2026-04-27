# agg benchmarks

This module keeps benchmark-only dependencies out of the root `agg` module.

Run PostgreSQL first:

```sh
docker compose -f ../tests/docker-compose.yml up -d --wait
```

Run all current benchmarks:

```sh
go test -run '^$' -bench . -benchmem ./tests
```

Run a focused group:

```sh
go test -run '^$' -bench 'BenchmarkOrmapper_Postgres_Aggregate' -benchmem ./tests
```

The default DSN is:

```text
postgres://ormapper:ormapper@localhost:17432/ormapper?sslmode=disable
```

Override it with `ORMAPPER_BENCH_POSTGRES_DSN`.

## Structure

- `shared`: benchmark entities, fixture data, and schema helpers.
- `tests/agg_postgres_test.go`: agg subject benchmarks.
- `tests/raw_postgres_test.go`: raw SQL baseline benchmarks.

Additional comparison libraries should be added as separate files under
`tests`, following the benchmark name shape:

```text
Benchmark<Subject>_Postgres_<Category>_<Operation>
```

Examples:

```text
BenchmarkGORM_Postgres_Simple_Insert
BenchmarkSqlx_Postgres_Aggregate_Update
```

This keeps result parsing and side-by-side comparison straightforward.
