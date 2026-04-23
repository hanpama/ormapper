package ormapper

import (
	"fmt"
	"reflect"
)

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
// Build it with WithTable and WithSchema.
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
		if err := validateEntityPtr(entityPtr, "Compile"); err != nil {
			return nil, err
		}

		entityType := reflect.TypeOf(entityPtr).Elem()
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

	return &Mapper{
		dialect:  dialect,
		mappings: buildEntityMappings(meta, registered),
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

func validateEntityPtr(entity any, op string) error {
	entityType := reflect.TypeOf(entity)
	if entityType == nil {
		return fmt.Errorf("%s: entity must be a pointer to struct, got <nil>", op)
	}
	if entityType.Kind() != reflect.Ptr {
		return fmt.Errorf("%s: entity must be a pointer to struct, got %s", op, entityType)
	}
	if entityType.Elem().Kind() != reflect.Struct {
		return fmt.Errorf("%s: entity must be a pointer to struct, got %s", op, entityType)
	}
	return nil
}
