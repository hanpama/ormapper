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
	layout *saveRowsLayout
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
	rowFields       []*field
	returningFields []*field
	rows            saveRowsLayout
}

type saveRowsLayout struct {
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

func (op saveRowsOp) insertValuesForRow(row saveRow) []any {
	projected := make([]any, len(op.layout.insertIndexes))
	for j, idx := range op.layout.insertIndexes {
		projected[j] = row.values[idx]
	}
	return projected
}

func (op saveRowsOp) keyFromReturnedValues(values []any) Key {
	return coercedKeyFromIndexes(values, op.layout.primaryReturningIndexes, op.layout.primaryTypes)
}

func (op saveRowsOp) classifyRow(row saveRow) (saveRowIntent, error) {
	generatedIndexes := op.layout.generatedPrimaryIndexes
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
	return keyFromIndexes(row.values, op.layout.primaryIndexes)
}

func keyFromIndexes(values []any, indexes []int) Key {
	var keyValues [9]any
	if len(indexes) > len(keyValues) {
		panic("ormapper: Key supports up to 9 column values")
	}
	for i, idx := range indexes {
		keyValues[i] = values[idx]
	}
	return newKeyFromValues(keyValues[:len(indexes)])
}

func coercedKeyFromIndexes(values []any, indexes []int, types []reflect.Type) Key {
	var keyValues [9]any
	if len(indexes) > len(keyValues) {
		panic("ormapper: Key supports up to 9 column values")
	}
	for i, idx := range indexes {
		keyValues[i] = coerceValue(values[idx], types[i])
	}
	return newKeyFromValues(keyValues[:len(indexes)])
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
