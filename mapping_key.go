package ormapper

import "reflect"

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
