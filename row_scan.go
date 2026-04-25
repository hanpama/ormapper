package ormapper

import (
	"fmt"
	"reflect"
	"sort"
)

type emptyRows struct{}

func (e *emptyRows) Next() bool             { return false }
func (e *emptyRows) Scan(dest ...any) error { return nil }
func (e *emptyRows) Close() error           { return nil }

func splitPlannedRows(rows []plannedRow) ([]int, []saveRow) {
	indexes := make([]int, len(rows))
	saveRows := make([]saveRow, len(rows))
	for i, row := range rows {
		indexes[i] = row.index
		saveRows[i] = row.row
	}
	return indexes, saveRows
}

func scanKeyedSavedRows(op saveRowsOp, indexes []int, saveRows []saveRow, rowSet rows) ([]savedRow, error) {
	inputByKey := make(map[Key]int, len(saveRows))
	for i, row := range saveRows {
		key := primaryKeyFromRow(op.layout, row)
		if _, exists := inputByKey[key]; exists {
			return nil, fmt.Errorf("duplicate save key %v", key)
		}
		inputByKey[key] = indexes[i]
	}

	returningColumnCount := len(op.layout.returningColumns)
	dest := make([]any, returningColumnCount)
	saved := make([]savedRow, 0, len(saveRows))
	for rowSet.Next() {
		values := make([]any, returningColumnCount)
		if err := scanRowValues(rowSet, values, dest); err != nil {
			return nil, err
		}
		key := primaryKeyFromReturnedValues(op.layout, values)
		index, ok := inputByKey[key]
		if !ok {
			return nil, fmt.Errorf("returned key %v does not match any input row", key)
		}
		saved = append(saved, savedRow{index: index, values: values})
	}
	return saved, nil
}

func scanIndexedSavedRows(rowSet rows, returningColumns int) ([]savedRow, error) {
	dest := make([]any, returningColumns+1)
	saved := make([]savedRow, 0)
	for rowSet.Next() {
		var indexValue any
		values := make([]any, returningColumns)
		dest[0] = &indexValue
		for i := range values {
			dest[i+1] = &values[i]
		}
		if err := rowSet.Scan(dest...); err != nil {
			return nil, err
		}
		index, err := intFromDB(normalizeScannedValue(indexValue))
		if err != nil {
			return nil, err
		}
		for i, value := range values {
			values[i] = normalizeScannedValue(value)
		}
		saved = append(saved, savedRow{index: index, values: values})
	}
	return saved, nil
}

func scanRowsBySingleIntKeyOrder(op saveRowsOp, indexes []int, rowSet rows) ([]savedRow, error) {
	if len(op.layout.primaryColumns) != 1 {
		return nil, fmt.Errorf("ordered generated insert correlation requires one primary key")
	}

	type keyedRow struct {
		key    int64
		values []any
	}
	returningColumnCount := len(op.layout.returningColumns)
	dest := make([]any, returningColumnCount)
	keyed := make([]keyedRow, 0, len(indexes))
	for rowSet.Next() {
		values := make([]any, returningColumnCount)
		if err := scanRowValues(rowSet, values, dest); err != nil {
			return nil, err
		}
		key := primaryKeyFromReturnedValues(op.layout, values)
		value, err := int64FromDB(key.At(0))
		if err != nil {
			return nil, err
		}
		keyed = append(keyed, keyedRow{key: value, values: values})
	}
	sort.Slice(keyed, func(i, j int) bool {
		return keyed[i].key < keyed[j].key
	})

	sortedIndexes := append([]int(nil), indexes...)
	sort.Ints(sortedIndexes)
	if len(sortedIndexes) != len(keyed) {
		return nil, fmt.Errorf("expected %d returned rows, got %d", len(sortedIndexes), len(keyed))
	}

	saved := make([]savedRow, len(keyed))
	for i, row := range keyed {
		saved[i] = savedRow{index: sortedIndexes[i], values: row.values}
	}
	return saved, nil
}

func intFromDB(value any) (int, error) {
	v, err := int64FromDB(value)
	if err != nil {
		return 0, err
	}
	return int(v), nil
}

func int64FromDB(value any) (int64, error) {
	const maxInt64 = int64(^uint64(0) >> 1)
	switch v := value.(type) {
	case int:
		return int64(v), nil
	case int8:
		return int64(v), nil
	case int16:
		return int64(v), nil
	case int32:
		return int64(v), nil
	case int64:
		return v, nil
	case uint:
		if uint64(v) > uint64(maxInt64) {
			return 0, fmt.Errorf("integer value %d overflows int64", v)
		}
		return int64(v), nil
	case uint8:
		return int64(v), nil
	case uint16:
		return int64(v), nil
	case uint32:
		return int64(v), nil
	case uint64:
		if v > uint64(maxInt64) {
			return 0, fmt.Errorf("integer value %d overflows int64", v)
		}
		return int64(v), nil
	default:
		return 0, fmt.Errorf("expected integer value, got %T", value)
	}
}

func scanTypedKeys(rowSet rows, keyTypes []reflect.Type) ([]Key, error) {
	if len(keyTypes) == 0 {
		return nil, nil
	}

	keys := make([]Key, 0)
	values := make([]any, len(keyTypes))
	dest := make([]any, len(keyTypes))
	for i := range values {
		dest[i] = &values[i]
	}
	for rowSet.Next() {
		if err := rowSet.Scan(dest...); err != nil {
			return nil, err
		}
		for i, value := range values {
			values[i] = coerceScannedValue(normalizeScannedValue(value), keyTypes[i])
		}
		keys = append(keys, NewKey(values...))
	}

	return keys, nil
}

func scanRowValues(rowSet rows, values []any, dest []any) error {
	for i := range values {
		dest[i] = &values[i]
	}
	if err := rowSet.Scan(dest...); err != nil {
		return err
	}
	for i, value := range values {
		values[i] = normalizeScannedValue(value)
	}
	return nil
}

func normalizeScannedValue(value any) any {
	switch v := value.(type) {
	case []byte:
		return string(v)
	default:
		return value
	}
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

func coerceScannedValue(value any, target reflect.Type) any {
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
