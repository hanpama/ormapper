package ormapper

import (
	"fmt"
	"reflect"
	"slices"
)

type mappingRegistry map[reflect.Type]*entityMapping

func (r mappingRegistry) get(entityType reflect.Type) (*entityMapping, error) {
	mapping, ok := r[entityType]
	if !ok {
		return nil, fmt.Errorf("no mapping found for type %s", entityType)
	}
	return mapping, nil
}

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

type saveLayout struct {
	rowFields       []*field
	returningFields []*field

	rowColumns                        []string
	insertColumns                     []string
	insertIndexes                     []int
	insertColumnsWithGeneratedPrimary []string
	updateColumns                     []string
	primaryColumns                    []string
	primaryIndexes          []int
	generatedPrimaryColumns []string
	generatedPrimaryIndexes []int
	returningColumns        []string

	keyFromReturning func([]any) Key
}

func (sl *saveLayout) projectEntity(entity any) ([]any, Key) {
	entityValue := reflect.ValueOf(entity).Elem()
	values := make([]any, len(sl.rowFields))
	for j, field := range sl.rowFields {
		values[j] = field.valueFrom(entityValue)
	}
	var keyValues [9]any
	if len(sl.primaryIndexes) > len(keyValues) {
		panic("ormapper: Key supports up to 9 column values")
	}
	for j, idx := range sl.primaryIndexes {
		keyValues[j] = values[idx]
	}
	return values, newKeyFromValues(keyValues[:len(sl.primaryIndexes)])
}

func (sl *saveLayout) isInsert(values []any) (bool, error) {
	generatedIndexes := sl.generatedPrimaryIndexes
	if len(generatedIndexes) == 0 {
		return false, nil
	}
	zeroCount := 0
	for _, idx := range generatedIndexes {
		value := values[idx]
		if value == nil {
			zeroCount++
			continue
		}
		v := reflect.ValueOf(value)
		if !v.IsValid() || v.IsZero() {
			zeroCount++
		}
	}
	switch zeroCount {
	case len(generatedIndexes):
		return true, nil
	case 0:
		return false, nil
	default:
		return false, fmt.Errorf("%w: generated primary key fields must be all zero or all non-zero", ErrUnsupportedSemantic)
	}
}

func (sl *saveLayout) hasGeneratedKey() bool {
	return len(sl.generatedPrimaryIndexes) > 0
}

func (sl *saveLayout) projectRows(entities []any) (inserts, candidates []plannedRow, err error) {
	submittedKeys := make(map[Key]int, len(entities))

	for i, entity := range entities {
		values, key := sl.projectEntity(entity)
		planned := plannedRow{index: i, key: key, values: values}

		insert, err := sl.isInsert(values)
		if err != nil {
			return nil, nil, err
		}

		if insert {
			inserts = append(inserts, planned)
		} else {
			if previous, ok := submittedKeys[planned.key]; ok {
				return nil, nil, fmt.Errorf("%w: duplicate submitted key %v at entity indexes %d and %d", ErrConsistency, planned.key, previous, i)
			}
			submittedKeys[planned.key] = i
			candidates = append(candidates, planned)
		}
	}

	return inserts, candidates, nil
}

func (sl *saveLayout) newInsertOp(schema, table string, rows []plannedRow) insertOp {
	return insertOp{
		schema:                            schema,
		table:                             table,
		rows:                              rows,
		insertColumns:                     sl.insertColumns,
		insertIndexes:                     sl.insertIndexes,
		insertColumnsWithGeneratedPrimary: sl.insertColumnsWithGeneratedPrimary,
		generatedPrimaryColumns:           sl.generatedPrimaryColumns,
		primaryColumns:                    sl.primaryColumns,
		returningColumns:                  sl.returningColumns,
		keyFromReturning:                  sl.keyFromReturning,
	}
}

func (sl *saveLayout) newUpdateOp(schema, table string, rows []plannedRow) updateOp {
	return updateOp{
		schema:           schema,
		table:            table,
		rows:             rows,
		rowColumns:       sl.rowColumns,
		primaryColumns:   sl.primaryColumns,
		updateColumns:    sl.updateColumns,
		returningColumns: sl.returningColumns,
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

	allPlan        fieldPlan
	primaryPlan    fieldPlan
	parentalPlan   fieldPlan
	insertablePlan fieldPlan
	updatablePlan  fieldPlan

	relationColumns []string // parentalPlan.columns + primaryPlan.columns

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
		entityType:  entityType,
		schema:      schema,
		table:       table,
		fieldMap:    fieldMap,
		childMap:    childMap,
		childFields: childFields,
		allFields:   allFields,
		primaryKey:  primaryKey,
		parentalKey: parentalKey,
	}
	em.allPlan = newFieldPlan(fieldMap, allFields)
	em.primaryPlan = newFieldPlan(fieldMap, primaryKey)
	em.parentalPlan = newFieldPlan(fieldMap, parentalKey)
	em.insertablePlan = newFieldPlan(fieldMap, insertable)
	em.updatablePlan = newFieldPlan(fieldMap, updatable)
	em.saveLayout = newSaveLayout(fieldMap, allFields, primaryKey, insertable, updatable)

	if len(parentalKey) > 0 {
		em.relationColumns = make([]string, 0, len(em.parentalPlan.columns)+len(em.primaryPlan.columns))
		em.relationColumns = append(em.relationColumns, em.parentalPlan.columns...)
		em.relationColumns = append(em.relationColumns, em.primaryPlan.columns...)
	}

	return em
}

func (em *entityMapping) newEntity() any {
	return reflect.New(em.entityType).Interface()
}

func (em *entityMapping) extractPrimaryKey(entity any) Key {
	return extractKeyFromFields(entity, em.primaryPlan.fields)
}

func (em *entityMapping) extractParentalKey(entity any) Key {
	return extractKeyFromFields(entity, em.parentalPlan.fields)
}

func (em *entityMapping) injectParentalKey(entity any, parentKey Key) {
	entityValue := reflect.ValueOf(entity).Elem()
	for i, field := range em.parentalPlan.fields {
		field.setOn(entityValue, parentKey.At(i))
	}
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
	layout.rowColumns = make([]string, 0, len(allFields))
	layout.insertColumns = make([]string, 0, len(insertable))
	layout.insertIndexes = make([]int, 0, len(insertable))
	layout.insertColumnsWithGeneratedPrimary = make([]string, 0, len(insertable)+len(primaryKey))
	layout.updateColumns = make([]string, 0, len(updatable))
	layout.primaryColumns = make([]string, 0, len(primaryKey))
	layout.primaryIndexes = make([]int, 0, len(primaryKey))
	layout.generatedPrimaryColumns = make([]string, 0, len(primaryKey))
	layout.generatedPrimaryIndexes = make([]int, 0, len(primaryKey))
	layout.returningColumns = make([]string, 0, len(allFields))
	rowIndexByName := make(map[string]int, len(allFields))

	for _, name := range allFields {
		if slices.Contains(insertable, name) || slices.Contains(primaryKey, name) || slices.Contains(updatable, name) {
			field := fieldMap[name]
			rowIndexByName[name] = len(layout.rowFields)
			layout.rowFields = append(layout.rowFields, field)
			layout.rowColumns = append(layout.rowColumns, field.column)
		}
	}

	for i, field := range layout.rowFields {
		if slices.Contains(insertable, field.name) {
			layout.insertColumns = append(layout.insertColumns, field.column)
			layout.insertIndexes = append(layout.insertIndexes, i)
		}
		if slices.Contains(updatable, field.name) {
			layout.updateColumns = append(layout.updateColumns, field.column)
		}
	}

	for _, name := range primaryKey {
		column := fieldMap[name].column
		layout.primaryColumns = append(layout.primaryColumns, column)
		layout.primaryIndexes = append(layout.primaryIndexes, rowIndexByName[name])
		if !slices.Contains(insertable, name) {
			layout.generatedPrimaryColumns = append(layout.generatedPrimaryColumns, column)
			layout.generatedPrimaryIndexes = append(layout.generatedPrimaryIndexes, rowIndexByName[name])
		}
	}

	layout.insertColumnsWithGeneratedPrimary = append(layout.insertColumnsWithGeneratedPrimary, layout.generatedPrimaryColumns...)
	layout.insertColumnsWithGeneratedPrimary = append(layout.insertColumnsWithGeneratedPrimary, layout.insertColumns...)

	for _, name := range primaryKey {
		layout.returningFields = append(layout.returningFields, fieldMap[name])
	}
	for _, name := range allFields {
		if !slices.Contains(insertable, name) && !slices.Contains(primaryKey, name) {
			layout.returningFields = append(layout.returningFields, fieldMap[name])
		}
	}
	for _, field := range layout.returningFields {
		layout.returningColumns = append(layout.returningColumns, field.column)
	}

	primaryReturningIndexes := make([]int, len(primaryKey))
	primaryTypes := make([]reflect.Type, len(primaryKey))
	for i, column := range layout.primaryColumns {
		primaryReturningIndexes[i] = slices.Index(layout.returningColumns, column)
		primaryTypes[i] = fieldMap[primaryKey[i]].typ
	}
	layout.keyFromReturning = func(values []any) Key {
		var kv [9]any
		for i, idx := range primaryReturningIndexes {
			val := values[idx]
			if b, ok := val.([]byte); ok {
				val = string(b)
			}
			if val != nil {
				v := reflect.ValueOf(val)
				if !v.Type().AssignableTo(primaryTypes[i]) && v.Type().ConvertibleTo(primaryTypes[i]) {
					val = v.Convert(primaryTypes[i]).Interface()
				}
			}
			kv[i] = val
		}
		return newKeyFromValues(kv[:len(primaryReturningIndexes)])
	}

	return layout
}
