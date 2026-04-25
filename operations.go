package ormapper

import (
	"fmt"
	"reflect"
)

type loadRowsOp struct {
	schema        string
	table         string
	selectColumns []string
	keyColumns    []string
	keys          []Key
}

type keyScanOp struct {
	schema     string
	table      string
	keyColumns []string
	keyTypes   []reflect.Type
	keys       []Key
}

type saveRowsOp struct {
	schema    string
	table     string
	fields    []saveField
	returning []string
}

type saveRow struct {
	values []any
}

type plannedRow struct {
	index int
	row   saveRow
}

type savedRow struct {
	index  int
	values []any
}

type saveField struct {
	name       string
	column     string
	typ        reflect.Type
	primaryKey bool
	generated  bool
	insertable bool
	updatable  bool
}

type saveRowIntent int

const (
	saveRowManualKey saveRowIntent = iota
	saveRowGeneratedInsert
	saveRowGeneratedUpdate
)

type keepPair struct {
	parentKey Key
	childKey  Key
}

type deleteRowsOp struct {
	schema     string
	table      string
	keyColumns []string
	keys       []Key
}

func (op saveRowsOp) insertColumns() []string {
	columns := make([]string, 0, len(op.fields))
	for _, field := range op.fields {
		if field.insertable {
			columns = append(columns, field.column)
		}
	}
	return columns
}

func (op saveRowsOp) insertColumnsWithGeneratedPrimary() []string {
	columns := make([]string, 0, len(op.fields))
	for _, field := range op.fields {
		if field.primaryKey && field.generated {
			columns = append(columns, field.column)
		}
	}
	for _, field := range op.fields {
		if field.insertable {
			columns = append(columns, field.column)
		}
	}
	return columns
}

func (op saveRowsOp) rowColumns() []string {
	columns := make([]string, 0, len(op.fields))
	for _, field := range op.fields {
		columns = append(columns, field.column)
	}
	return columns
}

func (op saveRowsOp) insertValuesForRow(row saveRow) []any {
	indexes := make([]int, 0, len(op.fields))
	for i, field := range op.fields {
		if field.insertable {
			indexes = append(indexes, i)
		}
	}

	projected := make([]any, len(indexes))
	for j, idx := range indexes {
		projected[j] = row.values[idx]
	}
	return projected
}

func (op saveRowsOp) primaryKeyIndexes() []int {
	indexes := make([]int, 0, len(op.fields))
	for i, field := range op.fields {
		if field.primaryKey {
			indexes = append(indexes, i)
		}
	}
	return indexes
}

func (op saveRowsOp) returningIndexes(columns []string) []int {
	indexes := make([]int, 0, len(columns))
	for _, column := range columns {
		for i, returning := range op.returning {
			if returning == column {
				indexes = append(indexes, i)
				break
			}
		}
	}
	return indexes
}

func (op saveRowsOp) primaryFields() []saveField {
	fields := make([]saveField, 0)
	for _, field := range op.fields {
		if field.primaryKey {
			fields = append(fields, field)
		}
	}
	return fields
}

func (op saveRowsOp) generatedPrimaryFields() []saveField {
	fields := make([]saveField, 0)
	for _, field := range op.fields {
		if field.primaryKey && field.generated {
			fields = append(fields, field)
		}
	}
	return fields
}

func (op saveRowsOp) generatedPrimaryIndexes() []int {
	indexes := make([]int, 0)
	for i, field := range op.fields {
		if field.primaryKey && field.generated {
			indexes = append(indexes, i)
		}
	}
	return indexes
}

func (op saveRowsOp) keyFromReturnedValues(values []any) Key {
	fields := op.primaryFields()
	columns := make([]string, len(fields))
	for i, field := range fields {
		columns[i] = field.column
	}
	indexes := op.returningIndexes(columns)
	keyValues := make([]any, len(indexes))
	for i, idx := range indexes {
		keyValues[i] = coerceValue(values[idx], fields[i].typ)
	}
	return NewKey(keyValues...)
}

func (op saveRowsOp) classifyRow(row saveRow) (saveRowIntent, error) {
	generatedIndexes := op.generatedPrimaryIndexes()
	if len(generatedIndexes) == 0 {
		return saveRowManualKey, nil
	}

	zeroCount := 0
	for _, idx := range generatedIndexes {
		if valueIsZero(row.values[idx]) {
			zeroCount++
		}
	}
	switch zeroCount {
	case len(generatedIndexes):
		return saveRowGeneratedInsert, nil
	case 0:
		return saveRowGeneratedUpdate, nil
	default:
		return 0, fmt.Errorf("%w: generated primary key fields must be all zero or all non-zero", ErrUnsupportedSemantic)
	}
}

func (op saveRowsOp) keyFromRow(row saveRow) Key {
	indexes := op.primaryKeyIndexes()
	values := make([]any, len(indexes))
	for i, idx := range indexes {
		values[i] = row.values[idx]
	}
	return NewKey(values...)
}

func valueIsZero(value any) bool {
	if value == nil {
		return true
	}
	v := reflect.ValueOf(value)
	return !v.IsValid() || v.IsZero()
}

func coerceValue(value any, target reflect.Type) any {
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

func (op saveRowsOp) conflictColumns() []string {
	columns := make([]string, 0, len(op.fields))
	for _, field := range op.fields {
		if field.primaryKey {
			columns = append(columns, field.column)
		}
	}
	return columns
}

func (op saveRowsOp) updateColumns() []string {
	columns := make([]string, 0, len(op.fields))
	for _, field := range op.fields {
		if field.updatable {
			columns = append(columns, field.column)
		}
	}
	return columns
}
