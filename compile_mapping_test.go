package agg

import (
	"reflect"
	"testing"
)

type mappingTestChild struct {
	ID       int64 `agg:"auto"`
	ParentID int64 `agg:"parental"`
	Name     string
}

type mappingTestNote struct {
	ID       int64 `agg:"auto"`
	ParentID int64 `agg:"parental"`
	Body     string
}

type mappingTestParent struct {
	ID        int64  `agg:"auto"`
	Name      string `agg:"column:display_name"`
	Ignored   string `agg:"-"`
	Readonly  int    `agg:"skip_insert,skip_update"`
	WriteOnce int    `agg:"skip_update"`
	Children  []*mappingTestChild
	Note      *mappingTestNote
	Loose     *unregisteredMappingStruct
	Numbers   []int
}

type unregisteredMappingStruct struct {
	ID int64
}

type mappingTestComposite struct {
	Key1 int    `agg:"primary"`
	Key2 string `agg:"primary"`
	Name string
}

type invalidNoPrimary struct {
	Name string
}

type invalidExplicitChild struct {
	ID  int64
	Bad int `agg:"child"`
}

type invalidUnregisteredChildParent struct {
	ID    int64
	Child *unregisteredMappingStruct `agg:"child"`
}

type invalidChildWithoutParentalParent struct {
	ID    int64
	Child *invalidChildWithoutParental
}

type invalidChildWithoutParental struct {
	ID   int64
	Name string
}

type invalidKeyTypeParent struct {
	ID       int64
	Children []*invalidKeyTypeChild
}

type invalidKeyTypeChild struct {
	ID       int64
	ParentID int `agg:"parental"`
}

type invalidGeneratedComposite struct {
	ID       int64 `agg:"auto"`
	TenantID int64 `agg:"primary"`
	Name     string
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
	if got := fieldNames(parent.insertablePlan); !reflect.DeepEqual(got, []string{"Name", "WriteOnce"}) {
		t.Fatalf("unexpected insertable fields: %#v", got)
	}
	if got := fieldNames(parent.updatablePlan); !reflect.DeepEqual(got, []string{"Name"}) {
		t.Fatalf("unexpected updatable fields: %#v", got)
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
	key := composite.extractPrimaryKey(&mappingTestComposite{Key1: 7, Key2: "x"})
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
	if _, err := Compile(SQLite, Map(&mappingTestParent{}), Map(&mappingTestParent{})); err == nil {
		t.Fatal("expected duplicate mapping error")
	}
	if _, err := Compile(SQLite, Map(&invalidNoPrimary{})); err == nil {
		t.Fatal("expected no-primary-key mapping error")
	}
	if _, err := Compile(SQLite, Map(&invalidExplicitChild{})); err == nil {
		t.Fatal("expected invalid explicit child mapping error")
	}
	if _, err := Compile(SQLite, Map(&invalidUnregisteredChildParent{})); err == nil {
		t.Fatal("expected unregistered explicit child target error")
	}
	if _, err := Compile(SQLite, Map(&invalidChildWithoutParentalParent{}), Map(&invalidChildWithoutParental{})); err == nil {
		t.Fatal("expected child without parental key error")
	}
	if _, err := Compile(SQLite, Map(&invalidKeyTypeParent{}), Map(&invalidKeyTypeChild{})); err == nil {
		t.Fatal("expected parent/child key type mismatch error")
	}
	if _, err := Compile(SQLite, Map(&invalidGeneratedComposite{})); err == nil {
		t.Fatal("expected generated composite primary key error")
	}
}

func fieldNames(plan fieldPlan) []string {
	names := make([]string, len(plan.fields))
	for i, f := range plan.fields {
		names[i] = f.name
	}
	return names
}
