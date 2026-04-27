package agg

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
//	err := mapper.Get(ctx, tx, &order, NewKey(id))
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

// GetMany loads aggregates by primary key into dest.
//
// dest must be a pointer to a slice of entity pointers, for example:
//
//	var orders []*Order
//	err := mapper.GetMany(ctx, tx, &orders, []Key{NewKey(id1), NewKey(id2)})
//
// The result slice has the same length and order as ids. Missing rows are nil.
func (m *Mapper) GetMany(ctx context.Context, db DBTX, dest any, ids []Key) error {
	entityType, err := unwrapDestSliceEntityType(dest, "GetMany")
	if err != nil {
		return err
	}

	mapping, err := m.getMapping(entityType)
	if err != nil {
		return err
	}

	u := newPersistence(m.mappings, m.dialect.newBackend(db))
	entities, err := u.getByKeys(ctx, mapping, uniqueKeys(ids))
	if err != nil {
		return err
	}

	entitiesByKey := make(map[Key]any, len(entities))
	for _, entity := range entities {
		entitiesByKey[mapping.extractPrimaryKey(entity)] = entity
	}

	destVal := reflect.ValueOf(dest).Elem()
	result := reflect.MakeSlice(destVal.Type(), len(ids), len(ids))
	for i, id := range ids {
		if entity, ok := entitiesByKey[id]; ok {
			result.Index(i).Set(reflect.ValueOf(entity))
		}
	}
	destVal.Set(result)

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

	return newPersistence(m.mappings, m.dialect.newBackend(db)).save(ctx, mapping, []any{entity})
}

// SaveMany persists authoritative aggregate snapshots in one batched operation.
//
// entities must be a slice of entity pointers of one mapped type.
func (m *Mapper) SaveMany(ctx context.Context, db DBTX, entities any) error {
	entityType, values, err := unwrapEntitySlice(entities, "SaveMany")
	if err != nil {
		return err
	}

	mapping, err := m.getMapping(entityType)
	if err != nil {
		return err
	}

	return newPersistence(m.mappings, m.dialect.newBackend(db)).save(ctx, mapping, values)
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
	key := mapping.extractPrimaryKey(entity)
	return u.deleteByKeys(ctx, mapping, []Key{key})
}

// DeleteMany removes aggregates and their descendants in one batched operation.
//
// entities only need to provide the primary key fields used to identify roots.
// entities must be a slice of entity pointers of one mapped type.
func (m *Mapper) DeleteMany(ctx context.Context, db DBTX, entities any) error {
	entityType, values, err := unwrapEntitySlice(entities, "DeleteMany")
	if err != nil {
		return err
	}

	mapping, err := m.getMapping(entityType)
	if err != nil {
		return err
	}

	keys := make([]Key, len(values))
	for i, entity := range values {
		keys[i] = mapping.extractPrimaryKey(entity)
	}

	return newPersistence(m.mappings, m.dialect.newBackend(db)).deleteByKeys(ctx, mapping, uniqueKeys(keys))
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

func unwrapDestSliceEntityType(dest any, op string) (reflect.Type, error) {
	destType := reflect.TypeOf(dest)
	if destType == nil {
		return nil, fmt.Errorf("%s: dest must be a pointer to a slice of entity pointers, got <nil>", op)
	}
	if destType.Kind() != reflect.Ptr {
		return nil, fmt.Errorf("%s: dest must be a pointer to a slice of entity pointers, got %s", op, destType)
	}
	sliceType := destType.Elem()
	if sliceType.Kind() != reflect.Slice {
		return nil, fmt.Errorf("%s: dest must be a pointer to a slice of entity pointers, got %s", op, destType)
	}
	elemType := sliceType.Elem()
	if elemType.Kind() != reflect.Ptr {
		return nil, fmt.Errorf("%s: dest slice element must be an entity pointer, got %s", op, elemType)
	}
	entityType := elemType.Elem()
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

func unwrapEntitySlice(entities any, op string) (reflect.Type, []any, error) {
	entitiesType := reflect.TypeOf(entities)
	if entitiesType == nil {
		return nil, nil, fmt.Errorf("%s: entities must be a slice of entity pointers, got <nil>", op)
	}
	if entitiesType.Kind() != reflect.Slice && entitiesType.Kind() != reflect.Array {
		return nil, nil, fmt.Errorf("%s: entities must be a slice of entity pointers, got %s", op, entitiesType)
	}
	elemType := entitiesType.Elem()
	if elemType.Kind() != reflect.Ptr {
		return nil, nil, fmt.Errorf("%s: entity slice element must be a pointer to struct, got %s", op, elemType)
	}
	entityType := elemType.Elem()
	if entityType.Kind() != reflect.Struct {
		return nil, nil, fmt.Errorf("%s: entity type must be a struct, got %s", op, entityType)
	}

	entitiesValue := reflect.ValueOf(entities)
	values := make([]any, entitiesValue.Len())
	for i := range values {
		entity := entitiesValue.Index(i)
		if entity.IsNil() {
			return nil, nil, fmt.Errorf("%s: entity at index %d must be a pointer to struct, got <nil>", op, i)
		}
		values[i] = entity.Interface()
	}
	return entityType, values, nil
}
