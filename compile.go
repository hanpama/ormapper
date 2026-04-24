package ormapper

import (
	"fmt"
	"reflect"
	"strings"
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

type entityMetadata struct {
	Schema string
	Table  string
	Fields []fieldMetadata
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

		tagValue := structField.Tag.Get("ormapper")
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

		field := field{
			name:       metadata.name,
			column:     columnName,
			typ:        metadata.typ,
			fieldIndex: metadata.fieldIndex,
		}

		fieldMap[fieldName] = &field

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
