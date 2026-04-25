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

func insertValuesFromRow(insertIndexes []int, row saveRow) []any {
	projected := make([]any, len(insertIndexes))
	for j, idx := range insertIndexes {
		projected[j] = row.values[idx]
	}
	return projected
}

func scanKeyedSavedRows(returningCount int, primaryReturningIndexes []int, primaryTypes []reflect.Type, plannedRows []plannedRow, rowSet rows) ([]savedRow, error) {
	inputByKey := make(map[Key]int, len(plannedRows))
	for _, row := range plannedRows {
		if _, exists := inputByKey[row.key]; exists {
			return nil, fmt.Errorf("duplicate save key %v", row.key)
		}
		inputByKey[row.key] = row.index
	}

	dest := make([]any, returningCount)
	saved := make([]savedRow, 0, len(plannedRows))
	for rowSet.Next() {
		values := make([]any, returningCount)
		if err := scanRowValues(rowSet, values, dest); err != nil {
			return nil, err
		}
		key := coercedKeyFromIndexes(values, primaryReturningIndexes, primaryTypes)
		index, ok := inputByKey[key]
		if !ok {
			return nil, fmt.Errorf("returned key %v does not match any input row", key)
		}
		saved = append(saved, savedRow{index: index, values: values})
	}
	return saved, nil
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
