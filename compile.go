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

type entityMetadata struct {
	Schema string
	Table  string
	Fields []fieldMetadata
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
		fieldName := metadata.Name
		fieldType := metadata.Typ

		if metadata.IgnoreTag {
			continue
		}

		isChild := metadata.ChildTag
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
				Target:     childTarget,
				Singular:   childSingular,
				Type:       fieldType,
				FieldIndex: metadata.FieldIndex,
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

		columnName := metadata.ColumnTag
		if columnName == "" {
			columnName = metadata.DefaultColumn
		}

		field := field{
			Name:       metadata.Name,
			Column:     columnName,
			Type:       metadata.Typ,
			FieldIndex: metadata.FieldIndex,
		}

		fieldMap[fieldName] = &field

		isPrimaryKey := metadata.PrimaryTag
		isParentalKey := metadata.ParentalTag

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
			if !metadata.SkipInsertTag {
				insertable = append(insertable, fieldName)
			}
		} else {
			if !metadata.SkipInsertTag {
				insertable = append(insertable, fieldName)
			}
			if !metadata.SkipUpdateTag {
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
