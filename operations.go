package ormapper

import (
	"fmt"
	"reflect"
)

type loadRowsOp struct {
	Schema     string
	Table      string
	Select     []string
	KeyColumns []string
	Keys       []Key
}

type keyScanOp struct {
	Schema     string
	Table      string
	KeyColumns []string
	KeyTypes   []reflect.Type
	Keys       []Key
}

type saveRowsOp struct {
	Schema    string
	Table     string
	Fields    []saveField
	Returning []string
}

type saveRow struct {
	Values []any
}

type plannedRow struct {
	Index int
	Row   saveRow
}

type savedRow struct {
	Index  int
	Values []any
}

type saveRowIntent int

const (
	saveRowManualKey saveRowIntent = iota
	saveRowGeneratedInsert
	saveRowGeneratedUpdate
)

type keepPair struct {
	ParentKey Key
	ChildKey  Key
}

type selectMissingChildrenOp struct {
	Schema           string
	Table            string
	ParentKeyColumns []string
	ChildKeyColumns  []string
	ParentKeys       []Key
	KeepPairs        []keepPair
}

type deleteRowsOp struct {
	Schema     string
	Table      string
	KeyColumns []string
	Keys       []Key
}

func (op saveRowsOp) insertColumns() []string {
	columns := make([]string, 0, len(op.Fields))
	for _, field := range op.Fields {
		if field.Insertable {
			columns = append(columns, field.Column)
		}
	}
	return columns
}

func (op saveRowsOp) insertColumnsWithGeneratedPrimary() []string {
	columns := make([]string, 0, len(op.Fields))
	for _, field := range op.Fields {
		if field.PrimaryKey && field.Generated {
			columns = append(columns, field.Column)
		}
	}
	for _, field := range op.Fields {
		if field.Insertable {
			columns = append(columns, field.Column)
		}
	}
	return columns
}

func (op saveRowsOp) rowColumns() []string {
	columns := make([]string, 0, len(op.Fields))
	for _, field := range op.Fields {
		columns = append(columns, field.Column)
	}
	return columns
}

func (op saveRowsOp) insertValuesForRow(row saveRow) []any {
	indexes := make([]int, 0, len(op.Fields))
	for i, field := range op.Fields {
		if field.Insertable {
			indexes = append(indexes, i)
		}
	}

	projected := make([]any, len(indexes))
	for j, idx := range indexes {
		projected[j] = row.Values[idx]
	}
	return projected
}

func (op saveRowsOp) insertValuesForRows(rows []saveRow) [][]any {
	values := make([][]any, len(rows))
	for i, row := range rows {
		values[i] = op.insertValuesForRow(row)
	}
	return values
}

func (op saveRowsOp) primaryKeyIndexes() []int {
	indexes := make([]int, 0, len(op.Fields))
	for i, field := range op.Fields {
		if field.PrimaryKey {
			indexes = append(indexes, i)
		}
	}
	return indexes
}

func (op saveRowsOp) returningIndexes(columns []string) []int {
	indexes := make([]int, 0, len(columns))
	for _, column := range columns {
		for i, returning := range op.Returning {
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
	for _, field := range op.Fields {
		if field.PrimaryKey {
			fields = append(fields, field)
		}
	}
	return fields
}

func (op saveRowsOp) generatedPrimaryFields() []saveField {
	fields := make([]saveField, 0)
	for _, field := range op.Fields {
		if field.PrimaryKey && field.Generated {
			fields = append(fields, field)
		}
	}
	return fields
}

func (op saveRowsOp) generatedPrimaryIndexes() []int {
	indexes := make([]int, 0)
	for i, field := range op.Fields {
		if field.PrimaryKey && field.Generated {
			indexes = append(indexes, i)
		}
	}
	return indexes
}

func (op saveRowsOp) keyFromReturnedValues(values []any) Key {
	fields := op.primaryFields()
	columns := make([]string, len(fields))
	for i, field := range fields {
		columns[i] = field.Column
	}
	indexes := op.returningIndexes(columns)
	keyValues := make([]any, len(indexes))
	for i, idx := range indexes {
		keyValues[i] = coerceValue(values[idx], fields[i].Type)
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
		if valueIsZero(row.Values[idx]) {
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
		values[i] = row.Values[idx]
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
	columns := make([]string, 0, len(op.Fields))
	for _, field := range op.Fields {
		if field.PrimaryKey {
			columns = append(columns, field.Column)
		}
	}
	return columns
}

func (op saveRowsOp) updateColumns() []string {
	columns := make([]string, 0, len(op.Fields))
	for _, field := range op.Fields {
		if field.Updatable {
			columns = append(columns, field.Column)
		}
	}
	return columns
}
