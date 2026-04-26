package ormapper

import (
	"fmt"
	"reflect"
)

type emptyRows struct{}

func (e *emptyRows) Next() bool            { return false }
func (e *emptyRows) Scan(dest ...any) error { return nil }
func (e *emptyRows) Close() error           { return nil }

type bufferedRows struct {
	data   [][]any
	cursor int
}

func (r *bufferedRows) Next() bool {
	r.cursor++
	return r.cursor <= len(r.data)
}

func (r *bufferedRows) Scan(dest ...any) error {
	row := r.data[r.cursor-1]
	for i, d := range dest {
		dv := reflect.ValueOf(d).Elem()
		val := row[i]
		if val == nil {
			dv.Set(reflect.Zero(dv.Type()))
			continue
		}
		sv := reflect.ValueOf(val)
		if sv.Type().AssignableTo(dv.Type()) {
			dv.Set(sv)
		} else if sv.Type().ConvertibleTo(dv.Type()) {
			dv.Set(sv.Convert(dv.Type()))
		} else {
			dv.Set(sv)
		}
	}
	return nil
}

func (r *bufferedRows) Close() error { return nil }

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

func scanKeyPairs(rowSet rows, leftTypes, rightTypes []reflect.Type) ([]Key, []Key, error) {
	leftLen := len(leftTypes)
	values := make([]any, leftLen+len(rightTypes))
	dest := make([]any, len(values))
	for i := range values {
		dest[i] = &values[i]
	}

	var leftKeys, rightKeys []Key
	for rowSet.Next() {
		if err := rowSet.Scan(dest...); err != nil {
			return nil, nil, err
		}
		for i, value := range values[:leftLen] {
			values[i] = coerceScannedValue(normalizeScannedValue(value), leftTypes[i])
		}
		for i, value := range values[leftLen:] {
			values[leftLen+i] = coerceScannedValue(normalizeScannedValue(value), rightTypes[i])
		}
		leftKeys = append(leftKeys, NewKey(values[:leftLen]...))
		rightKeys = append(rightKeys, NewKey(values[leftLen:]...))
	}

	return leftKeys, rightKeys, nil
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
