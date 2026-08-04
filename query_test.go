package agg

import (
	"context"
	"testing"
)

func TestParseSQLLiteralQuestionMark(t *testing.T) {
	node := parseSQL(`payload ?? 'priority' AND id = ?`, 7)
	fragment, ok := node.(sqlFragment)
	if !ok {
		t.Fatalf("expected sqlFragment, got %T", node)
	}
	if len(fragment.els) != 2 {
		t.Fatalf("expected two SQL elements, got %d", len(fragment.els))
	}
	text, ok := fragment.els[0].(sqlText)
	if !ok || text.text != `payload ? 'priority' AND id = ` {
		t.Fatalf("unexpected literal SQL element: %#v", fragment.els[0])
	}
	param, ok := fragment.els[1].(sqlParam)
	if !ok || param.value != 7 {
		t.Fatalf("unexpected SQL parameter: %#v", fragment.els[1])
	}
}

func TestConverterValue(t *testing.T) {
	converted, err := converterValue[string]([]byte("text"))
	if err != nil || converted != "text" {
		t.Fatalf("expected []byte to string conversion, got %q, %v", converted, err)
	}
	if _, err := converterValue[int]("not-an-int"); err == nil {
		t.Fatal("expected incompatible converter value error")
	}
}

func TestQueryRejectsNegativePagination(t *testing.T) {
	mapper := MustCompile(SQLite, Map(&mappingTestComposite{}))
	query := NewQuery[mappingTestComposite](mapper, nil, "m")

	if _, err := query.FetchMany(context.Background(), -1); err == nil {
		t.Fatal("expected negative limit error")
	}
	if _, err := query.Offset(-1).FetchAll(context.Background()); err == nil {
		t.Fatal("expected negative offset error")
	}
}
