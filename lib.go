// MIT License
//
// Copyright (c) 2026 Kyungil Choi
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in all
// copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
// SOFTWARE.

// Upstream: https://github.com/hanpama/agg

// Package agg persists plain Go aggregates with a small public API.
//
// The intended workflow is:
//
//  1. Compile a Mapper from your entity structs.
//  2. Pass context plus *sql.DB or *sql.Tx to each operation.
//  3. Use Get, Save, Delete, and their Many variants for aggregate persistence.
//  4. Use NewQuery for typed reads.
//
// agg does not expose a long-lived session. The caller owns transaction
// scope by deciding whether each call receives a *sql.DB or a *sql.Tx.
//
// # Copy vendoring
//
// The package is distributed as a database-neutral lib.go plus optional
// database backends. Copy lib.go together with postgres.go or sqlite.go into
// the same directory and package.
//
// # Struct tags
//
// Fields are configured with the `agg` struct tag:
//
//   - `agg:"auto"` — database-generated field (skip insert/update, backfill from RETURNING).
//   - `agg:"primary"` — explicit primary key (default: a field named ID).
//   - `agg:"parental"` — child foreign key pointing to the parent.
//   - `agg:"column:name"` — override the column name (default: snake_case of field name).
//   - `agg:"skip_insert"` — exclude from INSERT.
//   - `agg:"skip_update"` — exclude from UPDATE.
//   - `agg:"-"` — ignore the field entirely.
//
// Tags can be combined: `agg:"primary,parental"`.
//
// # Mapping options
//
//   - WithTable overrides the table name.
//   - WithSchema sets the schema prefix.
//   - WithConverter registers a bidirectional type converter for a field.
//
// Registered *Child and []*Child fields are treated as aggregate children
// automatically when the child type is also compiled.
//
// # Save semantics
//
// Save treats the input as the authoritative aggregate snapshot. It inserts rows
// with new manual keys, updates rows with existing keys, and deletes children
// missing from the input value. For auto primary keys, zero means insert and
// non-zero means update.
//
// # Example
//
//	type Order struct {
//	    ID    int64 `agg:"auto"`
//	    Total int64
//	    Items []*OrderItem
//	}
//
//	type OrderItem struct {
//	    ID      int64 `agg:"auto"`
//	    OrderID int64 `agg:"parental"`
//	    Name    string
//	    Qty     int
//	}
//
//	mapper := MustCompile(
//	    Postgres,
//	    Map(&Order{}, WithTable("orders")),
//	    Map(&OrderItem{}, WithTable("order_items")),
//	)
//
//	var order *Order
//	if err := mapper.Get(ctx, tx, &order, NewKey(id)); err != nil {
//	    return err
//	}
//
//	order.Total = 42
//	if err := mapper.Save(ctx, tx, order); err != nil {
//	    return err
//	}
//
//	query := NewQuery[Order](mapper, tx, "o")
//	found, err := query.Where("o.total > ?", 0).FetchAll(ctx)
package agg

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
)

// --- Public contracts and errors ---

var (
	// ErrConsistency indicates a data integrity violation such as duplicate
	// submitted keys or a row count mismatch from the database.
	ErrConsistency = errors.New("agg consistency error")

	// ErrStaleEntity indicates that a non-zero auto-generated key does not
	// exist in the database. The entity was likely deleted or never inserted.
	ErrStaleEntity = errors.New("agg stale entity")

	// ErrUnsupportedSemantic indicates a mapping configuration that the
	// library cannot handle, such as a generated composite primary key or
	// partially zero generated key fields.
	ErrUnsupportedSemantic = errors.New("agg unsupported semantic")
)

// DBTX is the minimal database handle required by Mapper.
// Both *sql.DB and *sql.Tx satisfy this interface, so callers usually pass one
// of those values directly.
type DBTX interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// Dialect selects the SQL dialect used by a Mapper.
// Use one of the built-in values such as Postgres or SQLite.
type Dialect interface {
	newBackend(db DBTX) backend
}

// --- Keys ---

// Key represents an entity's primary or foreign key value(s).
// It is a comparable value that can be used as a map key.
//
// Examples:
//
//	NewKey(1)               // single-column key
//	NewKey(orderID, itemID) // composite key
type Key struct {
	n  int
	v0 any
	vn any
}

// Length returns the number of column values in the key.
func (k Key) Length() int {
	return k.n
}

// At returns the key value at the given zero-based column index.
// Panics if index is out of range.
func (k Key) At(index int) any {
	if index < 0 || index >= k.n {
		panic("agg: key index out of range")
	}

	if index == 0 {
		return k.v0
	}
	return k.tailValue(index)
}

func (k Key) tailValue(index int) any {
	switch tail := k.vn.(type) {
	case keyTail1:
		if index == 1 {
			return tail.v1
		}
	case keyTail2:
		switch index {
		case 1:
			return tail.v1
		case 2:
			return tail.v2
		}
	case keyTail3:
		switch index {
		case 1:
			return tail.v1
		case 2:
			return tail.v2
		case 3:
			return tail.v3
		}
	case keyTail4:
		switch index {
		case 1:
			return tail.v1
		case 2:
			return tail.v2
		case 3:
			return tail.v3
		case 4:
			return tail.v4
		}
	case keyTail5:
		switch index {
		case 1:
			return tail.v1
		case 2:
			return tail.v2
		case 3:
			return tail.v3
		case 4:
			return tail.v4
		case 5:
			return tail.v5
		}
	case keyTail6:
		switch index {
		case 1:
			return tail.v1
		case 2:
			return tail.v2
		case 3:
			return tail.v3
		case 4:
			return tail.v4
		case 5:
			return tail.v5
		case 6:
			return tail.v6
		}
	case keyTail7:
		switch index {
		case 1:
			return tail.v1
		case 2:
			return tail.v2
		case 3:
			return tail.v3
		case 4:
			return tail.v4
		case 5:
			return tail.v5
		case 6:
			return tail.v6
		case 7:
			return tail.v7
		}
	case keyTail8:
		switch index {
		case 1:
			return tail.v1
		case 2:
			return tail.v2
		case 3:
			return tail.v3
		case 4:
			return tail.v4
		case 5:
			return tail.v5
		case 6:
			return tail.v6
		case 7:
			return tail.v7
		case 8:
			return tail.v8
		}
	}
	panic("agg: key index out of range")
}

type keyTail1 struct{ v1 any }
type keyTail2 struct{ v1, v2 any }
type keyTail3 struct{ v1, v2, v3 any }
type keyTail4 struct{ v1, v2, v3, v4 any }
type keyTail5 struct{ v1, v2, v3, v4, v5 any }
type keyTail6 struct{ v1, v2, v3, v4, v5, v6 any }
type keyTail7 struct{ v1, v2, v3, v4, v5, v6, v7 any }
type keyTail8 struct{ v1, v2, v3, v4, v5, v6, v7, v8 any }

// NewKey creates a new Key from the given values.
// Supports up to 9 column values.
func NewKey(vals ...any) Key {
	return newKeyFromValues(vals)
}

func newKeyFromValues(vals []any) Key {
	switch len(vals) {
	case 0:
		return new0()
	case 1:
		return new1(vals[0])
	case 2:
		return new2(vals[0], vals[1])
	case 3:
		return new3(vals[0], vals[1], vals[2])
	case 4:
		return new4(vals[0], vals[1], vals[2], vals[3])
	case 5:
		return new5(vals[0], vals[1], vals[2], vals[3], vals[4])
	case 6:
		return new6(vals[0], vals[1], vals[2], vals[3], vals[4], vals[5])
	case 7:
		return new7(vals[0], vals[1], vals[2], vals[3], vals[4], vals[5], vals[6])
	case 8:
		return new8(vals[0], vals[1], vals[2], vals[3], vals[4], vals[5], vals[6], vals[7])
	case 9:
		return new9(vals[0], vals[1], vals[2], vals[3], vals[4], vals[5], vals[6], vals[7], vals[8])
	default:
		panic("agg: Key supports up to 9 column values")
	}
}

func new0() Key {
	return Key{}
}

func new1(v0 any) Key {
	return Key{n: 1, v0: v0}
}

func new2(v0, v1 any) Key {
	return Key{n: 2, v0: v0, vn: keyTail1{v1: v1}}
}

func new3(v0, v1, v2 any) Key {
	return Key{n: 3, v0: v0, vn: keyTail2{v1: v1, v2: v2}}
}

func new4(v0, v1, v2, v3 any) Key {
	return Key{n: 4, v0: v0, vn: keyTail3{v1: v1, v2: v2, v3: v3}}
}

func new5(v0, v1, v2, v3, v4 any) Key {
	return Key{n: 5, v0: v0, vn: keyTail4{v1: v1, v2: v2, v3: v3, v4: v4}}
}

func new6(v0, v1, v2, v3, v4, v5 any) Key {
	return Key{n: 6, v0: v0, vn: keyTail5{v1: v1, v2: v2, v3: v3, v4: v4, v5: v5}}
}

func new7(v0, v1, v2, v3, v4, v5, v6 any) Key {
	return Key{n: 7, v0: v0, vn: keyTail6{v1: v1, v2: v2, v3: v3, v4: v4, v5: v5, v6: v6}}
}

func new8(v0, v1, v2, v3, v4, v5, v6, v7 any) Key {
	return Key{n: 8, v0: v0, vn: keyTail7{v1: v1, v2: v2, v3: v3, v4: v4, v5: v5, v6: v6, v7: v7}}
}

func new9(v0, v1, v2, v3, v4, v5, v6, v7, v8 any) Key {
	return Key{n: 9, v0: v0, vn: keyTail8{v1: v1, v2: v2, v3: v3, v4: v4, v5: v5, v6: v6, v7: v7, v8: v8}}
}

type keySet map[Key]struct{}

func newKeySet(keys []Key) keySet {
	s := make(keySet, len(keys))
	for _, key := range keys {
		s[key] = struct{}{}
	}
	return s
}

func uniqueKeys(keys []Key) []Key {
	if len(keys) < 2 {
		return keys
	}
	result := make([]Key, 0, len(keys))
	seen := make(keySet, len(keys))
	for _, key := range keys {
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, key)
	}
	return result
}

// --- Mapping compilation ---

// Mapping is an opaque entity registration created by Map and consumed by Compile.
type Mapping struct {
	ptr     any
	options []MapOption
}

// Map registers an entity pointer with compile-time mapping options.
func Map(entityPtr any, opts ...MapOption) Mapping {
	return Mapping{
		ptr:     entityPtr,
		options: opts,
	}
}

// MapOption customizes how an entity maps to a table.
// Build options with WithTable, WithSchema, and WithConverter.
type MapOption struct {
	apply func(*entityMetadata)
}

// WithSchema overrides the default schema for an entity.
// Pass it only to Map.
func WithSchema(schema string) MapOption {
	return MapOption{
		apply: func(meta *entityMetadata) {
			meta.Schema = schema
		},
	}
}

// WithTable overrides the default table name for an entity.
// Pass it only to Map.
func WithTable(table string) MapOption {
	return MapOption{
		apply: func(meta *entityMetadata) {
			meta.Table = table
		},
	}
}

// Compile analyzes entity structs and produces an immutable Mapper.
//
// Each mapping must be created with Map, for example Map(&Order{},
// WithTable("orders")).
//
// Compile is typically called once during application startup.
func Compile(dialect Dialect, mappings ...Mapping) (*Mapper, error) {
	if dialect == nil {
		return nil, fmt.Errorf("Compile: dialect is required")
	}

	registered := make(map[reflect.Type]bool, len(mappings))
	meta := make(map[reflect.Type]entityMetadata, len(mappings))

	for _, mapping := range mappings {
		entityPtr := mapping.ptr
		entityPtrType := reflect.TypeOf(entityPtr)
		if entityPtrType == nil {
			return nil, fmt.Errorf("Compile: entity must be a pointer to struct, got <nil>")
		}
		if entityPtrType.Kind() != reflect.Ptr {
			return nil, fmt.Errorf("Compile: entity must be a pointer to struct, got %s", entityPtrType)
		}
		if entityPtrType.Elem().Kind() != reflect.Struct {
			return nil, fmt.Errorf("Compile: entity must be a pointer to struct, got %s", entityPtrType)
		}

		entityType := entityPtrType.Elem()
		if registered[entityType] {
			return nil, fmt.Errorf("Compile: duplicate mapping for %s", entityType)
		}
		entityMeta := entityMetadata{
			Table:  toSnakeCase(entityType.Name()),
			Fields: analyzeStruct(entityType),
		}
		for _, opt := range mapping.options {
			opt.apply(&entityMeta)
		}

		registered[entityType] = true
		meta[entityType] = entityMeta
	}

	registry := buildEntityMappings(meta, registered)
	if err := validateMappings(registry); err != nil {
		return nil, err
	}

	return &Mapper{
		dialect:  dialect,
		mappings: registry,
	}, nil
}

// MustCompile is Compile that panics on error.
// Use it when invalid mappings should fail fast during startup.
func MustCompile(dialect Dialect, mappings ...Mapping) *Mapper {
	mapper, err := Compile(dialect, mappings...)
	if err != nil {
		panic(err)
	}
	return mapper
}

// WithConverter registers a bidirectional type converter for a field.
// F is the Go field type, C is the DB column type. toDB converts on save,
// fromDB converts on load. Pass it only to Map.
//
// Example — storing an enum as text:
//
//	Map(&Order{},
//	    WithConverter("Status", statusToDB, statusFromDB),
//	)
//
// Example — storing a struct as JSON:
//
//	Map(&Order{},
//	    WithConverter("Address", jsonMarshal, jsonUnmarshal),
//	)
func WithConverter[F, C any](fieldName string, toDB func(F) (C, error), fromDB func(C) (F, error)) MapOption {
	return MapOption{
		apply: func(meta *entityMetadata) {
			if meta.Converters == nil {
				meta.Converters = make(map[string]*fieldConverter)
			}
			meta.Converters[fieldName] = &fieldConverter{
				toDB:   func(v any) (any, error) { return toDB(v.(F)) },
				fromDB: func(v any) (any, error) { return fromDB(v.(C)) },
			}
		},
	}
}

type entityMetadata struct {
	Schema     string
	Table      string
	Fields     []fieldMetadata
	Converters map[string]*fieldConverter
}

type fieldMetadata struct {
	// Structural metadata (from reflect)
	name       string
	typ        reflect.Type
	fieldIndex int // Index of the field in the struct (for reflect.Value.Field)

	// Tag parsing results
	primaryTag    bool
	parentalTag   bool
	childTag      bool
	skipInsertTag bool
	skipUpdateTag bool
	ignoreTag     bool
	columnTag     string // Custom column name from tag, "" if not specified

	// Computed values
	defaultColumn string // snake_case version of name
}

func analyzeStruct(structType reflect.Type) []fieldMetadata {
	if structType.Kind() != reflect.Struct {
		return nil
	}

	var fields []fieldMetadata

	for i := 0; i < structType.NumField(); i++ {
		structField := structType.Field(i)

		if !structField.IsExported() {
			continue
		}

		metadata := fieldMetadata{
			name:          structField.Name,
			typ:           structField.Type,
			fieldIndex:    i,
			defaultColumn: toSnakeCase(structField.Name),
		}

		tagValue := structField.Tag.Get("agg")
		if tagValue != "" {
			if tagValue == "-" {
				metadata.ignoreTag = true
			} else {
				parts := strings.Split(tagValue, ",")
				for _, part := range parts {
					part = strings.TrimSpace(part)

					switch {
					case part == "primary":
						metadata.primaryTag = true
					case part == "parental":
						metadata.parentalTag = true
					case part == "child":
						metadata.childTag = true
					case part == "auto":
						// auto is shorthand for skip_insert,skip_update
						metadata.skipInsertTag = true
						metadata.skipUpdateTag = true
					case part == "skip_insert":
						metadata.skipInsertTag = true
					case part == "skip_update":
						metadata.skipUpdateTag = true
					case strings.HasPrefix(part, "column:"):
						metadata.columnTag = strings.TrimPrefix(part, "column:")
					}
				}
			}
		}

		fields = append(fields, metadata)
	}

	return fields
}

func buildEntityMappings(
	meta map[reflect.Type]entityMetadata,
	registered map[reflect.Type]bool,
) mappingRegistry {
	result := make(mappingRegistry, len(meta))

	for entityType, entityMeta := range meta {
		mapping := buildSingleMapping(entityType, entityMeta, registered)
		result[entityType] = mapping
	}

	return result
}

func validateMappings(registry mappingRegistry) error {
	for _, em := range registry {
		if len(em.primaryKey) == 0 {
			return fmt.Errorf("Compile: entity %s has no primary key", em.entityType)
		}
		if len(em.saveLayout.generatedPrimaryIndexes) > 0 && len(em.primaryKey) != 1 {
			return fmt.Errorf("%w: entity %s has generated composite primary key", ErrUnsupportedSemantic, em.entityType)
		}

		for _, childField := range em.childFields {
			child := em.childMap[childField]
			if child.target == nil {
				return fmt.Errorf("Compile: child field %s.%s must be a pointer to a mapped entity or a slice of mapped entity pointers", em.entityType, childField)
			}
			childMapping, ok := registry[child.target]
			if !ok {
				return fmt.Errorf("Compile: child field %s.%s target %s is not registered", em.entityType, childField, child.target)
			}
			if len(childMapping.parentalKey) == 0 {
				return fmt.Errorf("Compile: child entity %s has no parental key for %s.%s", childMapping.entityType, em.entityType, childField)
			}
			if len(em.primaryKey) != len(childMapping.parentalKey) {
				return fmt.Errorf("Compile: parent key %s.%v and child parental key %s.%v have different arity", em.entityType, em.primaryKey, childMapping.entityType, childMapping.parentalKey)
			}
			for i, parentField := range em.primaryKey {
				childField := childMapping.parentalKey[i]
				parentType := em.fieldMap[parentField].typ
				childType := childMapping.fieldMap[childField].typ
				if parentType != childType {
					return fmt.Errorf("Compile: parent key %s.%s type %s does not match child parental key %s.%s type %s", em.entityType, parentField, parentType, childMapping.entityType, childField, childType)
				}
			}
		}
	}
	return nil
}

func buildSingleMapping(
	entityType reflect.Type,
	entityMeta entityMetadata,
	registered map[reflect.Type]bool,
) *entityMapping {
	fieldMap := make(map[string]*field)
	childMap := make(map[string]*child)
	childFields := []string{}
	allFields := []string{}
	primaryKey := []string{}
	parentalKey := []string{}
	insertable := []string{}
	updatable := []string{}

	for _, metadata := range entityMeta.Fields {
		fieldName := metadata.name
		fieldType := metadata.typ

		if metadata.ignoreTag {
			continue
		}

		isChild := metadata.childTag
		var childTarget reflect.Type
		var childSingular bool

		if !isChild {
			if fieldType.Kind() == reflect.Slice {
				elemType := fieldType.Elem()
				if elemType.Kind() == reflect.Ptr {
					targetType := elemType.Elem()
					if registered[targetType] {
						isChild = true
						childTarget = targetType
						childSingular = false
					}
				}
			}

			if !isChild && fieldType.Kind() == reflect.Ptr {
				targetType := fieldType.Elem()
				if registered[targetType] {
					isChild = true
					childTarget = targetType
					childSingular = true
				}
			}
		} else {
			if fieldType.Kind() == reflect.Slice {
				elemType := fieldType.Elem()
				if elemType.Kind() == reflect.Ptr {
					childTarget = elemType.Elem()
					childSingular = false
				}
			} else if fieldType.Kind() == reflect.Ptr {
				childTarget = fieldType.Elem()
				childSingular = true
			}
		}

		if isChild {
			child := child{
				target:     childTarget,
				singular:   childSingular,
				typ:        fieldType,
				fieldIndex: metadata.fieldIndex,
			}
			childMap[fieldName] = &child
			childFields = append(childFields, fieldName)
			continue
		}

		if fieldType.Kind() == reflect.Slice {
			continue
		}

		if fieldType.Kind() == reflect.Ptr && fieldType.Elem().Kind() == reflect.Struct {
			continue
		}

		columnName := metadata.columnTag
		if columnName == "" {
			columnName = metadata.defaultColumn
		}

		f := field{
			name:       metadata.name,
			column:     columnName,
			typ:        metadata.typ,
			fieldIndex: metadata.fieldIndex,
			converter:  entityMeta.Converters[fieldName],
		}

		fieldMap[fieldName] = &f

		isPrimaryKey := metadata.primaryTag
		isParentalKey := metadata.parentalTag

		if !isPrimaryKey && !isParentalKey && fieldName == "ID" {
			isPrimaryKey = true
		}

		allFields = append(allFields, fieldName)

		if isPrimaryKey {
			primaryKey = append(primaryKey, fieldName)
		}
		if isParentalKey {
			parentalKey = append(parentalKey, fieldName)
		}

		if isPrimaryKey || isParentalKey {
			if !metadata.skipInsertTag {
				insertable = append(insertable, fieldName)
			}
		} else {
			if !metadata.skipInsertTag {
				insertable = append(insertable, fieldName)
			}
			if !metadata.skipUpdateTag {
				updatable = append(updatable, fieldName)
			}
		}
	}

	return newEntityMapping(
		entityType,
		entityMeta.Schema,
		entityMeta.Table,
		fieldMap,
		childMap,
		childFields,
		allFields,
		primaryKey,
		parentalKey,
		insertable,
		updatable,
	)
}

func toSnakeCase(s string) string {
	var result strings.Builder
	for i, r := range s {
		if i > 0 && r >= 'A' && r <= 'Z' {
			if i > 0 {
				prevRune := rune(s[i-1])
				if prevRune >= 'a' && prevRune <= 'z' {
					result.WriteRune('_')
				} else if i+1 < len(s) {
					nextRune := rune(s[i+1])
					if nextRune >= 'a' && nextRune <= 'z' {
						result.WriteRune('_')
					}
				}
			}
		}
		result.WriteRune(r)
	}
	return strings.ToLower(result.String())
}

// --- Mapper ---

// Mapper stores compiled mappings and executes aggregate persistence operations.
type Mapper struct {
	dialect  Dialect
	mappings mappingRegistry
}

func (m *Mapper) getMapping(entityType reflect.Type) (*entityMapping, error) {
	return m.mappings.get(entityType)
}

// Get loads a single aggregate by primary key into dest.
//
// dest must be a pointer to an entity pointer, for example:
//
//	var order *Order
//	err := mapper.Get(ctx, tx, &order, NewKey(id))
//
// When the row does not exist, Get leaves *dest as nil.
func (m *Mapper) Get(ctx context.Context, db DBTX, dest any, id Key) error {
	entityType, err := unwrapDestEntityType(dest, "Get")
	if err != nil {
		return err
	}

	mapping, err := m.getMapping(entityType)
	if err != nil {
		return err
	}

	u := newPersistence(m.mappings, m.dialect.newBackend(db))
	entities, err := u.getByKeys(ctx, mapping, []Key{id})
	if err != nil {
		return err
	}

	destVal := reflect.ValueOf(dest).Elem()
	destVal.Set(reflect.Zero(destVal.Type()))
	if len(entities) > 0 {
		destVal.Set(reflect.ValueOf(entities[0]))
	}

	return nil
}

// GetMany loads aggregates by primary key into dest.
//
// dest must be a pointer to a slice of entity pointers, for example:
//
//	var orders []*Order
//	err := mapper.GetMany(ctx, tx, &orders, []Key{NewKey(id1), NewKey(id2)})
//
// The result slice has the same length and order as ids. Missing rows are nil.
func (m *Mapper) GetMany(ctx context.Context, db DBTX, dest any, ids []Key) error {
	entityType, err := unwrapDestSliceEntityType(dest, "GetMany")
	if err != nil {
		return err
	}

	mapping, err := m.getMapping(entityType)
	if err != nil {
		return err
	}

	u := newPersistence(m.mappings, m.dialect.newBackend(db))
	entities, err := u.getByKeys(ctx, mapping, uniqueKeys(ids))
	if err != nil {
		return err
	}

	entitiesByKey := make(map[Key]any, len(entities))
	for _, entity := range entities {
		entitiesByKey[mapping.extractPrimaryKey(entity)] = entity
	}

	destVal := reflect.ValueOf(dest).Elem()
	result := reflect.MakeSlice(destVal.Type(), len(ids), len(ids))
	for i, id := range ids {
		if entity, ok := entitiesByKey[id]; ok {
			result.Index(i).Set(reflect.ValueOf(entity))
		}
	}
	destVal.Set(result)

	return nil
}

// Save persists the authoritative aggregate snapshot.
//
// Use Save with a full authoritative aggregate state. Omitted children are
// treated as removed from the aggregate. Auto primary keys use zero as insert
// intent and non-zero as update intent.
func (m *Mapper) Save(ctx context.Context, db DBTX, entity any) error {
	entityType, err := unwrapEntityType(entity, "Save")
	if err != nil {
		return err
	}

	mapping, err := m.getMapping(entityType)
	if err != nil {
		return err
	}

	return newPersistence(m.mappings, m.dialect.newBackend(db)).save(ctx, mapping, []any{entity})
}

// SaveMany persists authoritative aggregate snapshots in one batched operation.
//
// entities must be a slice of entity pointers of one mapped type.
func (m *Mapper) SaveMany(ctx context.Context, db DBTX, entities any) error {
	entityType, values, err := unwrapEntitySlice(entities, "SaveMany")
	if err != nil {
		return err
	}

	mapping, err := m.getMapping(entityType)
	if err != nil {
		return err
	}

	return newPersistence(m.mappings, m.dialect.newBackend(db)).save(ctx, mapping, values)
}

// Delete removes the aggregate and its descendants from the database.
//
// entity only needs to provide the primary key fields used to identify the root.
func (m *Mapper) Delete(ctx context.Context, db DBTX, entity any) error {
	entityType, err := unwrapEntityType(entity, "Delete")
	if err != nil {
		return err
	}

	mapping, err := m.getMapping(entityType)
	if err != nil {
		return err
	}

	u := newPersistence(m.mappings, m.dialect.newBackend(db))
	key := mapping.extractPrimaryKey(entity)
	return u.deleteByKeys(ctx, mapping, []Key{key})
}

// DeleteMany removes aggregates and their descendants in one batched operation.
//
// entities only need to provide the primary key fields used to identify roots.
// entities must be a slice of entity pointers of one mapped type.
func (m *Mapper) DeleteMany(ctx context.Context, db DBTX, entities any) error {
	entityType, values, err := unwrapEntitySlice(entities, "DeleteMany")
	if err != nil {
		return err
	}

	mapping, err := m.getMapping(entityType)
	if err != nil {
		return err
	}

	keys := make([]Key, len(values))
	for i, entity := range values {
		keys[i] = mapping.extractPrimaryKey(entity)
	}

	return newPersistence(m.mappings, m.dialect.newBackend(db)).deleteByKeys(ctx, mapping, uniqueKeys(keys))
}

func unwrapDestEntityType(dest any, op string) (reflect.Type, error) {
	destType := reflect.TypeOf(dest)
	if destType == nil {
		return nil, fmt.Errorf("%s: dest must be a pointer to entity pointer, got <nil>", op)
	}
	if destType.Kind() != reflect.Ptr {
		return nil, fmt.Errorf("%s: dest must be a pointer to entity pointer, got %s", op, destType)
	}
	if destType.Elem().Kind() != reflect.Ptr {
		return nil, fmt.Errorf("%s: dest must be a pointer to entity pointer, got %s", op, destType)
	}
	entityType := destType.Elem().Elem()
	if entityType.Kind() != reflect.Struct {
		return nil, fmt.Errorf("%s: entity type must be a struct, got %s", op, entityType)
	}
	return entityType, nil
}

func unwrapDestSliceEntityType(dest any, op string) (reflect.Type, error) {
	destType := reflect.TypeOf(dest)
	if destType == nil {
		return nil, fmt.Errorf("%s: dest must be a pointer to a slice of entity pointers, got <nil>", op)
	}
	if destType.Kind() != reflect.Ptr {
		return nil, fmt.Errorf("%s: dest must be a pointer to a slice of entity pointers, got %s", op, destType)
	}
	sliceType := destType.Elem()
	if sliceType.Kind() != reflect.Slice {
		return nil, fmt.Errorf("%s: dest must be a pointer to a slice of entity pointers, got %s", op, destType)
	}
	elemType := sliceType.Elem()
	if elemType.Kind() != reflect.Ptr {
		return nil, fmt.Errorf("%s: dest slice element must be an entity pointer, got %s", op, elemType)
	}
	entityType := elemType.Elem()
	if entityType.Kind() != reflect.Struct {
		return nil, fmt.Errorf("%s: entity type must be a struct, got %s", op, entityType)
	}
	return entityType, nil
}

func unwrapEntityType(entity any, op string) (reflect.Type, error) {
	entityType := reflect.TypeOf(entity)
	if entityType == nil {
		return nil, fmt.Errorf("%s: entity must be a pointer to struct, got <nil>", op)
	}
	if entityType.Kind() != reflect.Ptr {
		return nil, fmt.Errorf("%s: entity must be a pointer to struct, got %s", op, entityType)
	}
	entityType = entityType.Elem()
	if entityType.Kind() != reflect.Struct {
		return nil, fmt.Errorf("%s: entity must be a pointer to struct, got %s", op, reflect.TypeOf(entity))
	}
	return entityType, nil
}

func unwrapEntitySlice(entities any, op string) (reflect.Type, []any, error) {
	entitiesType := reflect.TypeOf(entities)
	if entitiesType == nil {
		return nil, nil, fmt.Errorf("%s: entities must be a slice of entity pointers, got <nil>", op)
	}
	if entitiesType.Kind() != reflect.Slice && entitiesType.Kind() != reflect.Array {
		return nil, nil, fmt.Errorf("%s: entities must be a slice of entity pointers, got %s", op, entitiesType)
	}
	elemType := entitiesType.Elem()
	if elemType.Kind() != reflect.Ptr {
		return nil, nil, fmt.Errorf("%s: entity slice element must be a pointer to struct, got %s", op, elemType)
	}
	entityType := elemType.Elem()
	if entityType.Kind() != reflect.Struct {
		return nil, nil, fmt.Errorf("%s: entity type must be a struct, got %s", op, entityType)
	}

	entitiesValue := reflect.ValueOf(entities)
	values := make([]any, entitiesValue.Len())
	for i := range values {
		entity := entitiesValue.Index(i)
		if entity.IsNil() {
			return nil, nil, fmt.Errorf("%s: entity at index %d must be a pointer to struct, got <nil>", op, i)
		}
		values[i] = entity.Interface()
	}
	return entityType, values, nil
}

// --- Query ---

// OrderExpr is an opaque ORDER BY expression created by Query.Asc or Query.Desc.
type OrderExpr struct {
	expr      sqlNode
	ascending bool
	nullsLast bool
}

// Query provides a typed read API for one mapped entity type.
// Create it with NewQuery.
type Query[T any] struct {
	mapper       *Mapper
	db           DBTX
	mapping      *entityMapping
	alias        string
	joins        []join
	joinIndexes  map[string]int
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
		alias = mapping.table
	}

	return &Query[T]{
		mapper:       mapper,
		db:           db,
		mapping:      mapping,
		alias:        alias,
		joins:        []join{},
		joinIndexes:  make(map[string]int),
		whereConds:   []sqlNode{},
		havingConds:  []sqlNode{},
		orderByOpts:  []OrderExpr{},
		groupByExprs: []sqlNode{},
	}
}

// Join adds an INNER JOIN clause.
func (q *Query[T]) Join(table string, alias string, on string, params ...any) *Query[T] {
	q.setJoin(alias, join{
		typ:   "JOIN",
		table: sqlText{text: table},
		alias: sqlText{text: alias},
		on:    parseSQL(on, params...),
	})
	return q
}

// LeftJoin adds a LEFT JOIN clause.
func (q *Query[T]) LeftJoin(table string, alias string, on string, params ...any) *Query[T] {
	q.setJoin(alias, join{
		typ:   "LEFT JOIN",
		table: sqlText{text: table},
		alias: sqlText{text: alias},
		on:    parseSQL(on, params...),
	})
	return q
}

func (q *Query[T]) setJoin(alias string, join join) {
	if index, ok := q.joinIndexes[alias]; ok {
		q.joins[index] = join
		return
	}
	q.joinIndexes[alias] = len(q.joins)
	q.joins = append(q.joins, join)
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
	for _, fieldName := range q.mapping.primaryKey {
		field := q.mapping.fieldMap[fieldName]
		q.groupByExprs = append(q.groupByExprs, sqlQN{part1: q.alias, part2: field.column})
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
	selectCols := make([]sqlNode, len(q.mapping.allFields))
	for i, fieldName := range q.mapping.allFields {
		field := q.mapping.fieldMap[fieldName]
		selectCols[i] = sqlQN{part1: q.alias, part2: field.column}
	}
	return selectCols
}

func (q *Query[T]) buildPrimaryKeyColumns() []sqlNode {
	selectCols := make([]sqlNode, 0, len(q.mapping.primaryKey))
	for _, fieldName := range q.mapping.primaryKey {
		field := q.mapping.fieldMap[fieldName]
		selectCols = append(selectCols, sqlQN{part1: q.alias, part2: field.column})
	}
	return selectCols
}

func (q *Query[T]) buildJoins() []join {
	joins := make([]join, len(q.joins))
	copy(joins, q.joins)
	return joins
}

func (q *Query[T]) buildWhereClause() *sqlNode {
	if len(q.whereConds) == 0 {
		return nil
	}
	var w sqlNode = sqlAll{els: q.whereConds}
	return &w
}

func (q *Query[T]) buildHavingClause() *sqlNode {
	if len(q.havingConds) == 0 {
		return nil
	}
	var h sqlNode = sqlAll{els: q.havingConds}
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
		var l sqlNode = sqlParam{value: *limit}
		limitSQL = &l
	}
	if q.offset != nil {
		var o sqlNode = sqlParam{value: *q.offset}
		offsetSQL = &o
	}

	stmt := sqlQuery{
		selectColumns: q.buildSelectColumns(),
		fromTable:     sqlQN{part1: q.mapping.schema, part2: q.mapping.table},
		fromAlias:     sqlText{text: q.alias},
		joins:         q.buildJoins(),
		where:         q.buildWhereClause(),
		groupBy:       q.buildGroupBy(),
		having:        q.buildHavingClause(),
		orderBys:      q.orderByOpts,
		limit:         limitSQL,
		offset:        offsetSQL,
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
	entities, err := u.scanEntities(q.mapping, rows, expectedCapacity)
	if err != nil {
		return nil, err
	}
	if len(entities) > 0 && len(q.mapping.childMap) > 0 {
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
		selectColumns: q.buildPrimaryKeyColumns(),
		fromTable:     sqlQN{part1: q.mapping.schema, part2: q.mapping.table},
		fromAlias:     sqlText{text: q.alias},
		joins:         q.buildJoins(),
		where:         q.buildWhereClause(),
		groupBy:       q.buildGroupBy(),
		having:        q.buildHavingClause(),
	}

	return q.mapper.dialect.newBackend(q.db).CountQuery(ctx, stmt)
}

// --- Compiled mappings ---

type mappingRegistry map[reflect.Type]*entityMapping

func (r mappingRegistry) get(entityType reflect.Type) (*entityMapping, error) {
	mapping, ok := r[entityType]
	if !ok {
		return nil, fmt.Errorf("no mapping found for type %s", entityType)
	}
	return mapping, nil
}

type fieldConverter struct {
	toDB   func(any) (any, error)
	fromDB func(any) (any, error)
}

type convertingScanner struct {
	target reflect.Value
	fromDB func(any) (any, error)
}

func (c *convertingScanner) Scan(src any) error {
	converted, err := c.fromDB(src)
	if err != nil {
		return err
	}
	c.target.Set(reflect.ValueOf(converted))
	return nil
}

type field struct {
	name       string
	column     string
	typ        reflect.Type
	fieldIndex int
	converter  *fieldConverter
}

func (f *field) ptrFrom(entity reflect.Value) any {
	if f.converter != nil {
		return &convertingScanner{
			target: entity.Field(f.fieldIndex),
			fromDB: f.converter.fromDB,
		}
	}
	return entity.Field(f.fieldIndex).Addr().Interface()
}

func (f *field) valueFrom(entity reflect.Value) any {
	return entity.Field(f.fieldIndex).Interface()
}

func (f *field) setOn(entity reflect.Value, value any) {
	field := entity.Field(f.fieldIndex)
	if value == nil {
		field.Set(reflect.Zero(field.Type()))
		return
	}
	v := reflect.ValueOf(value)
	if v.Type().AssignableTo(field.Type()) {
		field.Set(v)
		return
	}
	if v.Type().ConvertibleTo(field.Type()) {
		field.Set(v.Convert(field.Type()))
		return
	}
	field.Set(v)
}

type fieldPlan struct {
	fields  []*field
	columns []string
	types   []reflect.Type
}

func newFieldPlan(fieldMap map[string]*field, fieldNames []string) fieldPlan {
	plan := fieldPlan{
		fields:  make([]*field, 0, len(fieldNames)),
		columns: make([]string, 0, len(fieldNames)),
		types:   make([]reflect.Type, 0, len(fieldNames)),
	}
	for _, name := range fieldNames {
		field := fieldMap[name]
		plan.fields = append(plan.fields, field)
		plan.columns = append(plan.columns, field.column)
		plan.types = append(plan.types, field.typ)
	}
	return plan
}

type child struct {
	target     reflect.Type
	singular   bool
	typ        reflect.Type
	fieldIndex int // Index of the field in the struct for direct reflect access
}

func (c *child) count(entityPtr any) int {
	sv := reflect.ValueOf(entityPtr).Elem()
	fieldPtr := sv.Field(c.fieldIndex)
	if c.singular {
		if fieldPtr.IsNil() {
			return 0
		}
		return 1
	}
	return fieldPtr.Len()
}

func (c *child) appendTo(entityPtr any, dst []any) []any {
	sv := reflect.ValueOf(entityPtr).Elem()
	fieldPtr := sv.Field(c.fieldIndex)
	if c.singular {
		if fieldPtr.IsNil() {
			return dst
		}
		return append(dst, fieldPtr.Interface())
	}

	for j := 0; j < fieldPtr.Len(); j++ {
		dst = append(dst, fieldPtr.Index(j).Interface())
	}
	return dst
}

func (c *child) setByParentIndexes(parents []any, children []any, parentIndexes []int, counts []int) {
	if c.singular {
		for _, parent := range parents {
			entity := reflect.ValueOf(parent).Elem()
			entity.Field(c.fieldIndex).Set(reflect.Zero(c.typ))
		}

		clear(counts)
		for i, childEntity := range children {
			parentIndex := parentIndexes[i]
			if parentIndex < 0 || counts[parentIndex] > 0 {
				continue
			}
			entity := reflect.ValueOf(parents[parentIndex]).Elem()
			entity.Field(c.fieldIndex).Set(reflect.ValueOf(childEntity))
			counts[parentIndex] = 1
		}
		return
	}

	parentSlices := make([]reflect.Value, len(parents))
	for i, parent := range parents {
		sliceVal := reflect.MakeSlice(c.typ, counts[i], counts[i])
		parentSlices[i] = sliceVal
		entity := reflect.ValueOf(parent).Elem()
		entity.Field(c.fieldIndex).Set(sliceVal)
	}

	clear(counts)
	for i, childEntity := range children {
		parentIndex := parentIndexes[i]
		if parentIndex < 0 {
			continue
		}
		index := counts[parentIndex]
		parentSlices[parentIndex].Index(index).Set(reflect.ValueOf(childEntity))
		counts[parentIndex]++
	}
}

type saveLayout struct {
	rowFields       []*field
	returningFields []*field

	rowColumns                        []string
	insertColumns                     []string
	insertIndexes                     []int
	insertColumnsWithGeneratedPrimary []string
	updateColumns                     []string
	primaryColumns                    []string
	primaryIndexes                    []int
	generatedPrimaryColumns           []string
	generatedPrimaryIndexes           []int
	returningColumns                  []string

	keyFromReturning func([]any) Key
}

func (sl *saveLayout) projectEntity(entity any) ([]any, Key, error) {
	entityValue := reflect.ValueOf(entity).Elem()
	values := make([]any, len(sl.rowFields))
	for j, field := range sl.rowFields {
		values[j] = field.valueFrom(entityValue)
	}

	var keyValues [9]any
	if len(sl.primaryIndexes) > len(keyValues) {
		panic("agg: Key supports up to 9 column values")
	}
	for j, idx := range sl.primaryIndexes {
		keyValues[j] = values[idx]
	}
	key := newKeyFromValues(keyValues[:len(sl.primaryIndexes)])

	for j, field := range sl.rowFields {
		if field.converter != nil {
			val, err := field.converter.toDB(values[j])
			if err != nil {
				return nil, Key{}, fmt.Errorf("field %s: %w", field.name, err)
			}
			values[j] = val
		}
	}

	return values, key, nil
}

func (sl *saveLayout) isInsert(values []any) (bool, error) {
	generatedIndexes := sl.generatedPrimaryIndexes
	if len(generatedIndexes) == 0 {
		return false, nil
	}
	zeroCount := 0
	for _, idx := range generatedIndexes {
		value := values[idx]
		if value == nil {
			zeroCount++
			continue
		}
		v := reflect.ValueOf(value)
		if !v.IsValid() || v.IsZero() {
			zeroCount++
		}
	}
	switch zeroCount {
	case len(generatedIndexes):
		return true, nil
	case 0:
		return false, nil
	default:
		return false, fmt.Errorf("%w: generated primary key fields must be all zero or all non-zero", ErrUnsupportedSemantic)
	}
}

func (sl *saveLayout) hasGeneratedKey() bool {
	return len(sl.generatedPrimaryIndexes) > 0
}

func (sl *saveLayout) projectRows(entities []any) (inserts, candidates []plannedRow, err error) {
	submittedKeys := make(map[Key]int, len(entities))

	for i, entity := range entities {
		values, key, err := sl.projectEntity(entity)
		if err != nil {
			return nil, nil, err
		}
		planned := plannedRow{index: i, key: key, values: values}

		insert, err := sl.isInsert(values)
		if err != nil {
			return nil, nil, err
		}

		if insert {
			inserts = append(inserts, planned)
		} else {
			if previous, ok := submittedKeys[planned.key]; ok {
				return nil, nil, fmt.Errorf("%w: duplicate submitted key %v at entity indexes %d and %d", ErrConsistency, planned.key, previous, i)
			}
			submittedKeys[planned.key] = i
			candidates = append(candidates, planned)
		}
	}

	return inserts, candidates, nil
}

func (sl *saveLayout) newInsertOp(schema, table string, rows []plannedRow) insertOp {
	return insertOp{
		schema:                            schema,
		table:                             table,
		rows:                              rows,
		insertColumns:                     sl.insertColumns,
		insertIndexes:                     sl.insertIndexes,
		insertColumnsWithGeneratedPrimary: sl.insertColumnsWithGeneratedPrimary,
		generatedPrimaryColumns:           sl.generatedPrimaryColumns,
		primaryColumns:                    sl.primaryColumns,
		returningColumns:                  sl.returningColumns,
		keyFromReturning:                  sl.keyFromReturning,
	}
}

func (sl *saveLayout) newUpdateOp(schema, table string, rows []plannedRow) updateOp {
	return updateOp{
		schema:           schema,
		table:            table,
		rows:             rows,
		rowColumns:       sl.rowColumns,
		primaryColumns:   sl.primaryColumns,
		updateColumns:    sl.updateColumns,
		returningColumns: sl.returningColumns,
	}
}

type entityMapping struct {
	entityType  reflect.Type
	schema      string
	table       string
	fieldMap    map[string]*field
	childMap    map[string]*child
	childFields []string
	allFields   []string
	primaryKey  []string
	parentalKey []string

	allPlan        fieldPlan
	primaryPlan    fieldPlan
	parentalPlan   fieldPlan
	insertablePlan fieldPlan
	updatablePlan  fieldPlan

	relationColumns []string // parentalPlan.columns + primaryPlan.columns

	saveLayout *saveLayout
}

func newEntityMapping(
	entityType reflect.Type,
	schema string,
	table string,
	fieldMap map[string]*field,
	childMap map[string]*child,
	childFields []string,
	allFields []string,
	primaryKey []string,
	parentalKey []string,
	insertable []string,
	updatable []string,
) *entityMapping {
	em := &entityMapping{
		entityType:  entityType,
		schema:      schema,
		table:       table,
		fieldMap:    fieldMap,
		childMap:    childMap,
		childFields: childFields,
		allFields:   allFields,
		primaryKey:  primaryKey,
		parentalKey: parentalKey,
	}
	em.allPlan = newFieldPlan(fieldMap, allFields)
	em.primaryPlan = newFieldPlan(fieldMap, primaryKey)
	em.parentalPlan = newFieldPlan(fieldMap, parentalKey)
	em.insertablePlan = newFieldPlan(fieldMap, insertable)
	em.updatablePlan = newFieldPlan(fieldMap, updatable)
	em.saveLayout = newSaveLayout(fieldMap, allFields, primaryKey, insertable, updatable)

	if len(parentalKey) > 0 {
		em.relationColumns = make([]string, 0, len(em.parentalPlan.columns)+len(em.primaryPlan.columns))
		em.relationColumns = append(em.relationColumns, em.parentalPlan.columns...)
		em.relationColumns = append(em.relationColumns, em.primaryPlan.columns...)
	}

	return em
}

func (em *entityMapping) newEntity() any {
	return reflect.New(em.entityType).Interface()
}

func (em *entityMapping) extractPrimaryKey(entity any) Key {
	return extractKeyFromFields(entity, em.primaryPlan.fields)
}

func (em *entityMapping) extractParentalKey(entity any) Key {
	return extractKeyFromFields(entity, em.parentalPlan.fields)
}

func (em *entityMapping) injectParentalKey(entity any, parentKey Key) {
	entityValue := reflect.ValueOf(entity).Elem()
	for i, field := range em.parentalPlan.fields {
		field.setOn(entityValue, parentKey.At(i))
	}
}

func extractKeyFromFields(entity any, fields []*field) Key {
	if len(fields) > 9 {
		panic("agg: Key supports up to 9 column values")
	}

	entityValue := reflect.ValueOf(entity).Elem()
	var values [9]any
	for i, field := range fields {
		values[i] = field.valueFrom(entityValue)
	}
	return newKeyFromValues(values[:len(fields)])
}

func newSaveLayout(
	fieldMap map[string]*field,
	allFields []string,
	primaryKey []string,
	insertable []string,
	updatable []string,
) *saveLayout {
	layout := &saveLayout{
		rowFields:       make([]*field, 0, len(allFields)),
		returningFields: make([]*field, 0, len(allFields)),
	}
	layout.rowColumns = make([]string, 0, len(allFields))
	layout.insertColumns = make([]string, 0, len(insertable))
	layout.insertIndexes = make([]int, 0, len(insertable))
	layout.insertColumnsWithGeneratedPrimary = make([]string, 0, len(insertable)+len(primaryKey))
	layout.updateColumns = make([]string, 0, len(updatable))
	layout.primaryColumns = make([]string, 0, len(primaryKey))
	layout.primaryIndexes = make([]int, 0, len(primaryKey))
	layout.generatedPrimaryColumns = make([]string, 0, len(primaryKey))
	layout.generatedPrimaryIndexes = make([]int, 0, len(primaryKey))
	layout.returningColumns = make([]string, 0, len(allFields))
	rowIndexByName := make(map[string]int, len(allFields))

	for _, name := range allFields {
		if slices.Contains(insertable, name) || slices.Contains(primaryKey, name) || slices.Contains(updatable, name) {
			field := fieldMap[name]
			rowIndexByName[name] = len(layout.rowFields)
			layout.rowFields = append(layout.rowFields, field)
			layout.rowColumns = append(layout.rowColumns, field.column)
		}
	}

	for i, field := range layout.rowFields {
		if slices.Contains(insertable, field.name) {
			layout.insertColumns = append(layout.insertColumns, field.column)
			layout.insertIndexes = append(layout.insertIndexes, i)
		}
		if slices.Contains(updatable, field.name) {
			layout.updateColumns = append(layout.updateColumns, field.column)
		}
	}

	for _, name := range primaryKey {
		column := fieldMap[name].column
		layout.primaryColumns = append(layout.primaryColumns, column)
		layout.primaryIndexes = append(layout.primaryIndexes, rowIndexByName[name])
		if !slices.Contains(insertable, name) {
			layout.generatedPrimaryColumns = append(layout.generatedPrimaryColumns, column)
			layout.generatedPrimaryIndexes = append(layout.generatedPrimaryIndexes, rowIndexByName[name])
		}
	}

	layout.insertColumnsWithGeneratedPrimary = append(layout.insertColumnsWithGeneratedPrimary, layout.generatedPrimaryColumns...)
	layout.insertColumnsWithGeneratedPrimary = append(layout.insertColumnsWithGeneratedPrimary, layout.insertColumns...)

	for _, name := range primaryKey {
		layout.returningFields = append(layout.returningFields, fieldMap[name])
	}
	for _, name := range allFields {
		if !slices.Contains(insertable, name) && !slices.Contains(primaryKey, name) {
			layout.returningFields = append(layout.returningFields, fieldMap[name])
		}
	}
	for _, field := range layout.returningFields {
		layout.returningColumns = append(layout.returningColumns, field.column)
	}

	primaryReturningIndexes := make([]int, len(primaryKey))
	primaryTypes := make([]reflect.Type, len(primaryKey))
	for i, column := range layout.primaryColumns {
		primaryReturningIndexes[i] = slices.Index(layout.returningColumns, column)
		primaryTypes[i] = fieldMap[primaryKey[i]].typ
	}
	layout.keyFromReturning = func(values []any) Key {
		var kv [9]any
		for i, idx := range primaryReturningIndexes {
			val := values[idx]
			if b, ok := val.([]byte); ok {
				val = string(b)
			}
			if val != nil {
				v := reflect.ValueOf(val)
				if !v.Type().AssignableTo(primaryTypes[i]) && v.Type().ConvertibleTo(primaryTypes[i]) {
					val = v.Convert(primaryTypes[i]).Interface()
				}
			}
			kv[i] = val
		}
		return newKeyFromValues(kv[:len(primaryReturningIndexes)])
	}

	return layout
}

// --- Persistence ---

type persistence struct {
	registry   mappingRegistry
	backend    backend
	scanBuffer []any
}

func newPersistence(registry mappingRegistry, backend backend) *persistence {
	return &persistence{
		registry: registry,
		backend:  backend,
	}
}

// --- Load ---

func (u *persistence) getByKeys(ctx context.Context, em *entityMapping, ids []Key) ([]any, error) {
	return u.loadEntities(ctx, em, em.primaryKey, em.primaryPlan.columns, ids)
}

func (u *persistence) getByParentKeys(ctx context.Context, em *entityMapping, parentKeys []Key) ([]any, error) {
	return u.loadEntities(ctx, em, em.parentalKey, em.parentalPlan.columns, parentKeys)
}

func (u *persistence) loadEntities(ctx context.Context, em *entityMapping, keyNames []string, keyColumns []string, keys []Key) ([]any, error) {
	if len(keys) == 0 {
		return nil, nil
	}

	for _, key := range keys {
		if key.Length() != len(keyNames) {
			return nil, fmt.Errorf("key has %d values but %d key columns expected (entity %s, key columns: %v)", key.Length(), len(keyNames), em.entityType, keyNames)
		}
	}

	rowSet, err := u.backend.LoadRows(ctx, loadRowsOp{
		schema:        em.schema,
		table:         em.table,
		selectColumns: em.allPlan.columns,
		keyColumns:    keyColumns,
		keys:          keys,
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = rowSet.Close() }()

	entities, err := u.scanEntities(em, rowSet, 0)
	if err != nil {
		return nil, err
	}
	if len(entities) > 0 && len(em.childMap) > 0 {
		if err := u.loadChildren(ctx, em, entities); err != nil {
			return nil, err
		}
	}
	return entities, nil
}

func (u *persistence) scanEntity(entityPtr any, row rows, fields []*field) error {
	u.scanBuffer = u.scanBuffer[:0]
	if cap(u.scanBuffer) < len(fields) {
		u.scanBuffer = make([]any, 0, len(fields))
	}

	entityValue := reflect.ValueOf(entityPtr).Elem()
	for _, field := range fields {
		u.scanBuffer = append(u.scanBuffer, field.ptrFrom(entityValue))
	}

	return row.Scan(u.scanBuffer...)
}

func (u *persistence) scanEntities(em *entityMapping, rowSet rows, expectedCapacity int) ([]any, error) {
	entities := make([]any, 0, expectedCapacity)
	for rowSet.Next() {
		entityPtr := em.newEntity()
		if err := u.scanEntity(entityPtr, rowSet, em.allPlan.fields); err != nil {
			return nil, err
		}
		entities = append(entities, entityPtr)
	}
	return entities, nil
}

func (u *persistence) loadChildren(ctx context.Context, em *entityMapping, parents []any) error {
	parentKeys := make([]Key, len(parents))
	parentKeyToIndex := make(map[Key]int, len(parents))
	for i, parent := range parents {
		parentKeys[i] = em.extractPrimaryKey(parent)
		parentKeyToIndex[parentKeys[i]] = i
	}

	for _, childField := range em.childFields {
		child := em.childMap[childField]
		childMapping, err := u.registry.get(child.target)
		if err != nil {
			return err
		}

		childEntities, err := u.getByParentKeys(ctx, childMapping, parentKeys)
		if err != nil {
			return err
		}

		parentIndexes := make([]int, len(childEntities))
		counts := make([]int, len(parents))
		for i, childEntity := range childEntities {
			parentIndexes[i] = -1
			parentalKey := childMapping.extractParentalKey(childEntity)
			if parentIndex, ok := parentKeyToIndex[parentalKey]; ok {
				parentIndexes[i] = parentIndex
				counts[parentIndex]++
			}
		}

		child.setByParentIndexes(parents, childEntities, parentIndexes, counts)
	}

	return nil
}

// --- Save ---

func (u *persistence) save(ctx context.Context, em *entityMapping, entities []any) error {
	if len(entities) == 0 {
		return nil
	}

	inserts, candidates, err := em.saveLayout.projectRows(entities)
	if err != nil {
		return err
	}

	toInsert := inserts
	var toUpdate []plannedRow

	if len(candidates) > 0 {
		candidateKeys := make([]Key, len(candidates))
		for i, row := range candidates {
			candidateKeys[i] = row.key
		}
		existingKeys, err := u.selectExistingKeys(ctx, em, candidateKeys)
		if err != nil {
			return err
		}
		existing := newKeySet(existingKeys)

		toUpdate = make([]plannedRow, 0, len(candidates))
		for _, row := range candidates {
			if _, ok := existing[row.key]; ok {
				toUpdate = append(toUpdate, row)
			} else if em.saveLayout.hasGeneratedKey() {
				return fmt.Errorf("%w: generated key %v does not exist in %s", ErrStaleEntity, row.key, em.entityType)
			} else {
				toInsert = append(toInsert, row)
			}
		}
	}

	return u.savePlannedLevel(ctx, em, entities, toInsert, toUpdate)
}

func (u *persistence) savePlannedLevel(ctx context.Context, em *entityMapping, entities []any, toInsert, toUpdate []plannedRow) error {
	inserted, err := u.savePlannedRows(ctx, em, entities, toInsert, toUpdate)
	if err != nil {
		return err
	}
	for _, childField := range em.childFields {
		child := em.childMap[childField]
		childMapping, err := u.registry.get(child.target)
		if err != nil {
			return err
		}
		if err := u.saveChildRelation(ctx, em, childMapping, child, entities, inserted); err != nil {
			return err
		}
	}
	return nil
}

func (u *persistence) saveChildRelation(ctx context.Context, parentMapping, childMapping *entityMapping, child *child, parentEntities []any, parentInserted []bool) error {
	// Collect submitted children and inject parental keys
	childCount := 0
	for _, entity := range parentEntities {
		childCount += child.count(entity)
	}

	submitted := make([]any, 0, childCount)
	keepKeys := make(map[Key]keySet)
	existingParentKeys := make([]Key, 0, len(parentEntities))

	for i, entity := range parentEntities {
		parentKey := parentMapping.extractPrimaryKey(entity)

		if !parentInserted[i] {
			existingParentKeys = append(existingParentKeys, parentKey)
		}

		start := len(submitted)
		submitted = child.appendTo(entity, submitted)
		for _, childEntity := range submitted[start:] {
			childMapping.injectParentalKey(childEntity, parentKey)

			childKey := childMapping.extractPrimaryKey(childEntity)
			keep := keepKeys[parentKey]
			if keep == nil {
				keep = make(keySet)
				keepKeys[parentKey] = keep
			}
			keep[childKey] = struct{}{}
		}
	}

	// Load existing relation state
	existingChildKeys, existingParentByChild, err := u.loadRelationKeys(ctx, childMapping, existingParentKeys)
	if err != nil {
		return err
	}

	// Project, resolve, and save
	if len(submitted) > 0 {
		inserts, candidates, err := childMapping.saveLayout.projectRows(submitted)
		if err != nil {
			return err
		}

		toInsert := inserts
		var toUpdate []plannedRow

		for _, row := range candidates {
			parentKey := childMapping.extractParentalKey(submitted[row.index])
			childKey := row.key

			if _, ok := existingChildKeys[parentKey][childKey]; ok {
				toUpdate = append(toUpdate, row)
			} else if childMapping.saveLayout.hasGeneratedKey() {
				return fmt.Errorf("%w: generated key %v does not exist in parent relation for %s", ErrStaleEntity, childKey, childMapping.entityType)
			} else {
				if owner, ok := existingParentByChild[childKey]; ok && owner != parentKey {
					return fmt.Errorf("%w: key %v already exists under a different parent in %s", ErrConsistency, childKey, childMapping.entityType)
				}
				toInsert = append(toInsert, row)
			}
		}

		if err := u.savePlannedLevel(ctx, childMapping, submitted, toInsert, toUpdate); err != nil {
			return err
		}
	}

	// Delete orphaned children
	var toDelete []Key
	for parentKey, children := range existingChildKeys {
		keepSet := keepKeys[parentKey]
		for childKey := range children {
			if _, ok := keepSet[childKey]; !ok {
				toDelete = append(toDelete, childKey)
			}
		}
	}
	return u.deleteByKeys(ctx, childMapping, toDelete)
}

func (u *persistence) loadRelationKeys(ctx context.Context, childMapping *entityMapping, parentKeys []Key) (map[Key]keySet, map[Key]Key, error) {
	childKeysByParent := make(map[Key]keySet)
	parentByChildKey := make(map[Key]Key)
	if len(parentKeys) == 0 {
		return childKeysByParent, parentByChildKey, nil
	}

	rowSet, err := u.backend.LoadRows(ctx, loadRowsOp{
		schema:        childMapping.schema,
		table:         childMapping.table,
		selectColumns: childMapping.relationColumns,
		keyColumns:    childMapping.parentalPlan.columns,
		keys:          parentKeys,
	})
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = rowSet.Close() }()

	parentScanned, childScanned, err := u.scanKeyPairs(rowSet, childMapping.parentalPlan.types, childMapping.primaryPlan.types)
	if err != nil {
		return nil, nil, err
	}

	for i := range parentScanned {
		pk := parentScanned[i]
		ck := childScanned[i]
		children := childKeysByParent[pk]
		if children == nil {
			children = make(keySet)
			childKeysByParent[pk] = children
		}
		children[ck] = struct{}{}
		parentByChildKey[ck] = pk
	}

	return childKeysByParent, parentByChildKey, nil
}

// --- Save Execution ---

func (u *persistence) savePlannedRows(ctx context.Context, em *entityMapping, entities []any, toInsert, toUpdate []plannedRow) ([]bool, error) {
	inserted := make([]bool, len(entities))

	if len(toInsert) > 0 {
		rowSet, err := u.backend.InsertRows(ctx, em.saveLayout.newInsertOp(em.schema, em.table, toInsert))
		if err != nil {
			return nil, err
		}
		n, err := u.scanReturning(em, entities, toInsert, rowSet)
		if err != nil {
			return nil, err
		}
		if n != len(toInsert) {
			return nil, fmt.Errorf("%w: expected %d inserted rows, got %d", ErrConsistency, len(toInsert), n)
		}
		for _, row := range toInsert {
			inserted[row.index] = true
		}
	}

	if len(toUpdate) > 0 {
		rowSet, err := u.backend.UpdateRows(ctx, em.saveLayout.newUpdateOp(em.schema, em.table, toUpdate))
		if err != nil {
			return nil, err
		}
		n, err := u.scanReturning(em, entities, toUpdate, rowSet)
		if err != nil {
			return nil, err
		}
		if n != len(toUpdate) {
			return nil, fmt.Errorf("%w: expected %d updated rows, got %d", ErrStaleEntity, len(toUpdate), n)
		}
	}

	return inserted, nil
}

func (u *persistence) scanReturning(em *entityMapping, entities []any, planned []plannedRow, rowSet rows) (int, error) {
	defer func() { _ = rowSet.Close() }()

	count := 0
	for rowSet.Next() {
		if count >= len(planned) {
			return count + 1, nil
		}
		if err := u.scanEntity(entities[planned[count].index], rowSet, em.saveLayout.returningFields); err != nil {
			return 0, err
		}
		count++
	}
	return count, nil
}

func (u *persistence) selectExistingKeys(ctx context.Context, em *entityMapping, keys []Key) ([]Key, error) {
	if len(keys) == 0 {
		return nil, nil
	}
	rowSet, err := u.backend.LoadRows(ctx, loadRowsOp{
		schema:        em.schema,
		table:         em.table,
		selectColumns: em.primaryPlan.columns,
		keyColumns:    em.primaryPlan.columns,
		keys:          keys,
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = rowSet.Close() }()
	return u.scanTypedKeys(rowSet, em.primaryPlan.types)
}

// --- Delete ---

func (u *persistence) deleteByKeys(ctx context.Context, em *entityMapping, keys []Key) error {
	if len(keys) == 0 {
		return nil
	}

	for _, childField := range em.childFields {
		child := em.childMap[childField]
		childMapping, err := u.registry.get(child.target)
		if err != nil {
			return err
		}

		childKeys, err := u.loadKeysByParentKeys(ctx, childMapping, keys)
		if err != nil {
			return err
		}
		if err := u.deleteByKeys(ctx, childMapping, childKeys); err != nil {
			return err
		}
	}

	return u.backend.DeleteRowsByKeys(ctx, deleteRowsOp{
		schema:     em.schema,
		table:      em.table,
		keyColumns: em.primaryPlan.columns,
		keys:       keys,
	})
}

func (u *persistence) loadKeysByParentKeys(ctx context.Context, em *entityMapping, parentKeys []Key) ([]Key, error) {
	if len(parentKeys) == 0 {
		return nil, nil
	}

	rowSet, err := u.backend.LoadRows(ctx, loadRowsOp{
		schema:        em.schema,
		table:         em.table,
		selectColumns: em.primaryPlan.columns,
		keyColumns:    em.parentalPlan.columns,
		keys:          parentKeys,
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = rowSet.Close() }()

	return u.scanTypedKeys(rowSet, em.primaryPlan.types)
}

func (u *persistence) scanTypedKeys(rowSet rows, keyTypes []reflect.Type) ([]Key, error) {
	if len(keyTypes) == 0 {
		return nil, nil
	}
	keys := make([]Key, 0)
	values := make([]any, len(keyTypes))
	dest := make([]any, len(keyTypes))
	for i := range values {
		dest[i] = &values[i]
	}
	for rowSet.Next() {
		if err := rowSet.Scan(dest...); err != nil {
			return nil, err
		}
		for i, value := range values {
			values[i] = coerceScanned(value, keyTypes[i])
		}
		keys = append(keys, NewKey(values...))
	}
	return keys, nil
}

func (u *persistence) scanKeyPairs(rowSet rows, leftTypes, rightTypes []reflect.Type) ([]Key, []Key, error) {
	leftLen := len(leftTypes)
	values := make([]any, leftLen+len(rightTypes))
	dest := make([]any, len(values))
	for i := range values {
		dest[i] = &values[i]
	}
	var leftKeys, rightKeys []Key
	for rowSet.Next() {
		if err := rowSet.Scan(dest...); err != nil {
			return nil, nil, err
		}
		for i, value := range values[:leftLen] {
			values[i] = coerceScanned(value, leftTypes[i])
		}
		for i, value := range values[leftLen:] {
			values[leftLen+i] = coerceScanned(value, rightTypes[i])
		}
		leftKeys = append(leftKeys, NewKey(values[:leftLen]...))
		rightKeys = append(rightKeys, NewKey(values[leftLen:]...))
	}
	return leftKeys, rightKeys, nil
}

func coerceScanned(value any, target reflect.Type) any {
	if b, ok := value.([]byte); ok {
		value = string(b)
	}
	if value == nil {
		return nil
	}
	v := reflect.ValueOf(value)
	if v.Type().AssignableTo(target) {
		return value
	}
	if v.Type().ConvertibleTo(target) {
		return v.Convert(target).Interface()
	}
	return value
}

// --- Backend contract ---

type rows interface {
	Next() bool
	Scan(dest ...any) error
	Close() error
}

type backend interface {
	LoadRows(ctx context.Context, op loadRowsOp) (rows, error)
	InsertRows(ctx context.Context, op insertOp) (rows, error)
	UpdateRows(ctx context.Context, op updateOp) (rows, error)
	DeleteRowsByKeys(ctx context.Context, op deleteRowsOp) error
	FetchQuery(ctx context.Context, stmt sqlQuery) (rows, error)
	CountQuery(ctx context.Context, stmt sqlQuery) (int64, error)
}

type loadRowsOp struct {
	schema        string
	table         string
	selectColumns []string
	keyColumns    []string
	keys          []Key
}

type insertOp struct {
	schema                            string
	table                             string
	rows                              []plannedRow
	insertColumns                     []string
	insertIndexes                     []int
	insertColumnsWithGeneratedPrimary []string
	generatedPrimaryColumns           []string
	primaryColumns                    []string
	returningColumns                  []string
	keyFromReturning                  func([]any) Key
}

type updateOp struct {
	schema           string
	table            string
	rows             []plannedRow
	rowColumns       []string
	primaryColumns   []string
	updateColumns    []string
	returningColumns []string
}

type plannedRow struct {
	index  int
	key    Key
	values []any
}

type deleteRowsOp struct {
	schema     string
	table      string
	keyColumns []string
	keys       []Key
}

// --- SQL representation ---

type sqlNode interface {
	isSQL()
}

type sqlN struct{ part string }

type sqlQN struct{ part1, part2 string }

type sqlText struct{ text string }

type sqlParam struct{ value any }

type sqlAll struct{ els []sqlNode }

type sqlAny struct{ els []sqlNode }

type sqlEq struct{ left, right sqlNode }

type sqlLt struct{ left, right sqlNode }

type sqlGt struct{ left, right sqlNode }

type sqlIsNull struct{ operand sqlNode }

type sqlIsNotNull struct{ operand sqlNode }

type sqlFragment struct{ els []sqlNode }

// Marker method implementations
func (sqlN) isSQL()         {}
func (sqlQN) isSQL()        {}
func (sqlText) isSQL()      {}
func (sqlParam) isSQL()     {}
func (sqlAll) isSQL()       {}
func (sqlAny) isSQL()       {}
func (sqlEq) isSQL()        {}
func (sqlLt) isSQL()        {}
func (sqlGt) isSQL()        {}
func (sqlIsNull) isSQL()    {}
func (sqlIsNotNull) isSQL() {}
func (sqlFragment) isSQL()  {}

type join struct {
	typ   string // "JOIN" | "LEFT JOIN"
	table sqlNode
	alias sqlNode
	on    sqlNode
}

type sqlQuery struct {
	selectColumns []sqlNode
	fromTable     sqlNode
	fromAlias     sqlNode
	joins         []join
	where         *sqlNode
	orderBys      []OrderExpr
	groupBy       []sqlNode
	having        *sqlNode
	limit         *sqlNode
	offset        *sqlNode
}

func parseSQL(sql string, params ...any) sqlNode {
	// Pre-allocate tokens slice with correct capacity
	// N params means N params + (N+1) text segments = 2N+1 tokens
	tokens := make([]sqlNode, 0, 2*len(params)+1)
	paramIdx := 0
	var textBuilder strings.Builder
	inSingleQuote := false
	inDoubleQuote := false

	for i := 0; i < len(sql); i++ {
		ch := sql[i]

		if ch == '\'' && !inDoubleQuote {
			inSingleQuote = !inSingleQuote
			textBuilder.WriteByte(ch)
		} else if ch == '"' && !inSingleQuote {
			inDoubleQuote = !inDoubleQuote
			textBuilder.WriteByte(ch)
		} else if ch == '?' && !inSingleQuote && !inDoubleQuote {
			// Only treat ? as parameter placeholder if not inside quotes
			if textBuilder.Len() > 0 {
				tokens = append(tokens, sqlText{text: textBuilder.String()})
				textBuilder.Reset()
			}
			if paramIdx >= len(params) {
				panic(fmt.Sprintf("Not enough parameters: expected at least %d, got %d", paramIdx+1, len(params)))
			}
			tokens = append(tokens, sqlParam{value: params[paramIdx]})
			paramIdx++
		} else {
			textBuilder.WriteByte(ch)
		}
	}

	if textBuilder.Len() > 0 {
		tokens = append(tokens, sqlText{text: textBuilder.String()})
	}

	if paramIdx != len(params) {
		panic(fmt.Sprintf("Too many parameters: expected %d, got %d", paramIdx, len(params)))
	}

	return sqlFragment{els: tokens}
}
