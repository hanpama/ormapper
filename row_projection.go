package ormapper

func insertValuesFromRow(layout *saveRowsLayout, row saveRow) []any {
	projected := make([]any, len(layout.insertIndexes))
	for j, idx := range layout.insertIndexes {
		projected[j] = row.values[idx]
	}
	return projected
}

func primaryKeyFromRow(layout *saveRowsLayout, row saveRow) Key {
	return keyFromIndexes(row.values, layout.primaryIndexes)
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
