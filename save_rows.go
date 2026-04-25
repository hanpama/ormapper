package ormapper

import (
	"fmt"
	"reflect"
)

func splitPlannedRows(rows []plannedRow) ([]int, []saveRow) {
	indexes := make([]int, len(rows))
	saveRows := make([]saveRow, len(rows))
	for i, row := range rows {
		indexes[i] = row.index
		saveRows[i] = row.row
	}
	return indexes, saveRows
}

func insertValuesFromRow(layout *saveRowsLayout, row saveRow) []any {
	projected := make([]any, len(layout.insertIndexes))
	for j, idx := range layout.insertIndexes {
		projected[j] = row.values[idx]
	}
	return projected
}

func scanKeyedSavedRows(layout *saveRowsLayout, plannedRows []plannedRow, rowSet rows) ([]savedRow, error) {
	inputByKey := make(map[Key]int, len(plannedRows))
	for _, row := range plannedRows {
		if _, exists := inputByKey[row.key]; exists {
			return nil, fmt.Errorf("duplicate save key %v", row.key)
		}
		inputByKey[row.key] = row.index
	}

	returningColumnCount := len(layout.returningColumns)
	dest := make([]any, returningColumnCount)
	saved := make([]savedRow, 0, len(plannedRows))
	for rowSet.Next() {
		values := make([]any, returningColumnCount)
		if err := scanRowValues(rowSet, values, dest); err != nil {
			return nil, err
		}
		key := primaryKeyFromReturnedValues(layout, values)
		index, ok := inputByKey[key]
		if !ok {
			return nil, fmt.Errorf("returned key %v does not match any input row", key)
		}
		saved = append(saved, savedRow{index: index, values: values})
	}
	return saved, nil
}

func primaryKeyFromReturnedValues(layout *saveRowsLayout, values []any) Key {
	return coercedKeyFromIndexes(values, layout.primaryReturningIndexes, layout.primaryTypes)
}

func coercedKeyFromIndexes(values []any, indexes []int, types []reflect.Type) Key {
	var keyValues [9]any
	if len(indexes) > len(keyValues) {
		panic("ormapper: Key supports up to 9 column values")
	}
	for i, idx := range indexes {
		keyValues[i] = coerceScannedValue(values[idx], types[i])
	}
	return newKeyFromValues(keyValues[:len(indexes)])
}
