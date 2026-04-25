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
	schema string
	table  string
	layout *saveLayout
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

type saveLayout struct {
	rowFields                         []string
	rowColumns                        []string
	insertColumns                     []string
	insertIndexes                     []int
	insertColumnsWithGeneratedPrimary []string
	updateColumns                     []string
	primaryColumns                    []string
	primaryIndexes                    []int
	primaryTypes                      []reflect.Type
	generatedPrimaryColumns           []string
	generatedPrimaryIndexes           []int
	returningFields                   []string
	returningColumns                  []string
	primaryReturningIndexes           []int
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
	return op.layout.insertColumns
}

func (op saveRowsOp) insertColumnsWithGeneratedPrimary() []string {
	return op.layout.insertColumnsWithGeneratedPrimary
}

func (op saveRowsOp) rowColumns() []string {
	return op.layout.rowColumns
}

func (op saveRowsOp) insertValuesForRow(row saveRow) []any {
	projected := make([]any, len(op.layout.insertIndexes))
	for j, idx := range op.layout.insertIndexes {
		projected[j] = row.values[idx]
	}
	return projected
}

func (op saveRowsOp) primaryKeyIndexes() []int {
	return op.layout.primaryIndexes
}

func (op saveRowsOp) generatedPrimaryIndexes() []int {
	return op.layout.generatedPrimaryIndexes
}

func (op saveRowsOp) primaryKeyCount() int {
	return len(op.layout.primaryColumns)
}

func (op saveRowsOp) generatedPrimaryColumns() []string {
	return op.layout.generatedPrimaryColumns
}

func (op saveRowsOp) returningColumns() []string {
	return op.layout.returningColumns
}

func (op saveRowsOp) keyFromReturnedValues(values []any) Key {
	indexes := op.layout.primaryReturningIndexes
	keyValues := make([]any, len(indexes))
	for i, idx := range indexes {
		keyValues[i] = coerceValue(values[idx], op.layout.primaryTypes[i])
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
	return op.layout.primaryColumns
}

func (op saveRowsOp) updateColumns() []string {
	return op.layout.updateColumns
}
