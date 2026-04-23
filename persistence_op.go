package ormapper

import (
	"context"
	"fmt"
	"reflect"
)

type persistenceOp struct {
	mapper     *Mapper
	backend    backend
	scanBuffer []any
}

type saveResult struct {
	entity   any
	key      Key
	inserted bool
}

type childKeyRow struct {
	parentKey Key
	childKey  Key
}

func newPersistenceOp(mapper *Mapper, db DBTX) *persistenceOp {
	return &persistenceOp{
		mapper:  mapper,
		backend: mapper.dialect.newBackend(db),
	}
}

func (u *persistenceOp) scanEntity(em *entityMapping, entityPtr any, row rows, fieldNames []string) error {
	u.scanBuffer = u.scanBuffer[:0]
	if cap(u.scanBuffer) < len(fieldNames) {
		u.scanBuffer = make([]any, 0, len(fieldNames))
	}

	for _, name := range fieldNames {
		field := em.FieldMap[name]
		u.scanBuffer = append(u.scanBuffer, field.GetPtr(entityPtr))
	}

	return row.Scan(u.scanBuffer...)
}

func (u *persistenceOp) get(ctx context.Context, em *entityMapping, keyColumns []string, ids []Key) ([]any, error) {
	if len(ids) == 0 {
		return nil, nil
	}

	for _, id := range ids {
		if id.Length() != len(keyColumns) {
			return nil, fmt.Errorf("key has %d values but %d key columns expected (entity %s, key columns: %v)", id.Length(), len(keyColumns), em.EntityType, keyColumns)
		}
	}

	rows, err := u.backend.Select(ctx, selectOp{
		Select:     em.Columns(),
		FromSchema: em.Schema,
		FromTable:  em.Table,
		KeyColumns: keyColumns,
		Keys:       ids,
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	entities := make([]any, 0)
	for rows.Next() {
		entityPtr := reflect.New(em.EntityType).Interface()
		if err := u.scanEntity(em, entityPtr, rows, em.AllFields); err != nil {
			return nil, err
		}
		entities = append(entities, entityPtr)
	}

	if len(entities) > 0 && len(em.ChildMap) > 0 {
		if err := u.loadChildren(ctx, em, entities); err != nil {
			return nil, err
		}
	}

	return entities, nil
}

func (u *persistenceOp) loadChildren(ctx context.Context, em *entityMapping, parents []any) error {
	parentKeys := make([]Key, len(parents))
	parentKeyToParent := make(map[Key]any, len(parents))
	for i, parent := range parents {
		parentKeys[i] = em.ExtractKey(parent, em.PrimaryKey)
		parentKeyToParent[parentKeys[i]] = parent
	}

	for _, child := range em.ChildMap {
		childMapping, err := u.mapper.getMapping(child.Target)
		if err != nil {
			return err
		}

		childEntities, err := u.get(ctx, childMapping, childMapping.ParentalColumns(), parentKeys)
		if err != nil {
			return err
		}

		estimatedCapacity := 4
		if len(parents) > 0 {
			estimatedCapacity = (len(childEntities) + len(parents) - 1) / len(parents)
			if estimatedCapacity < 4 {
				estimatedCapacity = 4
			}
		}

		childGroups := make(map[any][]any, len(parents))
		for _, childEntity := range childEntities {
			parentalKey := childMapping.ExtractKey(childEntity, childMapping.ParentalKey)
			if parent, ok := parentKeyToParent[parentalKey]; ok {
				if childGroups[parent] == nil {
					childGroups[parent] = make([]any, 0, estimatedCapacity)
				}
				childGroups[parent] = append(childGroups[parent], childEntity)
			}
		}

		for _, parent := range parents {
			child.Set(parent, childGroups[parent])
		}
	}

	return nil
}

func (u *persistenceOp) existingKeysByKeys(ctx context.Context, em *entityMapping, keys []Key) (map[Key]struct{}, error) {
	if len(keys) == 0 {
		return nil, nil
	}

	rows, err := u.backend.Select(ctx, selectOp{
		Select:     em.PrimaryColumns(),
		FromSchema: em.Schema,
		FromTable:  em.Table,
		KeyColumns: em.PrimaryColumns(),
		Keys:       keys,
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	existing := make(map[Key]struct{}, len(keys))
	for rows.Next() {
		entityPtr := reflect.New(em.EntityType).Interface()
		if err := u.scanEntity(em, entityPtr, rows, em.PrimaryKey); err != nil {
			return nil, err
		}
		existing[em.ExtractKey(entityPtr, em.PrimaryKey)] = struct{}{}
	}

	return existing, nil
}

func (u *persistenceOp) save(ctx context.Context, em *entityMapping, entities []any) ([]saveResult, error) {
	if len(entities) == 0 {
		return nil, nil
	}

	directInsert := make([]any, 0, len(entities))
	upsert := make([]any, 0, len(entities))

	for _, entity := range entities {
		if em.allPrimaryKeyGenerated() && em.primaryKeyIsZero(entity) {
			directInsert = append(directInsert, entity)
			continue
		}
		upsert = append(upsert, entity)
	}

	results := make([]saveResult, 0, len(entities))

	if len(directInsert) > 0 {
		inserted, err := u.insert(ctx, em, directInsert)
		if err != nil {
			return nil, err
		}
		for _, entity := range inserted {
			results = append(results, saveResult{
				entity:   entity,
				key:      em.ExtractKey(entity, em.PrimaryKey),
				inserted: true,
			})
		}
	}

	if len(upsert) > 0 {
		upserted, err := u.upsert(ctx, em, upsert)
		if err != nil {
			return nil, err
		}
		for _, entity := range upserted {
			results = append(results, saveResult{
				entity: entity,
				key:    em.ExtractKey(entity, em.PrimaryKey),
			})
		}
	}

	for _, child := range em.ChildMap {
		childMapping, err := u.mapper.getMapping(child.Target)
		if err != nil {
			return nil, err
		}

		toSave := make([]any, 0)
		for _, result := range results {
			childEntities := child.Get(result.entity)

			for _, childEntity := range childEntities {
				for i, fieldName := range childMapping.ParentalKey {
					field := childMapping.FieldMap[fieldName]
					field.SetValue(childEntity, result.key.At(i))
				}
			}

			toSave = append(toSave, childEntities...)
		}

		if len(toSave) > 0 {
			if _, err := u.save(ctx, childMapping, toSave); err != nil {
				return nil, err
			}
		}

		parentKeys := make([]Key, 0, len(results))
		currentKeysByParent := make(map[Key]map[Key]struct{}, len(results))
		for _, result := range results {
			if result.inserted {
				continue
			}
			parentKeys = append(parentKeys, result.key)
			currentChildren := child.Get(result.entity)
			currentKeys := make(map[Key]struct{}, len(currentChildren))
			for _, childEntity := range currentChildren {
				currentKeys[childMapping.ExtractKey(childEntity, childMapping.PrimaryKey)] = struct{}{}
			}
			currentKeysByParent[result.key] = currentKeys
		}

		existingChildren, err := u.selectChildKeysByParents(ctx, childMapping, parentKeys)
		if err != nil {
			return nil, err
		}
		toDelete := make([]Key, 0)
		for _, existingChild := range existingChildren {
			if _, ok := currentKeysByParent[existingChild.parentKey][existingChild.childKey]; !ok {
				toDelete = append(toDelete, existingChild.childKey)
			}
		}
		if err := u.deleteByKeys(ctx, childMapping, toDelete); err != nil {
			return nil, err
		}
	}

	return results, nil
}

func (u *persistenceOp) insert(ctx context.Context, em *entityMapping, entities []any) ([]any, error) {
	insertCols := em.InsertableColumns()
	values := make([][]any, len(entities))
	for i, entity := range entities {
		row := make([]any, len(insertCols))
		for j, fieldName := range em.Insertable {
			field := em.FieldMap[fieldName]
			row[j] = field.GetValue(entity)
		}
		values[i] = row
	}

	rows, err := u.backend.Insert(ctx, insertOp{
		IntoSchema: em.Schema,
		IntoTable:  em.Table,
		Insert:     em.InsertableColumns(),
		Returning:  em.InsertReturningColumns(),
		Values:     values,
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	result := make([]any, 0, len(entities))
	i := 0
	for rows.Next() {
		if i >= len(entities) {
			return nil, fmt.Errorf("more rows than entities")
		}
		if err := u.scanEntity(em, entities[i], rows, em.InsertReturning()); err != nil {
			return nil, err
		}
		result = append(result, entities[i])
		i++
	}

	return result, nil
}

func (u *persistenceOp) upsert(ctx context.Context, em *entityMapping, entities []any) ([]any, error) {
	insertFields := em.upsertFields()
	insertCols := em.upsertColumns()
	values := make([][]any, len(entities))
	for i, entity := range entities {
		row := make([]any, len(insertCols))
		for j, fieldName := range insertFields {
			field := em.FieldMap[fieldName]
			row[j] = field.GetValue(entity)
		}
		values[i] = row
	}

	rows, err := u.backend.Upsert(ctx, upsertOp{
		IntoSchema: em.Schema,
		IntoTable:  em.Table,
		Insert:     insertCols,
		Conflict:   em.PrimaryColumns(),
		Update:     em.UpdatableColumns(),
		Returning:  em.InsertReturningColumns(),
		Values:     values,
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	result := make([]any, 0, len(entities))
	i := 0
	for rows.Next() {
		if i >= len(entities) {
			return nil, fmt.Errorf("more rows than entities")
		}
		if err := u.scanEntity(em, entities[i], rows, em.InsertReturning()); err != nil {
			return nil, err
		}
		result = append(result, entities[i])
		i++
	}

	return result, nil
}

func (u *persistenceOp) deleteByKeys(ctx context.Context, em *entityMapping, keys []Key) error {
	keys = uniqueKeys(keys)
	if len(keys) == 0 {
		return nil
	}

	for _, child := range em.ChildMap {
		childMapping, err := u.mapper.getMapping(child.Target)
		if err != nil {
			return err
		}

		childRows, err := u.selectChildKeysByParents(ctx, childMapping, keys)
		if err != nil {
			return err
		}
		childKeys := make([]Key, 0, len(childRows))
		for _, row := range childRows {
			childKeys = append(childKeys, row.childKey)
		}
		if err := u.deleteByKeys(ctx, childMapping, childKeys); err != nil {
			return err
		}
	}

	return u.backend.Delete(ctx, deleteOp{
		FromSchema: em.Schema,
		FromTable:  em.Table,
		KeyColumns: em.PrimaryColumns(),
		Keys:       keys,
	})
}

func (u *persistenceOp) selectChildKeysByParents(ctx context.Context, em *entityMapping, parentKeys []Key) ([]childKeyRow, error) {
	parentKeys = uniqueKeys(parentKeys)
	if len(parentKeys) == 0 {
		return nil, nil
	}

	fieldNames := uniqueFieldNames(em.PrimaryKey, em.ParentalKey)
	rows, err := u.backend.Select(ctx, selectOp{
		Select:     computeColumns(fieldNames, em.FieldMap),
		FromSchema: em.Schema,
		FromTable:  em.Table,
		KeyColumns: em.ParentalColumns(),
		Keys:       parentKeys,
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	result := make([]childKeyRow, 0)
	for rows.Next() {
		entityPtr := reflect.New(em.EntityType).Interface()
		if err := u.scanEntity(em, entityPtr, rows, fieldNames); err != nil {
			return nil, err
		}
		result = append(result, childKeyRow{
			parentKey: em.ExtractKey(entityPtr, em.ParentalKey),
			childKey:  em.ExtractKey(entityPtr, em.PrimaryKey),
		})
	}
	return result, nil
}

func uniqueKeys(keys []Key) []Key {
	if len(keys) < 2 {
		return keys
	}
	result := make([]Key, 0, len(keys))
	seen := make(map[Key]struct{}, len(keys))
	for _, key := range keys {
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, key)
	}
	return result
}

func uniqueFieldNames(groups ...[]string) []string {
	total := 0
	for _, group := range groups {
		total += len(group)
	}
	result := make([]string, 0, total)
	seen := make(map[string]struct{}, total)
	for _, group := range groups {
		for _, name := range group {
			if _, ok := seen[name]; ok {
				continue
			}
			seen[name] = struct{}{}
			result = append(result, name)
		}
	}
	return result
}
