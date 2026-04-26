package ormapper

func insertValuesFromRow(insertIndexes []int, values []any) []any {
	projected := make([]any, len(insertIndexes))
	for j, idx := range insertIndexes {
		projected[j] = values[idx]
	}
	return projected
}
