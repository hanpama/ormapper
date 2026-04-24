# ormapper Architecture

이 문서는 현재 구현을 밀어갈 기준이다. 목표는 `../orm1`의 기능을 얇게 복제하는 것이 아니라, 인메모리 세션/identity map/dirty tracking 없이도 엔티티 그래프를 명확한 의미로 영속하는 경량 라이브러리를 만드는 것이다.

## 1. Core Thesis

`Save(entity)`는 upsert가 아니다.

`Save(entity)`는 입력된 root entity graph를 authoritative snapshot으로 보고, 현재 데이터베이스 상태와 비교해 level 단위 delta를 적용한다.

```text
submitted graph
+ database snapshot at save time
=> toInsert
=> toUpdate
=> toDelete
```

이 delta는 row 단위로 처리하지 않는다. 모든 판단과 SQL 실행은 level-wide 또는 relation-wide batch로 수행한다.

## 2. Public Semantics

### 2.1 Insert vs Update

Primary key field는 세 종류로 나뉜다.

- auto primary key: DB/dialect가 insert 시 배정하는 identity
- manual primary key: 사용자가 직접 지정하는 identity
- composite primary key: 여러 primary key field의 조합

규칙:

```text
auto field는 insert input에서 항상 무시한다.

auto PK가 zero이면 insert intent다.
auto PK가 non-zero이면 update intent다.
auto PK non-zero인데 DB에 없으면 consistency error다.

manual PK는 DB에 있으면 update, 없으면 insert다.
manual PK의 zero 값은 결측으로 해석하지 않는다.
```

따라서 generated/auto PK non-zero 값은 "이 ID로 새 row를 만들라"가 아니다. "이미 존재하는 row를 수정하라"는 뜻이다.

### 2.2 Auto Fields

`ormapper:"auto"` field는 insert input에서 제외된다.

- auto PK zero: insert 후 `RETURNING`으로 값을 backfill한다.
- auto PK non-zero: update 대상 key로 사용한다.
- auto non-PK field: insert/update input에서 제외하고, `RETURNING`으로 backfill한다.

사용자가 auto field에 값을 넣어도 insert에는 영향을 주지 않는다.

### 2.3 Consistency Errors

Stateless라고 해서 consistency를 숨기지 않는다.

다음 상황은 명시적 오류다.

- auto PK non-zero entity가 사전 scan 결과 DB에 없음
- update로 계획된 row가 적용 시점에 사라져 update affected row 또는 returning row가 없음
- delete 계획 중 필요한 key shape가 mapping과 맞지 않음
- dialect가 해당 key generation/correlation semantic을 batch로 구현할 수 없음

권장 오류 이름:

```go
var ErrConsistency = errors.New("ormapper consistency error")
var ErrStaleEntity = errors.New("ormapper stale entity")
var ErrUnsupportedSemantic = errors.New("ormapper unsupported semantic")
```

구체 오류는 wrapping한다.

### 2.4 Concurrency

기본 transaction scope는 caller가 전달한 `*sql.DB` 또는 `*sql.Tx`가 결정한다.

Save는 다음 순서를 가진다.

```text
batch scan
batch insert/update/delete
```

scan 이후 다른 transaction이 row를 삭제하면 update/delete 단계에서 consistency error가 날 수 있다. 이것은 숨기지 않는다. caller가 더 강한 격리를 원하면 transaction isolation으로 해결한다.

## 3. Aggregate Replacement Semantics

### 3.1 Save

`Save(root)`는 root 아래 등록된 child relation 전체를 authoritative snapshot으로 본다.

- graph에 있는 entity는 insert 또는 update한다.
- graph에서 빠진 direct child는 delete 대상이다.
- delete는 항상 leaf-safe recursive delete로 처리한다.
- child relation의 leaf 여부로 public plan을 분기하지 않는다.

Save orchestration:

```text
saveLevel(mapping, entities)

1. classify entities by key intent
2. batch scan existing keys for entities that need DB existence judgment
3. build toInsert and toUpdate
4. batch insert toInsert
5. batch update toUpdate
6. backfill returned values
7. for each child relation in struct field order:
   a. inject parent key into submitted child entities
   b. saveLevel(childMapping, submitted children)
   c. relation-wide select missing child keys
   d. deleteByKeys(childMapping, missing child keys)
```

중요:

- `relation-wide select missing child keys`는 leaf/non-leaf 모두 동일하다.
- leaf child도 `select missing keys -> deleteByKeys` 경로를 탄다.
- leaf shortcut delete는 제거한다. 단순성을 팔아 작은 statement 절약을 사지 않는다.

### 3.2 Delete

`Delete(rootKey)`는 key operation이다.

```text
deleteByKeys(mapping, keys)

1. keys dedupe
2. for each child relation in struct field order:
   a. load child primary keys by parent keys
   b. deleteByKeys(childMapping, child keys)
3. delete current rows by primary keys
```

root existence check는 하지 않는다. root가 없으면 최종 delete가 no-op이지만, 이는 delete operation의 자연스러운 결과다.

## 4. Planner Model

### 4.1 Entity Save Classification

각 level에서 entity를 다음 category로 나눈다.

```text
generatedInsertCandidates:
  any generated primary key exists
  and all generated primary key values are zero

autoUpdateCandidates:
  any generated primary key exists
  and all generated primary key values are non-zero

manualKeyCandidates:
  no generated primary key exists
```

불허:

```text
generated primary key가 여러 개이고 일부만 zero인 경우
generated/manual mixed composite key 중 dialect가 batch key allocation을 지원하지 않는 경우
```

Manual key candidate는 batch existence scan으로 분리한다.

```text
manual existing => toUpdate
manual missing  => toInsert
```

Auto update candidate는 batch existence scan으로 검증한다.

```text
auto existing => toUpdate
auto missing  => ErrStaleEntity
```

Generated insert candidate는 existence scan을 하지 않는다. insert 때 auto fields는 제외하고 dialect가 key를 batch로 배정한다.

### 4.2 Relation Reconcile

Relation reconcile은 항상 key selection이다.

```text
SelectMissingChildren(parentKeys, keepPairs) => []childPrimaryKey
DeleteByKeys(childPrimaryKey)
```

이 규칙은 leaf와 non-leaf 모두 같다.

장점:

- delete path가 하나다.
- FK restrict/no action에서도 descendants를 먼저 지운다.
- SQL flow가 설명 가능하다.
- engine-specific optimization이 public planner를 갈라놓지 않는다.

## 5. Backend Boundary

공통 planner 책임:

- mapping metadata 해석
- graph traversal
- parent key injection
- entity classification
- relation snapshot 생성
- insert/update/delete 순서 결정
- consistency error 판단

Dialect 책임:

- high-level op를 batch SQL로 렌더링
- generated insert key allocation과 input correlation
- batch existing key scan
- batch insert
- batch update with returning or affected-row verification
- relation missing key select
- delete by key

목표 backend interface:

```go
type backend interface {
    LoadByKeys(ctx context.Context, op loadRowsOp) (rows, error)
    LoadByParentKeys(ctx context.Context, op loadRowsOp) (rows, error)
    SelectExistingKeys(ctx context.Context, op keyScanOp) ([]Key, error)

    InsertRows(ctx context.Context, op saveRowsOp, rows []plannedRow) ([]savedRow, error)
    UpdateRows(ctx context.Context, op saveRowsOp, rows []plannedRow) ([]savedRow, error)

    SelectMissingChildren(ctx context.Context, op selectMissingChildrenOp) ([]Key, error)
    DeleteRowsByKeys(ctx context.Context, op deleteRowsOp) error

    FetchQuery(ctx context.Context, stmt sqlQuery) (rows, error)
    CountQuery(ctx context.Context, stmt sqlQuery) (int64, error)
}
```

`SaveRows`라는 upsert-like primitive는 제거한다. insert와 update는 다른 semantic이므로 다른 primitive여야 한다.

## 6. SQL Shape

### 6.1 Batch Existing Key Scan

PostgreSQL:

```sql
WITH "$k" (pk...) AS (VALUES ...)
SELECT "$k".pk...
FROM "$k"
JOIN table ON table.pk = "$k".pk
```

SQLite:

```sql
WITH keys (pk...) AS (VALUES ...)
SELECT keys.pk...
FROM keys
JOIN table ON table.pk = keys.pk
```

### 6.2 Generated Insert

PostgreSQL:

```sql
WITH input ("$i", insert_cols...) AS (VALUES ...),
allocated AS (
  SELECT
    "$i",
    nextval(pg_get_serial_sequence(...)) AS generated_pk,
    insert_cols...
  FROM input
),
inserted AS (
  INSERT INTO table (generated_pk, insert_cols...)
  SELECT generated_pk, insert_cols...
  FROM allocated
  ORDER BY "$i"
  RETURNING returning_cols...
)
SELECT allocated."$i", inserted.returning_cols...
FROM inserted
JOIN allocated USING (generated_pk)
ORDER BY allocated."$i"
```

SQLite:

```sql
WITH input ("$i", insert_cols...) AS (VALUES ...),
base AS (
  SELECT max(sqlite_sequence.seq, max(table.generated_pk))
),
allocated AS (
  SELECT
    "$i",
    base + row_number() over (order by "$i") AS generated_pk,
    insert_cols...
  FROM input, base
)
INSERT INTO table (generated_pk, insert_cols...)
SELECT generated_pk, insert_cols...
FROM allocated
ORDER BY "$i"
RETURNING returning_cols...
```

SQLite generated insert currently requires a single integer generated primary key.

### 6.3 Manual Insert

Manual key insert is a plain batch insert with caller-provided PK included.

```sql
INSERT INTO table (pk..., insert_cols...)
SELECT ...
RETURNING returning_cols...
```

It must fail on unique conflict. The planner should not silently convert this into update.

### 6.4 Batch Update

PostgreSQL:

```sql
WITH "$r" (pk..., update_cols...) AS (VALUES ...)
UPDATE table
SET update_col = "$r".update_col, ...
FROM "$r"
WHERE table.pk = "$r".pk
RETURNING table.returning_cols...
```

SQLite:

```sql
WITH new_rows (pk..., update_cols...) AS (VALUES ...)
UPDATE table
SET update_col = new_rows.update_col, ...
FROM new_rows
WHERE table.pk = new_rows.pk
RETURNING returning_cols...
```

Update must return exactly the planned keys. Missing returned keys are consistency errors.

### 6.5 Relation Missing Key Select

Same for leaf and non-leaf relations.

```sql
WITH parent_rows (p...) AS (VALUES ...),
keep_rows (p..., c...) AS (VALUES ...)
SELECT child.pk...
FROM child
JOIN parent_rows ON child.parent_fk = parent_rows.p
LEFT JOIN keep_rows
  ON keep_rows.parent_fk = child.parent_fk
 AND keep_rows.child_pk = child.pk
WHERE keep_rows.child_pk IS NULL
```

If keep set is empty, select all direct child keys under the parent keys.

### 6.6 Delete By Keys

```sql
WITH keys (pk...) AS (VALUES ...)
DELETE FROM table
WHERE pk IN (SELECT pk FROM keys)
```

Delete does not need to error when row is already absent unless a higher-level operation specifically requires update consistency.

## 7. Expected Statement Sequences

### 7.1 New Generated Aggregate

```text
INSERT orders
INSERT order_notes
INSERT order_items
INSERT order_item_lots
```

No reconcile runs for newly generated parents, because they cannot have existing DB children.

### 7.2 Existing Mixed Aggregate

With existing root, existing note, one existing item kept, one new item, one removed item:

```text
EXISTS orders
UPDATE orders
EXISTS order_notes
UPDATE order_notes
MISSING order_notes
EXISTS order_items
INSERT order_items
UPDATE order_items
EXISTS order_item_lots
INSERT order_item_lots
UPDATE order_item_lots
MISSING order_item_lots
MISSING order_items
LOAD order_item_lots
DELETE order_item_lots
DELETE order_items
```

Notes:

- leaf relation also uses `MISSING -> DeleteByKeys`.
- delete recursion always goes through `deleteByKeys`.
- Empty delete key sets produce no SQL.

### 7.3 Singular Child Nil

```text
EXISTS orders
UPDATE orders
MISSING order_notes
DELETE order_notes
MISSING order_items
```

### 7.4 Explicit Auto Root ID

If `order.ID = 1000` and row 1000 does not exist:

```text
EXISTS orders
ERROR ErrStaleEntity
```

If row 1000 exists:

```text
EXISTS orders
UPDATE orders
INSERT order_notes
MISSING order_notes
DELETE order_notes
MISSING order_items
```

### 7.5 Delete Aggregate

```text
LOAD order_notes
DELETE order_notes
LOAD order_items
LOAD order_item_lots
DELETE order_item_lots
DELETE order_items
DELETE orders
```

Leaf shortcut is intentionally absent.

## 8. Implementation Plan

### Phase 1: Remove leaf shortcuts [done]

- `deleteMissingChildren` always selects missing child keys and calls `deleteByKeys`.
- `deleteByKeys` always loads child keys by parent keys before recursive delete.
- Remove `reconcileDeleteMissingRows`.
- Rename `ReconcileChildren` to `SelectMissingChildren`.
- Update SQL flow contracts and documentation.

### Phase 2: Split save primitive [done]

- Remove `SaveRows` as semantic primitive.
- Add `SelectExistingKeys`.
- Add `InsertRows`.
- Add `UpdateRows`.
- Reintroduce update SQL as batch update with returning/verification.
- Keep generated insert batch correlation in dialect.

### Phase 3: Planner rewrite [done]

- Replace current generated-zero/identified upsert planner with delta planner.
- Classify entities into generated inserts, auto updates, manual scan candidates.
- Build `toInsert` and `toUpdate`.
- Auto non-zero missing => `ErrStaleEntity`.
- Manual missing => insert.
- Manual existing => update.

### Phase 4: Contract tests [done]

- Change `ExplicitGeneratedRoot` to expect consistency error when row is missing.
- Add test for manual key missing insert.
- Add test for manual key existing update.
- Add test for leaf relation still using select-missing path.
- Add SQL flow fixture updates for new sequence.

### Phase 5: Benchmark and cleanup [done]

- Re-run root, SQLite, PostgreSQL contracts.
- Re-run Postgres aggregate benchmark.
- Remove remaining obsolete helper names.
- Confirm no row-by-row execution exists.

## 9. Non-Goals

- No identity map.
- No dirty tracking.
- No implicit upsert for auto identities.
- No row-by-row fallback.
- No leaf-specific public planner path.
- No silent insert when an auto-key update target is missing.

## 10. Source Layout

File structure must expose the same boundary as the runtime architecture.

```text
package.go
  Package documentation and built-in dialect entry points.
  This is the only central file that mentions both PostgreSQL and SQLite.

backend.go
  DBTX, Dialect, rows, emptyRows, and backend interface.
  This file must not depend on concrete engines.

postgres.go / sqlite.go
  Concrete dialect type, backend construction, SQL rendering, execution.

mapper.go
  Public aggregate API only. It validates input and delegates execution.

persistence.go
  Stateless graph save/load/delete orchestration.
  It receives mappingRegistry and backend, not Mapper.

mapping.go / mapping_key.go / registry.go
  Compiled entity metadata, key extraction, and lookup.

operations.go / backend_helpers.go
  Backend operation DTOs and shared scan/key helpers.
```

Important dependency rules:

- `backend.go` never imports or references `postgres.go` or `sqlite.go`.
- `persistence.go` never depends on `Mapper`.
- `mapping.go` never depends on persistence helpers.
- relation graph helpers stay inside `persistence.go`; no separate cross-referencing `save_graph.go`.
- key extraction belongs to mapping, hence `mapping_key.go`, not a generic `extract.go`.
