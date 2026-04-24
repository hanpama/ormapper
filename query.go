package ormapper

import (
	"context"
	"fmt"
	"reflect"
)

// Query provides a typed read API for one mapped entity type.
// Create it with NewQuery.
type Query[T any] struct {
	mapper       *Mapper
	db           DBTX
	mapping      *entityMapping
	alias        string
	joins        map[string]*join
	whereConds   []sqlNode
	havingConds  []sqlNode
	orderByOpts  []OrderExpr
	groupByExprs []sqlNode
	offset       *int
}

// NewQuery creates a query builder for entity type T.
//
// T must be the struct type itself, not *T. For example, use NewQuery[Order],
// not NewQuery[*Order].
//
// alias is the SQL alias used in Where, Join, OrderBy, and related clauses.
func NewQuery[T any](mapper *Mapper, db DBTX, alias string) *Query[T] {
	var zero T
	entityType := reflect.TypeOf(zero)
	if entityType == nil {
		panic("Query[T]: T must be a struct type")
	}
	if entityType.Kind() == reflect.Ptr {
		panic("Query[T]: T must be a struct type, not *Struct")
	}

	mapping, err := mapper.getMapping(entityType)
	if err != nil {
		panic(err)
	}

	if alias == "" {
		alias = mapping.Table
	}

	return &Query[T]{
		mapper:       mapper,
		db:           db,
		mapping:      mapping,
		alias:        alias,
		joins:        make(map[string]*join),
		whereConds:   []sqlNode{},
		havingConds:  []sqlNode{},
		orderByOpts:  []OrderExpr{},
		groupByExprs: []sqlNode{},
	}
}

// Join adds an INNER JOIN clause.
func (q *Query[T]) Join(table string, alias string, on string, params ...any) *Query[T] {
	q.joins[alias] = &join{
		Type:  "JOIN",
		Table: sqlText{Text: table},
		Alias: sqlText{Text: alias},
		On:    parseSQL(on, params...),
	}
	return q
}

// LeftJoin adds a LEFT JOIN clause.
func (q *Query[T]) LeftJoin(table string, alias string, on string, params ...any) *Query[T] {
	q.joins[alias] = &join{
		Type:  "LEFT JOIN",
		Table: sqlText{Text: table},
		Alias: sqlText{Text: alias},
		On:    parseSQL(on, params...),
	}
	return q
}

// Where appends a WHERE predicate combined with AND.
func (q *Query[T]) Where(condition string, params ...any) *Query[T] {
	q.whereConds = append(q.whereConds, parseSQL(fmt.Sprintf("(%s)", condition), params...))
	return q
}

// Having appends a HAVING predicate combined with AND.
func (q *Query[T]) Having(condition string, params ...any) *Query[T] {
	q.havingConds = append(q.havingConds, parseSQL(fmt.Sprintf("(%s)", condition), params...))
	return q
}

// GroupByPrimaryKey groups by the mapped entity primary key columns.
func (q *Query[T]) GroupByPrimaryKey() *Query[T] {
	for _, fieldName := range q.mapping.PrimaryKey {
		field := q.mapping.FieldMap[fieldName]
		q.groupByExprs = append(q.groupByExprs, sqlQN{Part1: q.alias, Part2: field.Column})
	}
	return q
}

// Offset sets the SQL OFFSET value.
func (q *Query[T]) Offset(n int) *Query[T] {
	q.offset = &n
	return q
}

// OrderBy replaces the current ORDER BY list.
// Build sort expressions with q.Asc(...) and q.Desc(...).
func (q *Query[T]) OrderBy(orderBys ...OrderExpr) *Query[T] {
	q.orderByOpts = orderBys
	return q
}

// Asc builds an ascending ORDER BY expression with NULLS LAST semantics.
func (q *Query[T]) Asc(expr string, params ...any) OrderExpr {
	return OrderExpr{
		expr:      parseSQL(expr, params...),
		ascending: true,
		nullsLast: true,
	}
}

// Desc builds a descending ORDER BY expression with NULLS FIRST semantics.
func (q *Query[T]) Desc(expr string, params ...any) OrderExpr {
	return OrderExpr{
		expr:      parseSQL(expr, params...),
		ascending: false,
		nullsLast: false,
	}
}

func (q *Query[T]) buildSelectColumns() []sqlNode {
	selectCols := make([]sqlNode, len(q.mapping.AllFields))
	for i, fieldName := range q.mapping.AllFields {
		field := q.mapping.FieldMap[fieldName]
		selectCols[i] = sqlQN{Part1: q.alias, Part2: field.Column}
	}
	return selectCols
}

func (q *Query[T]) buildPrimaryKeyColumns() []sqlNode {
	selectCols := make([]sqlNode, 0, len(q.mapping.PrimaryKey))
	for _, fieldName := range q.mapping.PrimaryKey {
		field := q.mapping.FieldMap[fieldName]
		selectCols = append(selectCols, sqlQN{Part1: q.alias, Part2: field.Column})
	}
	return selectCols
}

func (q *Query[T]) buildJoins() []join {
	joins := make([]join, 0, len(q.joins))
	for _, join := range q.joins {
		joins = append(joins, *join)
	}
	return joins
}

func (q *Query[T]) buildWhereClause() *sqlNode {
	if len(q.whereConds) == 0 {
		return nil
	}
	var w sqlNode = sqlAll{Els: q.whereConds}
	return &w
}

func (q *Query[T]) buildHavingClause() *sqlNode {
	if len(q.havingConds) == 0 {
		return nil
	}
	var h sqlNode = sqlAll{Els: q.havingConds}
	return &h
}

func (q *Query[T]) buildGroupBy() []sqlNode {
	if len(q.groupByExprs) == 0 {
		return nil
	}
	return q.groupByExprs
}

func (q *Query[T]) fetch(ctx context.Context, limit *int) ([]*T, error) {
	var limitSQL, offsetSQL *sqlNode
	if limit != nil {
		var l sqlNode = sqlParam{Value: *limit}
		limitSQL = &l
	}
	if q.offset != nil {
		var o sqlNode = sqlParam{Value: *q.offset}
		offsetSQL = &o
	}

	stmt := sqlQuery{
		Select:    q.buildSelectColumns(),
		FromTable: sqlQN{Part1: q.mapping.Schema, Part2: q.mapping.Table},
		FromAlias: sqlText{Text: q.alias},
		Joins:     q.buildJoins(),
		Where:     q.buildWhereClause(),
		GroupBy:   q.buildGroupBy(),
		Having:    q.buildHavingClause(),
		OrderBys:  q.orderByOpts,
		Limit:     limitSQL,
		Offset:    offsetSQL,
	}

	backend := q.mapper.dialect.newBackend(q.db)
	rows, err := backend.FetchQuery(ctx, stmt)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	expectedCapacity := 0
	if limit != nil {
		expectedCapacity = *limit
	}

	u := newPersistence(q.mapper.mappings, backend)
	entities := make([]any, 0, expectedCapacity)
	for rows.Next() {
		entityPtr := reflect.New(q.mapping.EntityType).Interface()
		if err := u.scanEntity(q.mapping, entityPtr, rows, q.mapping.AllFields); err != nil {
			return nil, err
		}
		entities = append(entities, entityPtr)
	}

	if len(entities) > 0 && len(q.mapping.ChildMap) > 0 {
		if err := u.loadChildren(ctx, q.mapping, entities); err != nil {
			return nil, err
		}
	}

	result := make([]*T, 0, len(entities))
	for _, entity := range entities {
		result = append(result, entity.(*T))
	}

	return result, nil
}

// FetchAll executes the query and returns all matching entities.
func (q *Query[T]) FetchAll(ctx context.Context) ([]*T, error) {
	return q.fetch(ctx, nil)
}

// FetchMany executes the query and returns up to limit entities.
func (q *Query[T]) FetchMany(ctx context.Context, limit int) ([]*T, error) {
	return q.fetch(ctx, &limit)
}

// FetchOne executes the query and returns the first matching entity.
// It returns nil, nil when no row matches.
func (q *Query[T]) FetchOne(ctx context.Context) (*T, error) {
	results, err := q.FetchMany(ctx, 1)
	if err != nil {
		return nil, err
	}
	if len(results) == 0 {
		return nil, nil
	}
	return results[0], nil
}

// Count returns the number of rows that match the current query.
func (q *Query[T]) Count(ctx context.Context) (int64, error) {
	stmt := sqlQuery{
		Select:    q.buildPrimaryKeyColumns(),
		FromTable: sqlQN{Part1: q.mapping.Schema, Part2: q.mapping.Table},
		FromAlias: sqlText{Text: q.alias},
		Joins:     q.buildJoins(),
		Where:     q.buildWhereClause(),
		GroupBy:   q.buildGroupBy(),
		Having:    q.buildHavingClause(),
	}

	return q.mapper.dialect.newBackend(q.db).CountQuery(ctx, stmt)
}
