package ormapper

import (
	"context"
	"fmt"
	"reflect"
)

// Mapper stores compiled mappings and executes aggregate persistence operations.
type Mapper struct {
	dialect  Dialect
	mappings mappingRegistry
}

func (m *Mapper) getMapping(entityType reflect.Type) (*entityMapping, error) {
	return m.mappings.get(entityType)
}

// Get loads a single aggregate by primary key into dest.
//
// dest must be a pointer to an entity pointer, for example:
//
//	var order *Order
//	err := mapper.Get(ctx, tx, &order, ormapper.NewKey(id))
//
// When the row does not exist, Get leaves *dest as nil.
func (m *Mapper) Get(ctx context.Context, db DBTX, dest any, id Key) error {
	entityType, err := unwrapDestEntityType(dest, "Get")
	if err != nil {
		return err
	}

	mapping, err := m.getMapping(entityType)
	if err != nil {
		return err
	}

	u := newPersistence(m.mappings, m.dialect.newBackend(db))
	entities, err := u.getByKeys(ctx, mapping, []Key{id})
	if err != nil {
		return err
	}

	destVal := reflect.ValueOf(dest).Elem()
	destVal.Set(reflect.Zero(destVal.Type()))
	if len(entities) > 0 {
		destVal.Set(reflect.ValueOf(entities[0]))
	}

	return nil
}

// Save persists the authoritative aggregate snapshot.
//
// Use Save with a full authoritative aggregate state. Omitted children are
// treated as removed from the aggregate. Auto primary keys use zero as insert
// intent and non-zero as update intent.
func (m *Mapper) Save(ctx context.Context, db DBTX, entity any) error {
	entityType, err := unwrapEntityType(entity, "Save")
	if err != nil {
		return err
	}

	mapping, err := m.getMapping(entityType)
	if err != nil {
		return err
	}

	_, err = newPersistence(m.mappings, m.dialect.newBackend(db)).save(ctx, mapping, []any{entity})
	return err
}

// Delete removes the aggregate and its descendants from the database.
//
// entity only needs to provide the primary key fields used to identify the root.
func (m *Mapper) Delete(ctx context.Context, db DBTX, entity any) error {
	entityType, err := unwrapEntityType(entity, "Delete")
	if err != nil {
		return err
	}

	mapping, err := m.getMapping(entityType)
	if err != nil {
		return err
	}

	u := newPersistence(m.mappings, m.dialect.newBackend(db))
	key := mapping.extractKey(entity, mapping.primaryKey)
	return u.deleteByKeys(ctx, mapping, []Key{key})
}

func unwrapDestEntityType(dest any, op string) (reflect.Type, error) {
	destType := reflect.TypeOf(dest)
	if destType == nil {
		return nil, fmt.Errorf("%s: dest must be a pointer to entity pointer, got <nil>", op)
	}
	if destType.Kind() != reflect.Ptr {
		return nil, fmt.Errorf("%s: dest must be a pointer to entity pointer, got %s", op, destType)
	}
	if destType.Elem().Kind() != reflect.Ptr {
		return nil, fmt.Errorf("%s: dest must be a pointer to entity pointer, got %s", op, destType)
	}
	entityType := destType.Elem().Elem()
	if entityType.Kind() != reflect.Struct {
		return nil, fmt.Errorf("%s: entity type must be a struct, got %s", op, entityType)
	}
	return entityType, nil
}

func unwrapEntityType(entity any, op string) (reflect.Type, error) {
	entityType := reflect.TypeOf(entity)
	if entityType == nil {
		return nil, fmt.Errorf("%s: entity must be a pointer to struct, got <nil>", op)
	}
	if entityType.Kind() != reflect.Ptr {
		return nil, fmt.Errorf("%s: entity must be a pointer to struct, got %s", op, entityType)
	}
	entityType = entityType.Elem()
	if entityType.Kind() != reflect.Struct {
		return nil, fmt.Errorf("%s: entity must be a pointer to struct, got %s", op, reflect.TypeOf(entity))
	}
	return entityType, nil
}
