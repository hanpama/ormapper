package ormapper

func insertValuesFromRow(insertIndexes []int, row saveRow) []any {
	projected := make([]any, len(insertIndexes))
	for j, idx := range insertIndexes {
		projected[j] = row.values[idx]
	}
	return projected
}
