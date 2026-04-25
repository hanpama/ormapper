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

func (f *field) ptrFrom(entity reflect.Value) any {
	return entity.Field(f.fieldIndex).Addr().Interface()
}

func (f *field) valueFrom(entity reflect.Value) any {
	return entity.Field(f.fieldIndex).Interface()
}

func (f *field) setOn(entity reflect.Value, value any) {
	field := entity.Field(f.fieldIndex)
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

type fieldPlan struct {
	fields  []*field
	columns []string
	types   []reflect.Type
}

func newFieldPlan(fieldMap map[string]*field, fieldNames []string) fieldPlan {
	plan := fieldPlan{
		fields:  make([]*field, 0, len(fieldNames)),
		columns: make([]string, 0, len(fieldNames)),
		types:   make([]reflect.Type, 0, len(fieldNames)),
	}
	for _, name := range fieldNames {
		field := fieldMap[name]
		plan.fields = append(plan.fields, field)
		plan.columns = append(plan.columns, field.column)
		plan.types = append(plan.types, field.typ)
	}
	return plan
}

type child struct {
	target     reflect.Type
	singular   bool
	typ        reflect.Type
	fieldIndex int // Index of the field in the struct for direct reflect access
}

func (c *child) count(entityPtr any) int {
	sv := reflect.ValueOf(entityPtr).Elem()
	fieldPtr := sv.Field(c.fieldIndex)
	if c.singular {
		if fieldPtr.IsNil() {
			return 0
		}
		return 1
	}
	return fieldPtr.Len()
}

func (c *child) appendTo(entityPtr any, dst []any) []any {
	sv := reflect.ValueOf(entityPtr).Elem()
	fieldPtr := sv.Field(c.fieldIndex)
	if c.singular {
		if fieldPtr.IsNil() {
			return dst
		}
		return append(dst, fieldPtr.Interface())
	}

	for j := 0; j < fieldPtr.Len(); j++ {
		dst = append(dst, fieldPtr.Index(j).Interface())
	}
	return dst
}

func (c *child) setByParentIndexes(parents []any, children []any, parentIndexes []int, counts []int) {
	if c.singular {
		for _, parent := range parents {
			entity := reflect.ValueOf(parent).Elem()
			entity.Field(c.fieldIndex).Set(reflect.Zero(c.typ))
		}

		clear(counts)
		for i, childEntity := range children {
			parentIndex := parentIndexes[i]
			if parentIndex < 0 || counts[parentIndex] > 0 {
				continue
			}
			entity := reflect.ValueOf(parents[parentIndex]).Elem()
			entity.Field(c.fieldIndex).Set(reflect.ValueOf(childEntity))
			counts[parentIndex] = 1
		}
		return
	}

	parentSlices := make([]reflect.Value, len(parents))
	for i, parent := range parents {
		sliceVal := reflect.MakeSlice(c.typ, counts[i], counts[i])
		parentSlices[i] = sliceVal
		entity := reflect.ValueOf(parent).Elem()
		entity.Field(c.fieldIndex).Set(sliceVal)
	}

	clear(counts)
	for i, childEntity := range children {
		parentIndex := parentIndexes[i]
		if parentIndex < 0 {
			continue
		}
		index := counts[parentIndex]
		parentSlices[parentIndex].Index(index).Set(reflect.ValueOf(childEntity))
		counts[parentIndex]++
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
	allColumns        []string
	primaryColumns    []string
	parentalColumns   []string
	insertableColumns []string
	updatableColumns  []string

	allPlan        fieldPlan
	primaryPlan    fieldPlan
	parentalPlan   fieldPlan
	insertablePlan fieldPlan
	updatablePlan  fieldPlan

	saveLayout *saveLayout
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
		entityType:        entityType,
		schema:            schema,
		table:             table,
		fieldMap:          fieldMap,
		childMap:          childMap,
		childFields:       childFields,
		allFields:         allFields,
		primaryKey:        primaryKey,
		parentalKey:       parentalKey,
		insertable:        insertable,
		updatable:         updatable,
		allColumns:        make([]string, 0, len(allFields)),
		primaryColumns:    make([]string, 0, len(primaryKey)),
		parentalColumns:   make([]string, 0, len(parentalKey)),
		insertableColumns: make([]string, 0, len(insertable)),
		updatableColumns:  make([]string, 0, len(updatable)),
	}
	em.allPlan = newFieldPlan(fieldMap, allFields)
	em.primaryPlan = newFieldPlan(fieldMap, primaryKey)
	em.parentalPlan = newFieldPlan(fieldMap, parentalKey)
	em.insertablePlan = newFieldPlan(fieldMap, insertable)
	em.updatablePlan = newFieldPlan(fieldMap, updatable)
	em.allColumns = em.allPlan.columns
	em.primaryColumns = em.primaryPlan.columns
	em.parentalColumns = em.parentalPlan.columns
	em.insertableColumns = em.insertablePlan.columns
	em.updatableColumns = em.updatablePlan.columns
	em.saveLayout = newSaveLayout(fieldMap, allFields, primaryKey, insertable, updatable)

	return em
}

func (em *entityMapping) newEntity() any {
	return reflect.New(em.entityType).Interface()
}

func newSaveLayout(
	fieldMap map[string]*field,
	allFields []string,
	primaryKey []string,
	insertable []string,
	updatable []string,
) *saveLayout {
	layout := &saveLayout{
		rowFields:       make([]*field, 0, len(allFields)),
		returningFields: make([]*field, 0, len(allFields)),
	}
	rows := &layout.rows
	rows.rowColumns = make([]string, 0, len(allFields))
	rows.insertColumns = make([]string, 0, len(insertable))
	rows.insertIndexes = make([]int, 0, len(insertable))
	rows.insertColumnsWithGeneratedPrimary = make([]string, 0, len(insertable)+len(primaryKey))
	rows.updateColumns = make([]string, 0, len(updatable))
	rows.primaryColumns = make([]string, 0, len(primaryKey))
	rows.primaryIndexes = make([]int, 0, len(primaryKey))
	rows.primaryTypes = make([]reflect.Type, 0, len(primaryKey))
	rows.generatedPrimaryColumns = make([]string, 0, len(primaryKey))
	rows.generatedPrimaryIndexes = make([]int, 0, len(primaryKey))
	rows.returningColumns = make([]string, 0, len(allFields))
	rows.primaryReturningIndexes = make([]int, 0, len(primaryKey))
	rowIndexByName := make(map[string]int, len(allFields))

	for _, name := range allFields {
		if slices.Contains(insertable, name) || slices.Contains(primaryKey, name) || slices.Contains(updatable, name) {
			field := fieldMap[name]
			rowIndexByName[name] = len(layout.rowFields)
			layout.rowFields = append(layout.rowFields, field)
			rows.rowColumns = append(rows.rowColumns, field.column)
		}
	}

	for i, field := range layout.rowFields {
		if slices.Contains(insertable, field.name) {
			rows.insertColumns = append(rows.insertColumns, field.column)
			rows.insertIndexes = append(rows.insertIndexes, i)
		}
		if slices.Contains(updatable, field.name) {
			rows.updateColumns = append(rows.updateColumns, field.column)
		}
	}

	for _, name := range primaryKey {
		column := fieldMap[name].column
		rows.primaryColumns = append(rows.primaryColumns, column)
		rows.primaryIndexes = append(rows.primaryIndexes, rowIndexByName[name])
		rows.primaryTypes = append(rows.primaryTypes, fieldMap[name].typ)
		if !slices.Contains(insertable, name) {
			rows.generatedPrimaryColumns = append(rows.generatedPrimaryColumns, column)
			rows.generatedPrimaryIndexes = append(rows.generatedPrimaryIndexes, rowIndexByName[name])
		}
	}

	rows.insertColumnsWithGeneratedPrimary = append(rows.insertColumnsWithGeneratedPrimary, rows.generatedPrimaryColumns...)
	rows.insertColumnsWithGeneratedPrimary = append(rows.insertColumnsWithGeneratedPrimary, rows.insertColumns...)

	for _, name := range primaryKey {
		layout.returningFields = append(layout.returningFields, fieldMap[name])
	}
	for _, name := range allFields {
		if !slices.Contains(insertable, name) && !slices.Contains(primaryKey, name) {
			layout.returningFields = append(layout.returningFields, fieldMap[name])
		}
	}
	for _, field := range layout.returningFields {
		rows.returningColumns = append(rows.returningColumns, field.column)
	}
	for _, column := range rows.primaryColumns {
		rows.primaryReturningIndexes = append(rows.primaryReturningIndexes, slices.Index(rows.returningColumns, column))
	}

	return layout
}

func (em *entityMapping) extractKey(entity any, fieldNames []string) Key {
	return extractKeyFromFields(entity, em.fieldsByName(fieldNames))
}

func (em *entityMapping) extractPrimaryKey(entity any) Key {
	return extractKeyFromFields(entity, em.primaryPlan.fields)
}

func (em *entityMapping) extractParentalKey(entity any) Key {
	return extractKeyFromFields(entity, em.parentalPlan.fields)
}

func (em *entityMapping) fieldsByName(fieldNames []string) []*field {
	fields := make([]*field, len(fieldNames))
	for i, name := range fieldNames {
		fields[i] = em.fieldMap[name]
	}
	return fields
}

func extractKeyFromFields(entity any, fields []*field) Key {
	if len(fields) > 9 {
		panic("ormapper: Key supports up to 9 column values")
	}

	entityValue := reflect.ValueOf(entity).Elem()
	var values [9]any
	for i, field := range fields {
		values[i] = field.valueFrom(entityValue)
	}
	return newKeyFromValues(values[:len(fields)])
}
