package ormapper

import "reflect"

type entityMetadata struct {
	Schema string
	Table  string
	Fields []fieldMetadata // All fields analyzed from the struct
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

// buildSingleMapping constructs an EntityMapping for a single entity type.
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
			// Auto-detect child relationship if not explicitly tagged
			// Check for slice of registered type: []*Post
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

		// Default: ID field is primary key if no explicit primary tag
		if !isPrimaryKey && !isParentalKey && fieldName == "ID" {
			isPrimaryKey = true
		}

		// Add to AllFields (all fields are included regardless of role)
		allFields = append(allFields, fieldName)

		// A field can be both primary and parental (e.g., 1:1 relationship where PK=FK)
		if isPrimaryKey {
			primaryKey = append(primaryKey, fieldName)
		}
		if isParentalKey {
			parentalKey = append(parentalKey, fieldName)
		}

		if isPrimaryKey || isParentalKey {
			// Primary/parental keys are insertable unless tagged skip_insert
			if !metadata.SkipInsertTag {
				insertable = append(insertable, fieldName)
			}
			// Primary/parental keys are not updatable (they define identity)
		} else {
			// Regular fields are insertable and updatable unless tagged otherwise
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
