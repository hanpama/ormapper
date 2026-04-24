package contracts

import (
	"context"
	"database/sql"
	"strings"
	"testing"
)

type traceDB struct {
	db        *sql.DB
	recording bool
	events    []string
}

func newTraceDB(db *sql.DB) *traceDB {
	return &traceDB{db: db}
}

func (t *traceDB) capture(fn func() error) ([]string, error) {
	t.events = nil
	t.recording = true
	err := fn()
	t.recording = false
	return append([]string(nil), t.events...), err
}

func (t *traceDB) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	t.record(query)
	return t.db.ExecContext(ctx, query, args...)
}

func (t *traceDB) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	t.record(query)
	return t.db.QueryContext(ctx, query, args...)
}

func (t *traceDB) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	t.record(query)
	return t.db.QueryRowContext(ctx, query, args...)
}

func (t *traceDB) record(query string) {
	if !t.recording {
		return
	}
	if event := classifySQLFlow(query); event != "" {
		t.events = append(t.events, event)
	}
}

func classifySQLFlow(query string) string {
	q := strings.ToLower(strings.Join(strings.Fields(query), " "))
	table := detectFlowTable(q)
	if table == "" {
		return ""
	}

	if strings.Contains(q, "insert into") {
		return "INSERT " + table
	}
	if strings.Contains(q, "update "+`"`+table+`"`) || strings.Contains(q, "update "+table) {
		return "UPDATE " + table
	}
	if strings.Contains(q, "delete from") {
		return "DELETE " + table
	}
	if strings.Contains(q, ` from "$k" join`) || strings.Contains(q, " from keys join") {
		return "EXISTS " + table
	}
	if strings.Contains(q, ` join "$p"`) || strings.Contains(q, " join parent_rows") {
		return "MISSING " + table
	}
	if strings.Contains(q, ` join "$k"`) || strings.Contains(q, " join keys") {
		return "LOAD " + table
	}

	return ""
}

func detectFlowTable(query string) string {
	for _, table := range []string{
		"order_item_lots",
		"order_items",
		"order_notes",
		"orders",
		"composite",
	} {
		if containsTableRef(query, table) {
			return table
		}
	}
	return ""
}

func containsTableRef(query, table string) bool {
	quoted := `"` + table + `"`
	return strings.Contains(query, "insert into "+quoted) ||
		strings.Contains(query, "insert into "+table) ||
		strings.Contains(query, "update "+quoted) ||
		strings.Contains(query, "update "+table) ||
		strings.Contains(query, "delete from "+quoted) ||
		strings.Contains(query, "delete from "+table) ||
		strings.Contains(query, " join "+quoted) ||
		strings.Contains(query, " join "+table) ||
		strings.Contains(query, " from "+quoted) ||
		strings.Contains(query, " from "+table)
}

func assertSQLFlow(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("unexpected SQL flow length\n got: %s\nwant: %s", formatSQLFlow(got), formatSQLFlow(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("unexpected SQL flow at index %d\n got: %s\nwant: %s", i, formatSQLFlow(got), formatSQLFlow(want))
		}
	}
}

func formatSQLFlow(flow []string) string {
	return strings.Join(flow, " -> ")
}
