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

func (u *persistenceOp) existingKeys(ctx context.Context, em *entityMapping, entities []any) (map[Key]struct{}, error) {
	if len(entities) == 0 {
		return nil, nil
	}

	keys := make([]Key, 0, len(entities))
	seen := make(map[Key]struct{}, len(entities))
	for _, entity := range entities {
		key := em.ExtractKey(entity, em.PrimaryKey)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		keys = append(keys, key)
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

func (u *persistenceOp) save(ctx context.Context, em *entityMapping, entities []any) error {
	if len(entities) == 0 {
		return nil
	}

	existing, err := u.existingKeys(ctx, em, entities)
	if err != nil {
		return err
	}

	toInsert := make([]any, 0, len(entities))
	toUpdate := make([]any, 0, len(entities))
	for _, entity := range entities {
		key := em.ExtractKey(entity, em.PrimaryKey)
		if _, ok := existing[key]; ok {
			toUpdate = append(toUpdate, entity)
		} else {
			toInsert = append(toInsert, entity)
		}
	}

	inserted := []any{}
	if len(toInsert) > 0 {
		inserted, err = u.insert(ctx, em, toInsert)
		if err != nil {
			return err
		}
	}
	if len(toUpdate) > 0 {
		if err := u.update(ctx, em, toUpdate); err != nil {
			return err
		}
	}

	allEntities := make([]any, 0, len(inserted)+len(toUpdate))
	allEntities = append(allEntities, toUpdate...)
	allEntities = append(allEntities, inserted...)

	for _, child := range em.ChildMap {
		childMapping, err := u.mapper.getMapping(child.Target)
		if err != nil {
			return err
		}

		toSave := make([]any, 0)
		for _, entity := range allEntities {
			childEntities := child.Get(entity)
			parentPrimaryKey := em.ExtractKey(entity, em.PrimaryKey)

			for _, childEntity := range childEntities {
				for i, fieldName := range childMapping.ParentalKey {
					field := childMapping.FieldMap[fieldName]
					field.SetValue(childEntity, parentPrimaryKey.At(i))
				}
			}

			toSave = append(toSave, childEntities...)
		}

		if len(toSave) > 0 {
			if err := u.save(ctx, childMapping, toSave); err != nil {
				return err
			}
		}

		for _, entity := range allEntities {
			parentPrimaryKey := em.ExtractKey(entity, em.PrimaryKey)
			currentChildren := child.Get(entity)
			currentKeys := make(map[Key]struct{}, len(currentChildren))
			for _, childEntity := range currentChildren {
				currentKeys[childMapping.ExtractKey(childEntity, childMapping.PrimaryKey)] = struct{}{}
			}

			existingChildren, err := u.get(ctx, childMapping, childMapping.ParentalColumns(), []Key{parentPrimaryKey})
			if err != nil {
				return err
			}

			toDelete := make([]any, 0)
			for _, existingChild := range existingChildren {
				key := childMapping.ExtractKey(existingChild, childMapping.PrimaryKey)
				if _, ok := currentKeys[key]; !ok {
					toDelete = append(toDelete, existingChild)
				}
			}

			if len(toDelete) > 0 {
				if err := u.delete(ctx, childMapping, toDelete); err != nil {
					return err
				}
			}
		}
	}

	return nil
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

func (u *persistenceOp) update(ctx context.Context, em *entityMapping, entities []any) error {
	setValues := make([][]any, len(entities))
	whereValues := make([][]any, len(entities))

	for i, entity := range entities {
		setRow := make([]any, len(em.Updatable))
		for j, fieldName := range em.Updatable {
			field := em.FieldMap[fieldName]
			setRow[j] = field.GetValue(entity)
		}
		setValues[i] = setRow

		whereRow := make([]any, len(em.PrimaryKey))
		for j, fieldName := range em.PrimaryKey {
			field := em.FieldMap[fieldName]
			whereRow[j] = field.GetValue(entity)
		}
		whereValues[i] = whereRow
	}

	return u.backend.Update(ctx, updateOp{
		Schema:      em.Schema,
		Table:       em.Table,
		Sets:        em.UpdatableColumns(),
		Where:       em.PrimaryColumns(),
		SetValues:   setValues,
		WhereValues: whereValues,
	})
}

func (u *persistenceOp) delete(ctx context.Context, em *entityMapping, entities []any) error {
	if len(entities) == 0 {
		return nil
	}

	for _, child := range em.ChildMap {
		childMapping, err := u.mapper.getMapping(child.Target)
		if err != nil {
			return err
		}

		toDeleteChildren := make([]any, 0)
		for _, parent := range entities {
			toDeleteChildren = append(toDeleteChildren, child.Get(parent)...)
		}

		if len(toDeleteChildren) > 0 {
			if err := u.delete(ctx, childMapping, toDeleteChildren); err != nil {
				return err
			}
		}
	}

	keys := make([]Key, len(entities))
	for i, entity := range entities {
		keys[i] = em.ExtractKey(entity, em.PrimaryKey)
	}

	return u.backend.Delete(ctx, deleteOp{
		FromSchema: em.Schema,
		FromTable:  em.Table,
		KeyColumns: em.PrimaryColumns(),
		Keys:       keys,
	})
}
