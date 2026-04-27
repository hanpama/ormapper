package agg

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

func newPersistence(registry mappingRegistry, backend backend) *persistence {
	return &persistence{
		registry: registry,
		backend:  backend,
	}
}

// --- Load ---

func (u *persistence) getByKeys(ctx context.Context, em *entityMapping, ids []Key) ([]any, error) {
	return u.loadEntities(ctx, em, em.primaryKey, em.primaryPlan.columns, ids)
}

func (u *persistence) getByParentKeys(ctx context.Context, em *entityMapping, parentKeys []Key) ([]any, error) {
	return u.loadEntities(ctx, em, em.parentalKey, em.parentalPlan.columns, parentKeys)
}

func (u *persistence) loadEntities(ctx context.Context, em *entityMapping, keyNames []string, keyColumns []string, keys []Key) ([]any, error) {
	if len(keys) == 0 {
		return nil, nil
	}

	for _, key := range keys {
		if key.Length() != len(keyNames) {
			return nil, fmt.Errorf("key has %d values but %d key columns expected (entity %s, key columns: %v)", key.Length(), len(keyNames), em.entityType, keyNames)
		}
	}

	rowSet, err := u.backend.LoadRows(ctx, loadRowsOp{
		schema:        em.schema,
		table:         em.table,
		selectColumns: em.allPlan.columns,
		keyColumns:    keyColumns,
		keys:          keys,
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = rowSet.Close() }()

	entities, err := u.scanEntities(em, rowSet, 0)
	if err != nil {
		return nil, err
	}
	if len(entities) > 0 && len(em.childMap) > 0 {
		if err := u.loadChildren(ctx, em, entities); err != nil {
			return nil, err
		}
	}
	return entities, nil
}

func (u *persistence) scanEntity(entityPtr any, row rows, fields []*field) error {
	u.scanBuffer = u.scanBuffer[:0]
	if cap(u.scanBuffer) < len(fields) {
		u.scanBuffer = make([]any, 0, len(fields))
	}

	entityValue := reflect.ValueOf(entityPtr).Elem()
	for _, field := range fields {
		u.scanBuffer = append(u.scanBuffer, field.ptrFrom(entityValue))
	}

	return row.Scan(u.scanBuffer...)
}

func (u *persistence) scanEntities(em *entityMapping, rowSet rows, expectedCapacity int) ([]any, error) {
	entities := make([]any, 0, expectedCapacity)
	for rowSet.Next() {
		entityPtr := em.newEntity()
		if err := u.scanEntity(entityPtr, rowSet, em.allPlan.fields); err != nil {
			return nil, err
		}
		entities = append(entities, entityPtr)
	}
	return entities, nil
}

func (u *persistence) loadChildren(ctx context.Context, em *entityMapping, parents []any) error {
	parentKeys := make([]Key, len(parents))
	parentKeyToIndex := make(map[Key]int, len(parents))
	for i, parent := range parents {
		parentKeys[i] = em.extractPrimaryKey(parent)
		parentKeyToIndex[parentKeys[i]] = i
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

		parentIndexes := make([]int, len(childEntities))
		counts := make([]int, len(parents))
		for i, childEntity := range childEntities {
			parentIndexes[i] = -1
			parentalKey := childMapping.extractParentalKey(childEntity)
			if parentIndex, ok := parentKeyToIndex[parentalKey]; ok {
				parentIndexes[i] = parentIndex
				counts[parentIndex]++
			}
		}

		child.setByParentIndexes(parents, childEntities, parentIndexes, counts)
	}

	return nil
}

// --- Save ---

func (u *persistence) save(ctx context.Context, em *entityMapping, entities []any) error {
	if len(entities) == 0 {
		return nil
	}

	inserts, candidates, err := em.saveLayout.projectRows(entities)
	if err != nil {
		return err
	}

	toInsert := inserts
	var toUpdate []plannedRow

	if len(candidates) > 0 {
		candidateKeys := make([]Key, len(candidates))
		for i, row := range candidates {
			candidateKeys[i] = row.key
		}
		existingKeys, err := u.selectExistingKeys(ctx, em, candidateKeys)
		if err != nil {
			return err
		}
		existing := newKeySet(existingKeys)

		toUpdate = make([]plannedRow, 0, len(candidates))
		for _, row := range candidates {
			if _, ok := existing[row.key]; ok {
				toUpdate = append(toUpdate, row)
			} else if em.saveLayout.hasGeneratedKey() {
				return fmt.Errorf("%w: generated key %v does not exist in %s", ErrStaleEntity, row.key, em.entityType)
			} else {
				toInsert = append(toInsert, row)
			}
		}
	}

	return u.savePlannedLevel(ctx, em, entities, toInsert, toUpdate)
}

func (u *persistence) savePlannedLevel(ctx context.Context, em *entityMapping, entities []any, toInsert, toUpdate []plannedRow) error {
	inserted, err := u.savePlannedRows(ctx, em, entities, toInsert, toUpdate)
	if err != nil {
		return err
	}
	for _, childField := range em.childFields {
		child := em.childMap[childField]
		childMapping, err := u.registry.get(child.target)
		if err != nil {
			return err
		}
		if err := u.saveChildRelation(ctx, em, childMapping, child, entities, inserted); err != nil {
			return err
		}
	}
	return nil
}

func (u *persistence) saveChildRelation(ctx context.Context, parentMapping, childMapping *entityMapping, child *child, parentEntities []any, parentInserted []bool) error {
	// Collect submitted children and inject parental keys
	childCount := 0
	for _, entity := range parentEntities {
		childCount += child.count(entity)
	}

	submitted := make([]any, 0, childCount)
	keepKeys := make(map[Key]keySet)
	existingParentKeys := make([]Key, 0, len(parentEntities))

	for i, entity := range parentEntities {
		parentKey := parentMapping.extractPrimaryKey(entity)

		if !parentInserted[i] {
			existingParentKeys = append(existingParentKeys, parentKey)
		}

		start := len(submitted)
		submitted = child.appendTo(entity, submitted)
		for _, childEntity := range submitted[start:] {
			childMapping.injectParentalKey(childEntity, parentKey)

			childKey := childMapping.extractPrimaryKey(childEntity)
			keep := keepKeys[parentKey]
			if keep == nil {
				keep = make(keySet)
				keepKeys[parentKey] = keep
			}
			keep[childKey] = struct{}{}
		}
	}

	// Load existing relation state
	existingChildKeys, existingParentByChild, err := u.loadRelationKeys(ctx, childMapping, existingParentKeys)
	if err != nil {
		return err
	}

	// Project, resolve, and save
	if len(submitted) > 0 {
		inserts, candidates, err := childMapping.saveLayout.projectRows(submitted)
		if err != nil {
			return err
		}

		toInsert := inserts
		var toUpdate []plannedRow

		for _, row := range candidates {
			parentKey := childMapping.extractParentalKey(submitted[row.index])
			childKey := row.key

			if _, ok := existingChildKeys[parentKey][childKey]; ok {
				toUpdate = append(toUpdate, row)
			} else if childMapping.saveLayout.hasGeneratedKey() {
				return fmt.Errorf("%w: generated key %v does not exist in parent relation for %s", ErrStaleEntity, childKey, childMapping.entityType)
			} else {
				if owner, ok := existingParentByChild[childKey]; ok && owner != parentKey {
					return fmt.Errorf("%w: key %v already exists under a different parent in %s", ErrConsistency, childKey, childMapping.entityType)
				}
				toInsert = append(toInsert, row)
			}
		}

		if err := u.savePlannedLevel(ctx, childMapping, submitted, toInsert, toUpdate); err != nil {
			return err
		}
	}

	// Delete orphaned children
	var toDelete []Key
	for parentKey, children := range existingChildKeys {
		keepSet := keepKeys[parentKey]
		for childKey := range children {
			if _, ok := keepSet[childKey]; !ok {
				toDelete = append(toDelete, childKey)
			}
		}
	}
	return u.deleteByKeys(ctx, childMapping, toDelete)
}

func (u *persistence) loadRelationKeys(ctx context.Context, childMapping *entityMapping, parentKeys []Key) (map[Key]keySet, map[Key]Key, error) {
	childKeysByParent := make(map[Key]keySet)
	parentByChildKey := make(map[Key]Key)
	if len(parentKeys) == 0 {
		return childKeysByParent, parentByChildKey, nil
	}

	rowSet, err := u.backend.LoadRows(ctx, loadRowsOp{
		schema:        childMapping.schema,
		table:         childMapping.table,
		selectColumns: childMapping.relationColumns,
		keyColumns:    childMapping.parentalPlan.columns,
		keys:          parentKeys,
	})
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = rowSet.Close() }()

	parentScanned, childScanned, err := u.scanKeyPairs(rowSet, childMapping.parentalPlan.types, childMapping.primaryPlan.types)
	if err != nil {
		return nil, nil, err
	}

	for i := range parentScanned {
		pk := parentScanned[i]
		ck := childScanned[i]
		children := childKeysByParent[pk]
		if children == nil {
			children = make(keySet)
			childKeysByParent[pk] = children
		}
		children[ck] = struct{}{}
		parentByChildKey[ck] = pk
	}

	return childKeysByParent, parentByChildKey, nil
}

// --- Save Execution ---

func (u *persistence) savePlannedRows(ctx context.Context, em *entityMapping, entities []any, toInsert, toUpdate []plannedRow) ([]bool, error) {
	inserted := make([]bool, len(entities))

	if len(toInsert) > 0 {
		rowSet, err := u.backend.InsertRows(ctx, em.saveLayout.newInsertOp(em.schema, em.table, toInsert))
		if err != nil {
			return nil, err
		}
		n, err := u.scanReturning(em, entities, toInsert, rowSet)
		if err != nil {
			return nil, err
		}
		if n != len(toInsert) {
			return nil, fmt.Errorf("%w: expected %d inserted rows, got %d", ErrConsistency, len(toInsert), n)
		}
		for _, row := range toInsert {
			inserted[row.index] = true
		}
	}

	if len(toUpdate) > 0 {
		rowSet, err := u.backend.UpdateRows(ctx, em.saveLayout.newUpdateOp(em.schema, em.table, toUpdate))
		if err != nil {
			return nil, err
		}
		n, err := u.scanReturning(em, entities, toUpdate, rowSet)
		if err != nil {
			return nil, err
		}
		if n != len(toUpdate) {
			return nil, fmt.Errorf("%w: expected %d updated rows, got %d", ErrStaleEntity, len(toUpdate), n)
		}
	}

	return inserted, nil
}

func (u *persistence) scanReturning(em *entityMapping, entities []any, planned []plannedRow, rowSet rows) (int, error) {
	defer func() { _ = rowSet.Close() }()

	count := 0
	for rowSet.Next() {
		if count >= len(planned) {
			return count + 1, nil
		}
		if err := u.scanEntity(entities[planned[count].index], rowSet, em.saveLayout.returningFields); err != nil {
			return 0, err
		}
		count++
	}
	return count, nil
}

func (u *persistence) selectExistingKeys(ctx context.Context, em *entityMapping, keys []Key) ([]Key, error) {
	if len(keys) == 0 {
		return nil, nil
	}
	rowSet, err := u.backend.LoadRows(ctx, loadRowsOp{
		schema:        em.schema,
		table:         em.table,
		selectColumns: em.primaryPlan.columns,
		keyColumns:    em.primaryPlan.columns,
		keys:          keys,
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = rowSet.Close() }()
	return u.scanTypedKeys(rowSet, em.primaryPlan.types)
}

// --- Delete ---

func (u *persistence) deleteByKeys(ctx context.Context, em *entityMapping, keys []Key) error {
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
		keyColumns: em.primaryPlan.columns,
		keys:       keys,
	})
}

func (u *persistence) loadKeysByParentKeys(ctx context.Context, em *entityMapping, parentKeys []Key) ([]Key, error) {
	if len(parentKeys) == 0 {
		return nil, nil
	}

	rowSet, err := u.backend.LoadRows(ctx, loadRowsOp{
		schema:        em.schema,
		table:         em.table,
		selectColumns: em.primaryPlan.columns,
		keyColumns:    em.parentalPlan.columns,
		keys:          parentKeys,
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = rowSet.Close() }()

	return u.scanTypedKeys(rowSet, em.primaryPlan.types)
}

func (u *persistence) scanTypedKeys(rowSet rows, keyTypes []reflect.Type) ([]Key, error) {
	if len(keyTypes) == 0 {
		return nil, nil
	}
	keys := make([]Key, 0)
	values := make([]any, len(keyTypes))
	dest := make([]any, len(keyTypes))
	for i := range values {
		dest[i] = &values[i]
	}
	for rowSet.Next() {
		if err := rowSet.Scan(dest...); err != nil {
			return nil, err
		}
		for i, value := range values {
			values[i] = coerceScanned(value, keyTypes[i])
		}
		keys = append(keys, NewKey(values...))
	}
	return keys, nil
}

func (u *persistence) scanKeyPairs(rowSet rows, leftTypes, rightTypes []reflect.Type) ([]Key, []Key, error) {
	leftLen := len(leftTypes)
	values := make([]any, leftLen+len(rightTypes))
	dest := make([]any, len(values))
	for i := range values {
		dest[i] = &values[i]
	}
	var leftKeys, rightKeys []Key
	for rowSet.Next() {
		if err := rowSet.Scan(dest...); err != nil {
			return nil, nil, err
		}
		for i, value := range values[:leftLen] {
			values[i] = coerceScanned(value, leftTypes[i])
		}
		for i, value := range values[leftLen:] {
			values[leftLen+i] = coerceScanned(value, rightTypes[i])
		}
		leftKeys = append(leftKeys, NewKey(values[:leftLen]...))
		rightKeys = append(rightKeys, NewKey(values[leftLen:]...))
	}
	return leftKeys, rightKeys, nil
}

func coerceScanned(value any, target reflect.Type) any {
	if b, ok := value.([]byte); ok {
		value = string(b)
	}
	if value == nil {
		return nil
	}
	v := reflect.ValueOf(value)
	if v.Type().AssignableTo(target) {
		return value
	}
	if v.Type().ConvertibleTo(target) {
		return v.Convert(target).Interface()
	}
	return value
}
