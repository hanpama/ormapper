package ormapper

import (
	"reflect"
	"slices"
)

type field struct {
	name       string
	column     string
	typ        reflect.Type
	fieldIndex int // Index of the field in the struct for direct reflect access
}

func (f *field) getPtr(entityPtr any) any {
	sv := reflect.ValueOf(entityPtr).Elem()
	return sv.Field(f.fieldIndex).Addr().Interface()
}

func (f *field) getValue(entityPtr any) any {
	sv := reflect.ValueOf(entityPtr).Elem()
	return sv.Field(f.fieldIndex).Interface()
}

func (f *field) setValue(entityPtr any, value any) {
	sv := reflect.ValueOf(entityPtr).Elem()
	field := sv.Field(f.fieldIndex)
	if value == nil {
		field.Set(reflect.Zero(field.Type()))
		return
	}
	v := reflect.ValueOf(value)
	if v.Type().AssignableTo(field.Type()) {
		field.Set(v)
		return
	}
	if v.Type().ConvertibleTo(field.Type()) {
		field.Set(v.Convert(field.Type()))
		return
	}
	field.Set(v)
}

type child struct {
	target     reflect.Type
	singular   bool
	typ        reflect.Type
	fieldIndex int // Index of the field in the struct for direct reflect access
}

func (c *child) get(entityPtr any) []any {
	if c.singular {
		sv := reflect.ValueOf(entityPtr).Elem()
		fieldPtr := sv.Field(c.fieldIndex)
		if fieldPtr.IsNil() {
			return []any{}
		}
		value := fieldPtr.Interface()
		if value == nil {
			return []any{}
		}
		return []any{value}
	} else {
		sv := reflect.ValueOf(entityPtr).Elem()
		fieldPtr := sv.Field(c.fieldIndex)
		sliceLen := fieldPtr.Len()
		result := make([]any, sliceLen)
		for j := 0; j < sliceLen; j++ {
			result[j] = fieldPtr.Index(j).Interface()
		}
		return result
	}
}

func (c *child) set(entityPtr any, children []any) {
	if c.singular {
		var value any
		if len(children) > 0 {
			value = children[0]
		} else {
			value = nil
		}
		sv := reflect.ValueOf(entityPtr).Elem()
		fieldPtr := sv.Field(c.fieldIndex)
		if value == nil {
			fieldPtr.Set(reflect.Zero(c.typ))
		} else {
			fieldPtr.Set(reflect.ValueOf(value))
		}
	} else {
		sv := reflect.ValueOf(entityPtr).Elem()
		fieldPtr := sv.Field(c.fieldIndex)
		sliceVal := reflect.MakeSlice(c.typ, len(children), len(children))
		for j, val := range children {
			sliceVal.Index(j).Set(reflect.ValueOf(val))
		}
		fieldPtr.Set(sliceVal)
	}
}

type entityMapping struct {
	entityType  reflect.Type
	schema      string
	table       string
	fieldMap    map[string]*field
	childMap    map[string]*child
	childFields []string
	allFields   []string
	primaryKey  []string
	parentalKey []string
	insertable  []string
	updatable   []string

	// Pre-computed column lists (private, immutable after initialization)
	allColumns             []string
	primaryColumns         []string
	parentalColumns        []string
	insertableColumns      []string
	updatableColumns       []string
	insertReturningColumns []string

	// Pre-computed field name lists for RETURNING operations
	insertReturning []string
	saveFields      []string
	saveFieldSet    []saveField
}

func newEntityMapping(
	entityType reflect.Type,
	schema string,
	table string,
	fieldMap map[string]*field,
	childMap map[string]*child,
	childFields []string,
	allFields []string,
	primaryKey []string,
	parentalKey []string,
	insertable []string,
	updatable []string,
) *entityMapping {
	em := &entityMapping{
		entityType:             entityType,
		schema:                 schema,
		table:                  table,
		fieldMap:               fieldMap,
		childMap:               childMap,
		childFields:            childFields,
		allFields:              allFields,
		primaryKey:             primaryKey,
		parentalKey:            parentalKey,
		insertable:             insertable,
		updatable:              updatable,
		allColumns:             make([]string, 0, len(allFields)),
		primaryColumns:         make([]string, 0, len(primaryKey)),
		parentalColumns:        make([]string, 0, len(parentalKey)),
		insertableColumns:      make([]string, 0, len(insertable)),
		updatableColumns:       make([]string, 0, len(updatable)),
		insertReturningColumns: make([]string, 0, len(allFields)),
		saveFields:             make([]string, 0, len(allFields)),
		saveFieldSet:           make([]saveField, 0, len(allFields)),
	}
	for _, name := range allFields {
		em.allColumns = append(em.allColumns, fieldMap[name].column)
	}
	for _, name := range primaryKey {
		em.primaryColumns = append(em.primaryColumns, fieldMap[name].column)
	}
	for _, name := range parentalKey {
		em.parentalColumns = append(em.parentalColumns, fieldMap[name].column)
	}
	for _, name := range insertable {
		em.insertableColumns = append(em.insertableColumns, fieldMap[name].column)
	}
	for _, name := range updatable {
		em.updatableColumns = append(em.updatableColumns, fieldMap[name].column)
	}
	for _, name := range allFields {
		if slices.Contains(insertable, name) || slices.Contains(primaryKey, name) || slices.Contains(updatable, name) {
			em.saveFields = append(em.saveFields, name)
		}
	}
	for _, name := range em.saveFields {
		em.saveFieldSet = append(em.saveFieldSet, saveField{
			name:       name,
			column:     fieldMap[name].column,
			typ:        fieldMap[name].typ,
			primaryKey: slices.Contains(primaryKey, name),
			generated:  !slices.Contains(insertable, name),
			insertable: slices.Contains(insertable, name),
			updatable:  slices.Contains(updatable, name),
		})
	}

	em.insertReturning = append(em.insertReturning, primaryKey...)
	for _, name := range allFields {
		if !slices.Contains(insertable, name) && !slices.Contains(primaryKey, name) {
			em.insertReturning = append(em.insertReturning, name)
		}
	}
	for _, name := range em.insertReturning {
		em.insertReturningColumns = append(em.insertReturningColumns, fieldMap[name].column)
	}

	return em
}

func (em *entityMapping) fieldTypes(fieldNames []string) []reflect.Type {
	types := make([]reflect.Type, len(fieldNames))
	for i, name := range fieldNames {
		types[i] = em.fieldMap[name].typ
	}
	return types
}
