package ormapper

import (
	"reflect"
	"strings"
)

type fieldMetadata struct {
	// Structural metadata (from reflect)
	Name       string
	Typ        reflect.Type
	FieldIndex int // Index of the field in the struct (for reflect.Value.Field)

	// Tag parsing results
	PrimaryTag    bool
	ParentalTag   bool
	ChildTag      bool
	SkipInsertTag bool
	SkipUpdateTag bool
	IgnoreTag     bool
	ColumnTag     string // Custom column name from tag, "" if not specified

	// Computed values
	DefaultColumn string // snake_case version of name
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
			Name:          structField.Name,
			Typ:           structField.Type,
			FieldIndex:    i,
			DefaultColumn: toSnakeCase(structField.Name),
		}

		tagValue := structField.Tag.Get("ormapper")
		if tagValue != "" {
			if tagValue == "-" {
				metadata.IgnoreTag = true
			} else {
				parts := strings.Split(tagValue, ",")
				for _, part := range parts {
					part = strings.TrimSpace(part)

					switch {
					case part == "primary":
						metadata.PrimaryTag = true
					case part == "parental":
						metadata.ParentalTag = true
					case part == "child":
						metadata.ChildTag = true
					case part == "auto":
						// auto is shorthand for skip_insert,skip_update
						metadata.SkipInsertTag = true
						metadata.SkipUpdateTag = true
					case part == "skip_insert":
						metadata.SkipInsertTag = true
					case part == "skip_update":
						metadata.SkipUpdateTag = true
					case strings.HasPrefix(part, "column:"):
						metadata.ColumnTag = strings.TrimPrefix(part, "column:")
					}
				}
			}
		}

		fields = append(fields, metadata)
	}

	return fields
}
