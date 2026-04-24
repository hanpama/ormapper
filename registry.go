package ormapper

import (
	"fmt"
	"reflect"
)

type mappingRegistry map[reflect.Type]*entityMapping

func (r mappingRegistry) get(entityType reflect.Type) (*entityMapping, error) {
	mapping, ok := r[entityType]
	if !ok {
		return nil, fmt.Errorf("no mapping found for type %s", entityType)
	}
	return mapping, nil
}
