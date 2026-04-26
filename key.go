package ormapper

import "reflect"

// Key represents an entity's primary or foreign key value(s).
// It is a comparable value that can be used as a map key.
//
// Examples:
//
//	ormapper.NewKey(1)               // single-column key
//	ormapper.NewKey(orderID, itemID) // composite key
type Key struct {
	n  int
	v0 any
	vn any
}

func (k Key) Length() int {
	return k.n
}

func (k Key) At(index int) any {
	if index < 0 || index >= k.n {
		panic("ormapper: key index out of range")
	}

	if index == 0 {
		return k.v0
	}
	return k.tailValue(index)
}

func (k Key) tailValue(index int) any {
	switch tail := k.vn.(type) {
	case keyTail1:
		if index == 1 {
			return tail.v1
		}
	case keyTail2:
		switch index {
		case 1:
			return tail.v1
		case 2:
			return tail.v2
		}
	case keyTail3:
		switch index {
		case 1:
			return tail.v1
		case 2:
			return tail.v2
		case 3:
			return tail.v3
		}
	case keyTail4:
		switch index {
		case 1:
			return tail.v1
		case 2:
			return tail.v2
		case 3:
			return tail.v3
		case 4:
			return tail.v4
		}
	case keyTail5:
		switch index {
		case 1:
			return tail.v1
		case 2:
			return tail.v2
		case 3:
			return tail.v3
		case 4:
			return tail.v4
		case 5:
			return tail.v5
		}
	case keyTail6:
		switch index {
		case 1:
			return tail.v1
		case 2:
			return tail.v2
		case 3:
			return tail.v3
		case 4:
			return tail.v4
		case 5:
			return tail.v5
		case 6:
			return tail.v6
		}
	case keyTail7:
		switch index {
		case 1:
			return tail.v1
		case 2:
			return tail.v2
		case 3:
			return tail.v3
		case 4:
			return tail.v4
		case 5:
			return tail.v5
		case 6:
			return tail.v6
		case 7:
			return tail.v7
		}
	case keyTail8:
		switch index {
		case 1:
			return tail.v1
		case 2:
			return tail.v2
		case 3:
			return tail.v3
		case 4:
			return tail.v4
		case 5:
			return tail.v5
		case 6:
			return tail.v6
		case 7:
			return tail.v7
		case 8:
			return tail.v8
		}
	}
	panic("ormapper: key index out of range")
}

type keyTail1 struct{ v1 any }
type keyTail2 struct{ v1, v2 any }
type keyTail3 struct{ v1, v2, v3 any }
type keyTail4 struct{ v1, v2, v3, v4 any }
type keyTail5 struct{ v1, v2, v3, v4, v5 any }
type keyTail6 struct{ v1, v2, v3, v4, v5, v6 any }
type keyTail7 struct{ v1, v2, v3, v4, v5, v6, v7 any }
type keyTail8 struct{ v1, v2, v3, v4, v5, v6, v7, v8 any }

// NewKey creates a new Key from the given values.
// Supports up to 9 column values.
func NewKey(vals ...any) Key {
	return newKeyFromValues(vals)
}

func newKeyFromValues(vals []any) Key {
	switch len(vals) {
	case 0:
		return new0()
	case 1:
		return new1(vals[0])
	case 2:
		return new2(vals[0], vals[1])
	case 3:
		return new3(vals[0], vals[1], vals[2])
	case 4:
		return new4(vals[0], vals[1], vals[2], vals[3])
	case 5:
		return new5(vals[0], vals[1], vals[2], vals[3], vals[4])
	case 6:
		return new6(vals[0], vals[1], vals[2], vals[3], vals[4], vals[5])
	case 7:
		return new7(vals[0], vals[1], vals[2], vals[3], vals[4], vals[5], vals[6])
	case 8:
		return new8(vals[0], vals[1], vals[2], vals[3], vals[4], vals[5], vals[6], vals[7])
	case 9:
		return new9(vals[0], vals[1], vals[2], vals[3], vals[4], vals[5], vals[6], vals[7], vals[8])
	default:
		panic("ormapper: Key supports up to 9 column values")
	}
}

func new0() Key {
	return Key{}
}

func new1(v0 any) Key {
	return Key{n: 1, v0: v0}
}

func new2(v0, v1 any) Key {
	return Key{n: 2, v0: v0, vn: keyTail1{v1: v1}}
}

func new3(v0, v1, v2 any) Key {
	return Key{n: 3, v0: v0, vn: keyTail2{v1: v1, v2: v2}}
}

func new4(v0, v1, v2, v3 any) Key {
	return Key{n: 4, v0: v0, vn: keyTail3{v1: v1, v2: v2, v3: v3}}
}

func new5(v0, v1, v2, v3, v4 any) Key {
	return Key{n: 5, v0: v0, vn: keyTail4{v1: v1, v2: v2, v3: v3, v4: v4}}
}

func new6(v0, v1, v2, v3, v4, v5 any) Key {
	return Key{n: 6, v0: v0, vn: keyTail5{v1: v1, v2: v2, v3: v3, v4: v4, v5: v5}}
}

func new7(v0, v1, v2, v3, v4, v5, v6 any) Key {
	return Key{n: 7, v0: v0, vn: keyTail6{v1: v1, v2: v2, v3: v3, v4: v4, v5: v5, v6: v6}}
}

func new8(v0, v1, v2, v3, v4, v5, v6, v7 any) Key {
	return Key{n: 8, v0: v0, vn: keyTail7{v1: v1, v2: v2, v3: v3, v4: v4, v5: v5, v6: v6, v7: v7}}
}

func new9(v0, v1, v2, v3, v4, v5, v6, v7, v8 any) Key {
	return Key{n: 9, v0: v0, vn: keyTail8{v1: v1, v2: v2, v3: v3, v4: v4, v5: v5, v6: v6, v7: v7, v8: v8}}
}

type keySet map[Key]struct{}

func newKeySet(keys []Key) keySet {
	s := make(keySet, len(keys))
	for _, key := range keys {
		s[key] = struct{}{}
	}
	return s
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

func uniqueKeys(keys []Key) []Key {
	if len(keys) < 2 {
		return keys
	}
	result := make([]Key, 0, len(keys))
	seen := make(keySet, len(keys))
	for _, key := range keys {
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, key)
	}
	return result
}
