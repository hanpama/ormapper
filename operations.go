package ormapper

type selectOp struct {
	Select     []string // Columns to select
	FromSchema string   // Database schema (optional)
	FromTable  string   // Table name
	KeyColumns []string // Columns used for WHERE clause (typically primary key)
	Keys       []Key    // Batch key values
}

type insertOp struct {
	IntoSchema string   // Database schema (optional)
	IntoTable  string   // Table name
	Insert     []string // Columns to insert
	Returning  []string // Columns to return (e.g., auto-generated columns)
	Values     [][]any  // Batch insert values, Values[i][j] maps to Insert[j]
}

type updateOp struct {
	Schema      string   // Database schema (optional)
	Table       string   // Table name
	Sets        []string // Columns to update (SET clause)
	Where       []string // Columns for WHERE clause (typically primary key)
	SetValues   [][]any  // Values for SET clause: SetValues[i][j] maps to Sets[j]
	WhereValues [][]any  // Values for WHERE clause: WhereValues[i][j] maps to Where[j]
}

type deleteOp struct {
	FromSchema string   // Database schema (optional)
	FromTable  string   // Table name
	KeyColumns []string // Columns used for WHERE clause (typically primary key)
	Keys       []Key    // Batch key values
}
