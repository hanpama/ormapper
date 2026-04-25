# ormapper — File Dependency Analysis

This report tracks the intended source-file layering after the persistence
semantic refactor. The important check is that backend contracts no longer point
at concrete database engines.

## Layered View

```mermaid
graph TD
  package_go["package.go\npackage doc, Postgres, SQLite"]
  backend_go["backend.go\nDBTX, Dialect, backend"]
  compile_go["compile.go\npublic compile API"]
  mapper_go["mapper.go\npublic Mapper API"]
  query_go["query.go\npublic Query API"]

  registry_go["registry.go\nmapping lookup"]
  mapping_go["mapping.go\nentity execution plan, key extraction"]

  persistence_go["persistence.go\ngraph save/load/delete orchestration"]
  operations_go["operations.go\nbackend operation DTOs"]
  backend_scan_go["backend_scan.go\nreturned-row scan/correlation"]
  sql_go["sql.go\nSQL AST helpers"]
  errors_go["errors.go\nsemantic errors"]
  key_go["key.go\nKey values"]

  postgres_go["postgres.go\nPostgreSQL dialect/backend"]
  sqlite_go["sqlite.go\nSQLite dialect/backend"]

  package_go --> postgres_go
  package_go --> sqlite_go
  package_go --> backend_go

  compile_go --> mapper_go
  compile_go --> backend_go
  compile_go --> mapping_go
  compile_go --> registry_go

  mapper_go --> backend_go
  mapper_go --> registry_go
  mapper_go --> persistence_go
  mapper_go --> mapping_go
  mapper_go --> key_go

  query_go --> backend_go
  query_go --> mapper_go
  query_go --> persistence_go
  query_go --> sql_go

  registry_go --> mapping_go
  mapping_go --> key_go
  mapping_go --> operations_go

  persistence_go --> backend_go
  persistence_go --> registry_go
  persistence_go --> mapping_go
  persistence_go --> operations_go
  persistence_go --> backend_scan_go
  persistence_go --> errors_go
  persistence_go --> key_go

  backend_go --> operations_go
  backend_go --> sql_go
  backend_go --> key_go

  operations_go --> errors_go
  operations_go --> key_go

  backend_scan_go --> backend_go
  backend_scan_go --> operations_go
  backend_scan_go --> key_go

  postgres_go --> backend_go
  postgres_go --> backend_scan_go
  postgres_go --> operations_go
  postgres_go --> sql_go
  postgres_go --> errors_go
  postgres_go --> key_go

  sqlite_go --> backend_go
  sqlite_go --> backend_scan_go
  sqlite_go --> operations_go
  sqlite_go --> sql_go
  sqlite_go --> errors_go
  sqlite_go --> key_go
```

## Boundary Checks

| Check | Status | Notes |
|---|---|---|
| `backend.go` does not depend on `postgres.go` or `sqlite.go` | OK | `backend.go` owns only contracts. |
| Built-in dialect exposure is centralized | OK | `package.go` exposes `Postgres` and `SQLite`. |
| Concrete backend construction lives with each engine | OK | `postgresDialect.newBackend` is in `postgres.go`; `sqliteDialect.newBackend` is in `sqlite.go`. |
| `Mapper` does not perform graph persistence itself | OK | `mapper.go` validates public API inputs and delegates to `persistence.go`. |
| `persistence.go` no longer depends on `Mapper` | OK | It receives `mappingRegistry` and `backend`. |
| `save_graph.go` cross-reference is gone | OK | Relation snapshot/reconcile code is merged into `persistence.go`. |
| `extract.go` standalone file is gone | OK | Key extraction is part of `mapping.go`, adjacent to mapping metadata. |
| `mapping.go` no longer depends on persistence helpers | OK | `uniqueFieldNames` moved into mapping code. |

## Current File Roles

| File | Role |
|---|---|
| `package.go` | Package documentation and built-in dialect entry points. |
| `backend.go` | Public DB handle abstraction plus internal backend contract. |
| `compile.go` | Public mapping registration, struct tag analysis, validation, and private mapping construction subroutines. |
| `registry.go` | Mapping lookup abstraction shared by Mapper and persistence. |
| `mapper.go` | Public aggregate API: `Get`, `Save`, `Delete`. |
| `query.go` | Public typed query API. |
| `mapping.go` | Compiled entity execution plans, save layout, child metadata, key extraction, and reflect accessors. |
| `persistence.go` | Stateless graph persistence orchestration. |
| `operations.go` | High-level backend operation DTOs and operation helpers. |
| `backend_scan.go` | Backend returned-row scan and correlation helpers. |
| `postgres.go` | PostgreSQL SQL rendering and backend execution. |
| `sqlite.go` | SQLite SQL rendering and backend execution. |
| `sql.go` | Query SQL node model and parser. |
| `key.go` | Comparable key representation. |
| `errors.go` | Public semantic error sentinels. |

## Removed Files

- `dialect.go`: split into `package.go` and `backend.go`; concrete dialect
  methods moved to engine files.
- `doc.go`: package documentation moved to `package.go`.
- `build.go`: compile subroutines moved under `compile.go`.
- `analyze.go`: struct tag analysis moved under `compile.go`.
- `persistence_op.go`: renamed and merged into `persistence.go`.
- `save_graph.go`: merged into `persistence.go`.
- `extract.go`: merged into `mapping.go`.

## Remaining Tensions

- `mapping.go` currently builds `saveLayout`, while `operations.go` owns the
  backend-facing `saveRowsLayout`. This split keeps entity field plans out of
  backend operation DTOs.
- `postgres.go` and `sqlite.go` are intentionally large because each keeps SQL
  rendering and execution together. Splitting renderers later should be based on
  evidence from readability or test friction, not file length alone.
