package ormapper

import (
	"context"
	"fmt"
	"reflect"
)

type persistence struct {
	registry   mappingRegistry
	backend    backend
	scanBuffer []any
}

type saveResult struct {
	entity   any
	key      Key
	inserted bool
}

type relationSnapshot struct {
	childMapping *entityMapping
	parentKeys   []Key
	keepPairs    []keepPair
}

func newPersistence(registry mappingRegistry, backend backend) *persistence {
	return &persistence{
		registry: registry,
		backend:  backend,
	}
}

func (u *persistence) scanEntity(em *entityMapping, entityPtr any, row rows, fieldNames []string) error {
	u.scanBuffer = u.scanBuffer[:0]
	if cap(u.scanBuffer) < len(fieldNames) {
		u.scanBuffer = make([]any, 0, len(fieldNames))
	}

	for _, name := range fieldNames {
		field := em.fieldMap[name]
		u.scanBuffer = append(u.scanBuffer, field.getPtr(entityPtr))
	}

	return row.Scan(u.scanBuffer...)
}

func (u *persistence) getByKeys(ctx context.Context, em *entityMapping, ids []Key) ([]any, error) {
	if len(ids) == 0 {
		return nil, nil
	}

	for _, id := range ids {
		if id.Length() != len(em.primaryKey) {
			return nil, fmt.Errorf("key has %d values but %d key columns expected (entity %s, key columns: %v)", id.Length(), len(em.primaryKey), em.entityType, em.primaryKey)
		}
	}

	rowSet, err := u.backend.LoadByKeys(ctx, loadRowsOp{
		schema:        em.schema,
		table:         em.table,
		selectColumns: em.allColumns,
		keyColumns:    em.primaryColumns,
		keys:          ids,
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = rowSet.Close() }()

	return u.scanEntities(ctx, em, rowSet)
}

func (u *persistence) getByParentKeys(ctx context.Context, em *entityMapping, parentKeys []Key) ([]any, error) {
	if len(parentKeys) == 0 {
		return nil, nil
	}

	for _, key := range parentKeys {
		if key.Length() != len(em.parentalKey) {
			return nil, fmt.Errorf("key has %d values but %d parental key columns expected (entity %s, key columns: %v)", key.Length(), len(em.parentalKey), em.entityType, em.parentalKey)
		}
	}

	rowSet, err := u.backend.LoadByParentKeys(ctx, loadRowsOp{
		schema:        em.schema,
		table:         em.table,
		selectColumns: em.allColumns,
		keyColumns:    em.parentalColumns,
		keys:          parentKeys,
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = rowSet.Close() }()

	return u.scanEntities(ctx, em, rowSet)
}

func (u *persistence) scanEntities(ctx context.Context, em *entityMapping, rowSet rows) ([]any, error) {
	entities := make([]any, 0)
	for rowSet.Next() {
		entityPtr := reflect.New(em.entityType).Interface()
		if err := u.scanEntity(em, entityPtr, rowSet, em.allFields); err != nil {
			return nil, err
		}
		entities = append(entities, entityPtr)
	}

	if len(entities) > 0 && len(em.childMap) > 0 {
		if err := u.loadChildren(ctx, em, entities); err != nil {
			return nil, err
		}
	}

	return entities, nil
}

func (u *persistence) loadChildren(ctx context.Context, em *entityMapping, parents []any) error {
	parentKeys := make([]Key, len(parents))
	parentKeyToParent := make(map[Key]any, len(parents))
	for i, parent := range parents {
		parentKeys[i] = em.extractKey(parent, em.primaryKey)
		parentKeyToParent[parentKeys[i]] = parent
	}

	for _, childField := range em.childFields {
		child := em.childMap[childField]
		childMapping, err := u.registry.get(child.target)
		if err != nil {
			return err
		}

		childEntities, err := u.getByParentKeys(ctx, childMapping, parentKeys)
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
			parentalKey := childMapping.extractKey(childEntity, childMapping.parentalKey)
			if parent, ok := parentKeyToParent[parentalKey]; ok {
				if childGroups[parent] == nil {
					childGroups[parent] = make([]any, 0, estimatedCapacity)
				}
				childGroups[parent] = append(childGroups[parent], childEntity)
			}
		}

		for _, parent := range parents {
			child.set(parent, childGroups[parent])
		}
	}

	return nil
}

func (u *persistence) save(ctx context.Context, em *entityMapping, entities []any) ([]saveResult, error) {
	if len(entities) == 0 {
		return nil, nil
	}

	results, err := u.saveRows(ctx, em, entities)
	if err != nil {
		return nil, err
	}

	for _, childField := range em.childFields {
		child := em.childMap[childField]
		childMapping, err := u.registry.get(child.target)
		if err != nil {
			return nil, err
		}

		toSave := buildChildSaveSet(childMapping, child, results)
		if len(toSave) > 0 {
			if _, err := u.save(ctx, childMapping, toSave); err != nil {
				return nil, err
			}
		}

		snapshot := buildRelationSnapshot(childMapping, child, results)
		if err := u.deleteMissingChildren(ctx, snapshot); err != nil {
			return nil, err
		}
	}

	return results, nil
}

func buildChildSaveSet(childMapping *entityMapping, relation *child, parents []saveResult) []any {
	toSave := make([]any, 0)
	for _, result := range parents {
		childEntities := relation.get(result.entity)
		for _, childEntity := range childEntities {
			injectParentKey(childMapping, childEntity, result.key)
		}
		toSave = append(toSave, childEntities...)
	}
	return toSave
}

func buildRelationSnapshot(childMapping *entityMapping, relation *child, parents []saveResult) relationSnapshot {
	snapshot := relationSnapshot{
		childMapping: childMapping,
		parentKeys:   make([]Key, 0, len(parents)),
		keepPairs:    make([]keepPair, 0),
	}

	for _, result := range parents {
		if result.inserted {
			continue
		}
		snapshot.parentKeys = append(snapshot.parentKeys, result.key)
		for _, childEntity := range relation.get(result.entity) {
			snapshot.keepPairs = append(snapshot.keepPairs, keepPair{
				parentKey: result.key,
				childKey:  childMapping.extractKey(childEntity, childMapping.primaryKey),
			})
		}
	}

	return snapshot
}

func injectParentKey(childMapping *entityMapping, childEntity any, parentKey Key) {
	for i, fieldName := range childMapping.parentalKey {
		childMapping.fieldMap[fieldName].setValue(childEntity, parentKey.At(i))
	}
}

func (u *persistence) deleteMissingChildren(ctx context.Context, snapshot relationSnapshot) error {
	op := selectMissingChildrenOp{
		schema:           snapshot.childMapping.schema,
		table:            snapshot.childMapping.table,
		parentKeyColumns: snapshot.childMapping.parentalColumns,
		childKeyColumns:  snapshot.childMapping.primaryColumns,
		parentKeys:       snapshot.parentKeys,
		keepPairs:        snapshot.keepPairs,
	}

	toDelete, err := u.backend.SelectMissingChildren(ctx, op)
	if err != nil {
		return err
	}
	return u.deleteByKeys(ctx, snapshot.childMapping, toDelete)
}

func (u *persistence) saveRows(ctx context.Context, em *entityMapping, entities []any) ([]saveResult, error) {
	saveFields := em.saveFields
	saveOp := saveRowsOp{
		schema:    em.schema,
		table:     em.table,
		fields:    em.saveFieldSet,
		returning: em.insertReturningColumns,
	}
	generatedInserts := make([]plannedRow, 0)
	manualCandidates := make([]plannedRow, 0)
	generatedUpdates := make([]plannedRow, 0)

	for i, entity := range entities {
		row := make([]any, len(saveFields))
		for j, fieldName := range saveFields {
			row[j] = em.fieldMap[fieldName].getValue(entity)
		}
		planned := plannedRow{index: i, row: saveRow{values: row}}
		intent, err := saveOp.classifyRow(planned.row)
		if err != nil {
			return nil, err
		}
		switch intent {
		case saveRowGeneratedInsert:
			generatedInserts = append(generatedInserts, planned)
		case saveRowGeneratedUpdate:
			generatedUpdates = append(generatedUpdates, planned)
		case saveRowManualKey:
			manualCandidates = append(manualCandidates, planned)
		}
	}

	insertRows := make([]plannedRow, 0, len(generatedInserts)+len(manualCandidates))
	insertRows = append(insertRows, generatedInserts...)
	updateRows := make([]plannedRow, 0, len(generatedUpdates)+len(manualCandidates))

	if len(manualCandidates) > 0 {
		existingKeys, err := u.selectExistingKeys(ctx, em, keysFromPlannedRows(saveOp, manualCandidates))
		if err != nil {
			return nil, err
		}
		existing := keySet(existingKeys)
		for _, row := range manualCandidates {
			if _, ok := existing[saveOp.keyFromRow(row.row)]; ok {
				updateRows = append(updateRows, row)
			} else {
				insertRows = append(insertRows, row)
			}
		}
	}

	if len(generatedUpdates) > 0 {
		existingKeys, err := u.selectExistingKeys(ctx, em, keysFromPlannedRows(saveOp, generatedUpdates))
		if err != nil {
			return nil, err
		}
		existing := keySet(existingKeys)
		for _, row := range generatedUpdates {
			if _, ok := existing[saveOp.keyFromRow(row.row)]; !ok {
				return nil, fmt.Errorf("%w: generated key %v does not exist in %s", ErrStaleEntity, saveOp.keyFromRow(row.row), em.entityType)
			}
			updateRows = append(updateRows, row)
		}
	}

	savedRows := make([]savedRow, 0, len(entities))
	inserted := make([]bool, len(entities))
	if len(insertRows) > 0 {
		insertedRows, err := u.backend.InsertRows(ctx, saveOp, insertRows)
		if err != nil {
			return nil, err
		}
		savedRows = append(savedRows, insertedRows...)
		for _, row := range insertRows {
			inserted[row.index] = true
		}
	}
	if len(updateRows) > 0 {
		updatedRows, err := u.backend.UpdateRows(ctx, saveOp, updateRows)
		if err != nil {
			return nil, err
		}
		savedRows = append(savedRows, updatedRows...)
	}

	result := make([]saveResult, len(entities))
	seen := make([]bool, len(entities))
	for _, saved := range savedRows {
		if saved.index < 0 || saved.index >= len(entities) {
			return nil, fmt.Errorf("returned row index %d out of range", saved.index)
		}
		if seen[saved.index] {
			return nil, fmt.Errorf("duplicate returned row index %d", saved.index)
		}
		seen[saved.index] = true

		if err := u.applyReturnedValues(em, entities[saved.index], em.insertReturning, saved.values); err != nil {
			return nil, err
		}
		result[saved.index] = saveResult{
			entity:   entities[saved.index],
			key:      em.extractKey(entities[saved.index], em.primaryKey),
			inserted: inserted[saved.index],
		}
	}

	for i, ok := range seen {
		if !ok {
			return nil, fmt.Errorf("missing returned row for entity index %d", i)
		}
	}

	return result, nil
}

func (u *persistence) selectExistingKeys(ctx context.Context, em *entityMapping, keys []Key) ([]Key, error) {
	keys = uniqueKeys(keys)
	if len(keys) == 0 {
		return nil, nil
	}
	return u.backend.SelectExistingKeys(ctx, keyScanOp{
		schema:     em.schema,
		table:      em.table,
		keyColumns: em.primaryColumns,
		keyTypes:   em.fieldTypes(em.primaryKey),
		keys:       keys,
	})
}

func (u *persistence) applyReturnedValues(em *entityMapping, entity any, fieldNames []string, values []any) error {
	if len(values) != len(fieldNames) {
		return fmt.Errorf("expected %d returned values, got %d", len(fieldNames), len(values))
	}
	for i, name := range fieldNames {
		em.fieldMap[name].setValue(entity, values[i])
	}
	return nil
}

func (u *persistence) deleteByKeys(ctx context.Context, em *entityMapping, keys []Key) error {
	keys = uniqueKeys(keys)
	if len(keys) == 0 {
		return nil
	}

	for _, childField := range em.childFields {
		child := em.childMap[childField]
		childMapping, err := u.registry.get(child.target)
		if err != nil {
			return err
		}

		childKeys, err := u.loadKeysByParentKeys(ctx, childMapping, keys)
		if err != nil {
			return err
		}
		if err := u.deleteByKeys(ctx, childMapping, childKeys); err != nil {
			return err
		}
	}

	return u.backend.DeleteRowsByKeys(ctx, deleteRowsOp{
		schema:     em.schema,
		table:      em.table,
		keyColumns: em.primaryColumns,
		keys:       keys,
	})
}

func (u *persistence) loadKeysByParentKeys(ctx context.Context, em *entityMapping, parentKeys []Key) ([]Key, error) {
	parentKeys = uniqueKeys(parentKeys)
	if len(parentKeys) == 0 {
		return nil, nil
	}

	rowSet, err := u.backend.LoadByParentKeys(ctx, loadRowsOp{
		schema:        em.schema,
		table:         em.table,
		selectColumns: em.primaryColumns,
		keyColumns:    em.parentalColumns,
		keys:          parentKeys,
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = rowSet.Close() }()

	keys := make([]Key, 0)
	for rowSet.Next() {
		entityPtr := reflect.New(em.entityType).Interface()
		if err := u.scanEntity(em, entityPtr, rowSet, em.primaryKey); err != nil {
			return nil, err
		}
		keys = append(keys, em.extractKey(entityPtr, em.primaryKey))
	}

	return keys, nil
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
