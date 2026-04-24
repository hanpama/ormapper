package ormapper

import (
	"reflect"
	"testing"
)

type mappingTestChild struct {
	ID       int64 `ormapper:"auto"`
	ParentID int64 `ormapper:"parental"`
	Name     string
}

type mappingTestNote struct {
	ID       int64 `ormapper:"auto"`
	ParentID int64 `ormapper:"parental"`
	Body     string
}

type mappingTestParent struct {
	ID        int64  `ormapper:"auto"`
	Name      string `ormapper:"column:display_name"`
	Ignored   string `ormapper:"-"`
	Readonly  int    `ormapper:"skip_insert,skip_update"`
	WriteOnce int    `ormapper:"skip_update"`
	Children  []*mappingTestChild
	Note      *mappingTestNote
	Loose     *unregisteredMappingStruct
	Numbers   []int
}

type unregisteredMappingStruct struct {
	ID int64
}

type mappingTestComposite struct {
	Key1 int    `ormapper:"primary"`
	Key2 string `ormapper:"primary"`
	Name string
}

func TestToSnakeCase(t *testing.T) {
	cases := map[string]string{
		"ID":           "id",
		"CustomerID":   "customer_id",
		"HTTPRequest":  "http_request",
		"OrderItemLot": "order_item_lot",
	}

	for input, want := range cases {
		if got := toSnakeCase(input); got != want {
			t.Fatalf("toSnakeCase(%q): expected %q, got %q", input, want, got)
		}
	}
}

func TestCompileBuildsMappings(t *testing.T) {
	mapper := MustCompile(
		SQLite,
		Map(&mappingTestParent{}, WithSchema("app"), WithTable("parents")),
		Map(&mappingTestChild{}, WithTable("children")),
		Map(&mappingTestNote{}, WithTable("notes")),
		Map(&mappingTestComposite{}, WithTable("composite")),
	)

	parent := mapper.mappings[reflect.TypeOf(mappingTestParent{})]
	if parent == nil {
		t.Fatal("expected parent mapping")
	}
	if parent.schema != "app" || parent.table != "parents" {
		t.Fatalf("unexpected table mapping: schema=%q table=%q", parent.schema, parent.table)
	}
	if got := parent.fieldMap["Name"].column; got != "display_name" {
		t.Fatalf("expected custom column display_name, got %q", got)
	}
	if _, ok := parent.fieldMap["Ignored"]; ok {
		t.Fatal("ignored field should not be mapped")
	}
	if _, ok := parent.fieldMap["Loose"]; ok {
		t.Fatal("unregistered struct pointer should not be mapped as scalar field")
	}
	if _, ok := parent.fieldMap["Numbers"]; ok {
		t.Fatal("slice of non-entity should not be mapped")
	}
	if !reflect.DeepEqual(parent.primaryKey, []string{"ID"}) {
		t.Fatalf("expected default ID primary key, got %#v", parent.primaryKey)
	}
	if !reflect.DeepEqual(parent.insertable, []string{"Name", "WriteOnce"}) {
		t.Fatalf("unexpected insertable fields: %#v", parent.insertable)
	}
	if !reflect.DeepEqual(parent.updatable, []string{"Name"}) {
		t.Fatalf("unexpected updatable fields: %#v", parent.updatable)
	}

	if child := parent.childMap["Children"]; child == nil || child.singular || child.target != reflect.TypeOf(mappingTestChild{}) {
		t.Fatalf("expected plural child mapping, got %#v", child)
	}
	if child := parent.childMap["Note"]; child == nil || !child.singular || child.target != reflect.TypeOf(mappingTestNote{}) {
		t.Fatalf("expected singular child mapping, got %#v", child)
	}

	child := mapper.mappings[reflect.TypeOf(mappingTestChild{})]
	if !reflect.DeepEqual(child.parentalKey, []string{"ParentID"}) {
		t.Fatalf("expected parental key ParentID, got %#v", child.parentalKey)
	}

	composite := mapper.mappings[reflect.TypeOf(mappingTestComposite{})]
	if !reflect.DeepEqual(composite.primaryKey, []string{"Key1", "Key2"}) {
		t.Fatalf("expected composite primary key, got %#v", composite.primaryKey)
	}
	key := composite.extractKey(&mappingTestComposite{Key1: 7, Key2: "x"}, composite.primaryKey)
	if key != NewKey(7, "x") {
		t.Fatalf("unexpected composite key: %#v", key)
	}
}

func TestCompileInvalidMappings(t *testing.T) {
	if _, err := Compile(nil, Map(&mappingTestParent{})); err == nil {
		t.Fatal("expected nil dialect error")
	}
	if _, err := Compile(SQLite, Map(nil)); err == nil {
		t.Fatal("expected nil entity pointer error")
	}
	if _, err := Compile(SQLite, Map(mappingTestParent{})); err == nil {
		t.Fatal("expected non-pointer entity error")
	}
	if _, err := Compile(SQLite, Map(new(int))); err == nil {
		t.Fatal("expected pointer-to-non-struct error")
	}
}
