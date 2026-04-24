package ormapper

import (
	"fmt"
	"reflect"
	"sort"
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

func keysFromPlannedRows(op saveRowsOp, rows []plannedRow) []Key {
	keys := make([]Key, len(rows))
	for i, row := range rows {
		keys[i] = op.keyFromRow(row.row)
	}
	return keys
}

func keySet(keys []Key) map[Key]struct{} {
	result := make(map[Key]struct{}, len(keys))
	for _, key := range keys {
		result[key] = struct{}{}
	}
	return result
}

func scanKeyedSavedRows(op saveRowsOp, indexes []int, saveRows []saveRow, rowSet rows) ([]savedRow, error) {
	inputByKey := make(map[Key]int, len(saveRows))
	for i, row := range saveRows {
		key := op.keyFromRow(row)
		if _, exists := inputByKey[key]; exists {
			return nil, fmt.Errorf("duplicate save key %v", key)
		}
		inputByKey[key] = indexes[i]
	}

	valueRows, err := scanValueRows(rowSet, len(op.returning))
	if err != nil {
		return nil, err
	}

	saved := make([]savedRow, 0, len(valueRows))
	for _, values := range valueRows {
		key := op.keyFromReturnedValues(values)
		index, ok := inputByKey[key]
		if !ok {
			return nil, fmt.Errorf("returned key %v does not match any input row", key)
		}
		saved = append(saved, savedRow{index: index, values: values})
	}
	return saved, nil
}

func scanIndexedSavedRows(rowSet rows, returningColumns int) ([]savedRow, error) {
	valueRows, err := scanValueRows(rowSet, returningColumns+1)
	if err != nil {
		return nil, err
	}

	saved := make([]savedRow, 0, len(valueRows))
	for _, row := range valueRows {
		index, err := intFromDB(row[0])
		if err != nil {
			return nil, err
		}
		values := make([]any, returningColumns)
		copy(values, row[1:])
		saved = append(saved, savedRow{index: index, values: values})
	}
	return saved, nil
}

func savedRowsBySingleIntKeyOrder(op saveRowsOp, indexes []int, valueRows [][]any) ([]savedRow, error) {
	if len(op.primaryFields()) != 1 {
		return nil, fmt.Errorf("ordered generated insert correlation requires one primary key")
	}

	type keyedRow struct {
		key    int64
		values []any
	}
	keyed := make([]keyedRow, len(valueRows))
	for i, values := range valueRows {
		key := op.keyFromReturnedValues(values)
		value, err := int64FromDB(key.At(0))
		if err != nil {
			return nil, err
		}
		keyed[i] = keyedRow{key: value, values: values}
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

func keepRowsFromPairs(pairs []keepPair) [][]any {
	keepRows := make([][]any, len(pairs))
	for i, pair := range pairs {
		row := make([]any, 0, pair.parentKey.Length()+pair.childKey.Length())
		for j := 0; j < pair.parentKey.Length(); j++ {
			row = append(row, pair.parentKey.At(j))
		}
		for j := 0; j < pair.childKey.Length(); j++ {
			row = append(row, pair.childKey.At(j))
		}
		keepRows[i] = row
	}
	return keepRows
}

func rowsFromKeys(keys []Key) [][]any {
	if len(keys) == 0 {
		return nil
	}

	values := make([][]any, len(keys))
	for i, key := range keys {
		row := make([]any, key.Length())
		for j := 0; j < key.Length(); j++ {
			row[j] = key.At(j)
		}
		values[i] = row
	}

	return values
}

func scanKeys(rowSet rows, keyColumns int) ([]Key, error) {
	if keyColumns == 0 {
		return nil, nil
	}

	keys := make([]Key, 0)
	for rowSet.Next() {
		values := make([]any, keyColumns)
		dest := make([]any, keyColumns)
		for i := range values {
			dest[i] = &values[i]
		}
		if err := rowSet.Scan(dest...); err != nil {
			return nil, err
		}
		for i, value := range values {
			values[i] = normalizeScannedValue(value)
		}
		keys = append(keys, NewKey(values...))
	}

	return keys, nil
}

func scanTypedKeys(rowSet rows, keyTypes []reflect.Type) ([]Key, error) {
	if len(keyTypes) == 0 {
		return nil, nil
	}

	keys := make([]Key, 0)
	for rowSet.Next() {
		values := make([]any, len(keyTypes))
		dest := make([]any, len(keyTypes))
		for i := range values {
			dest[i] = &values[i]
		}
		if err := rowSet.Scan(dest...); err != nil {
			return nil, err
		}
		for i, value := range values {
			values[i] = coerceValue(normalizeScannedValue(value), keyTypes[i])
		}
		keys = append(keys, NewKey(values...))
	}

	return keys, nil
}

func scanValueRows(rowSet rows, columnCount int) ([][]any, error) {
	valueRows := make([][]any, 0)
	for rowSet.Next() {
		values := make([]any, columnCount)
		dest := make([]any, columnCount)
		for i := range values {
			dest[i] = &values[i]
		}
		if err := rowSet.Scan(dest...); err != nil {
			return nil, err
		}
		for i, value := range values {
			values[i] = normalizeScannedValue(value)
		}
		valueRows = append(valueRows, values)
	}
	return valueRows, nil
}

func normalizeScannedValue(value any) any {
	switch v := value.(type) {
	case []byte:
		return string(v)
	default:
		return value
	}
}
