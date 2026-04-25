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

type saveRowIntent int

const (
	saveRowManualKey saveRowIntent = iota
	saveRowGeneratedInsert
	saveRowGeneratedUpdate
)

func newPersistence(registry mappingRegistry, backend backend) *persistence {
	return &persistence{
		registry: registry,
		backend:  backend,
	}
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

func (u *persistence) getByKeys(ctx context.Context, em *entityMapping, ids []Key) ([]any, error) {
	if len(ids) == 0 {
		return nil, nil
	}

	for _, id := range ids {
		if id.Length() != len(em.primaryKey) {
			return nil, fmt.Errorf("key has %d values but %d key columns expected (entity %s, key columns: %v)", id.Length(), len(em.primaryKey), em.entityType, em.primaryKey)
		}
	}

	rowSet, err := u.backend.LoadRows(ctx, loadRowsOp{
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

func (u *persistence) getByParentKeys(ctx context.Context, em *entityMapping, parentKeys []Key) ([]any, error) {
	if len(parentKeys) == 0 {
		return nil, nil
	}

	for _, key := range parentKeys {
		if key.Length() != len(em.parentalKey) {
			return nil, fmt.Errorf("key has %d values but %d parental key columns expected (entity %s, key columns: %v)", key.Length(), len(em.parentalKey), em.entityType, em.parentalKey)
		}
	}

	rowSet, err := u.backend.LoadRows(ctx, loadRowsOp{
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

func (u *persistence) save(ctx context.Context, em *entityMapping, entities []any) ([]saveResult, error) {
	if len(entities) == 0 {
		return nil, nil
	}

	plannedRows, intents, err := projectSaveRows(em, entities)
	if err != nil {
		return nil, err
	}

	generatedInserts := make([]plannedRow, 0)
	manualCandidates := make([]plannedRow, 0)
	generatedUpdates := make([]plannedRow, 0)
	toInsert := make([]plannedRow, 0, len(entities))
	toUpdate := make([]plannedRow, 0, len(entities))
	for i, row := range plannedRows {
		switch intents[i] {
		case saveRowGeneratedInsert:
			generatedInserts = append(generatedInserts, row)
		case saveRowGeneratedUpdate:
			generatedUpdates = append(generatedUpdates, row)
		case saveRowManualKey:
			manualCandidates = append(manualCandidates, row)
		}
	}
	toInsert = append(toInsert, generatedInserts...)

	if len(manualCandidates) > 0 {
		existingKeys, err := u.selectExistingKeys(ctx, em, keysFromPlannedRows(manualCandidates))
		if err != nil {
			return nil, err
		}
		existing := keySet(existingKeys)
		for _, row := range manualCandidates {
			if _, ok := existing[row.key]; ok {
				toUpdate = append(toUpdate, row)
			} else {
				toInsert = append(toInsert, row)
			}
		}
	}

	if len(generatedUpdates) > 0 {
		existingKeys, err := u.selectExistingKeys(ctx, em, keysFromPlannedRows(generatedUpdates))
		if err != nil {
			return nil, err
		}
		existing := keySet(existingKeys)
		for _, row := range generatedUpdates {
			if _, ok := existing[row.key]; !ok {
				return nil, fmt.Errorf("%w: generated key %v does not exist in %s", ErrStaleEntity, row.key, em.entityType)
			}
			toUpdate = append(toUpdate, row)
		}
	}

	return u.savePlannedLevel(ctx, em, entities, toInsert, toUpdate)
}

func (u *persistence) savePlannedLevel(ctx context.Context, em *entityMapping, entities []any, toInsert, toUpdate []plannedRow) ([]saveResult, error) {
	results, err := u.savePlannedRows(ctx, em, entities, toInsert, toUpdate)
	if err != nil {
		return nil, err
	}

	for _, childField := range em.childFields {
		child := em.childMap[childField]
		childMapping, err := u.registry.get(child.target)
		if err != nil {
			return nil, err
		}

		childCount := 0
		for _, result := range results {
			childCount += child.count(result.entity)
		}

		submittedChildren := make([]any, 0, childCount)
		keepChildKeysByParent := make(map[Key]map[Key]struct{})
		existingParentKeys := make([]Key, 0, len(results))
		for _, result := range results {
			if !result.inserted {
				existingParentKeys = append(existingParentKeys, result.key)
			}

			start := len(submittedChildren)
			submittedChildren = child.appendTo(result.entity, submittedChildren)
			for _, childEntity := range submittedChildren[start:] {
				entityValue := reflect.ValueOf(childEntity).Elem()
				for i, field := range childMapping.parentalPlan.fields {
					field.setOn(entityValue, result.key.At(i))
				}

				childKey := childMapping.extractPrimaryKey(childEntity)
				keep := keepChildKeysByParent[result.key]
				if keep == nil {
					keep = make(map[Key]struct{})
					keepChildKeysByParent[result.key] = keep
				}
				keep[childKey] = struct{}{}
			}
		}

		existingChildKeysByParent := make(map[Key]map[Key]struct{})
		existingParentByChild := make(map[Key]Key)
		existingParentKeys = uniqueKeys(existingParentKeys)
		if len(existingParentKeys) > 0 {
			selectColumns := make([]string, 0, len(childMapping.parentalColumns)+len(childMapping.primaryColumns))
			selectColumns = append(selectColumns, childMapping.parentalColumns...)
			selectColumns = append(selectColumns, childMapping.primaryColumns...)

			rowSet, err := u.backend.LoadRows(ctx, loadRowsOp{
				schema:        childMapping.schema,
				table:         childMapping.table,
				selectColumns: selectColumns,
				keyColumns:    childMapping.parentalColumns,
				keys:          existingParentKeys,
			})
			if err != nil {
				return nil, err
			}

			parentTypes := childMapping.parentalPlan.types
			childTypes := childMapping.primaryPlan.types
			parentValues := make([]any, len(parentTypes))
			childValues := make([]any, len(childTypes))
			dest := make([]any, 0, len(parentValues)+len(childValues))
			for i := range parentValues {
				dest = append(dest, &parentValues[i])
			}
			for i := range childValues {
				dest = append(dest, &childValues[i])
			}

			for rowSet.Next() {
				if err := rowSet.Scan(dest...); err != nil {
					_ = rowSet.Close()
					return nil, err
				}
				for i, value := range parentValues {
					parentValues[i] = coerceScannedValue(normalizeScannedValue(value), parentTypes[i])
				}
				for i, value := range childValues {
					childValues[i] = coerceScannedValue(normalizeScannedValue(value), childTypes[i])
				}

				parentKey := NewKey(parentValues...)
				childKey := NewKey(childValues...)
				existingChildren := existingChildKeysByParent[parentKey]
				if existingChildren == nil {
					existingChildren = make(map[Key]struct{})
					existingChildKeysByParent[parentKey] = existingChildren
				}
				existingChildren[childKey] = struct{}{}
				existingParentByChild[childKey] = parentKey
			}
			if err := rowSet.Close(); err != nil {
				return nil, err
			}
		}

		if len(submittedChildren) > 0 {
			childRows, childIntents, err := projectSaveRows(childMapping, submittedChildren)
			if err != nil {
				return nil, err
			}

			childGeneratedInserts := make([]plannedRow, 0)
			childToInsert := make([]plannedRow, 0, len(submittedChildren))
			childToUpdate := make([]plannedRow, 0, len(submittedChildren))
			for i, row := range childRows {
				entity := submittedChildren[row.index]
				parentKey := childMapping.extractParentalKey(entity)
				childKey := row.key
				existingChildren := existingChildKeysByParent[parentKey]

				switch childIntents[i] {
				case saveRowGeneratedInsert:
					childGeneratedInserts = append(childGeneratedInserts, row)
				case saveRowGeneratedUpdate:
					if _, ok := existingChildren[childKey]; !ok {
						return nil, fmt.Errorf("%w: generated key %v does not exist in parent relation for %s", ErrStaleEntity, childKey, childMapping.entityType)
					}
					childToUpdate = append(childToUpdate, row)
				case saveRowManualKey:
					if _, ok := existingChildren[childKey]; ok {
						childToUpdate = append(childToUpdate, row)
					} else {
						if owner, ok := existingParentByChild[childKey]; ok && owner != parentKey {
							return nil, fmt.Errorf("%w: key %v already exists under a different parent in %s", ErrConsistency, childKey, childMapping.entityType)
						}
						childToInsert = append(childToInsert, row)
					}
				}
			}
			childToInsert = append(childToInsert, childGeneratedInserts...)

			if _, err := u.savePlannedLevel(ctx, childMapping, submittedChildren, childToInsert, childToUpdate); err != nil {
				return nil, err
			}
		}

		toDelete := make([]Key, 0)
		for parentKey, existingChildren := range existingChildKeysByParent {
			keep := keepChildKeysByParent[parentKey]
			for childKey := range existingChildren {
				if _, ok := keep[childKey]; !ok {
					toDelete = append(toDelete, childKey)
				}
			}
		}

		if err := u.deleteByKeys(ctx, childMapping, toDelete); err != nil {
			return nil, err
		}
	}

	return results, nil
}

func keysFromPlannedRows(rows []plannedRow) []Key {
	keys := make([]Key, len(rows))
	for i, row := range rows {
		keys[i] = row.key
	}
	return keys
}

func keySet(keys []Key) map[Key]struct{} {
	result := make(map[Key]struct{}, len(keys))
	for _, key := range keys {
		result[key] = struct{}{}
	}
	return result
}

func projectSaveRows(em *entityMapping, entities []any) ([]plannedRow, []saveRowIntent, error) {
	layout := &em.saveLayout.rows
	rows := make([]plannedRow, len(entities))
	intents := make([]saveRowIntent, len(entities))
	submittedKeys := make(map[Key]int, len(entities))

	for i, entity := range entities {
		row := make([]any, len(em.saveLayout.rowFields))
		entityValue := reflect.ValueOf(entity).Elem()
		for j, field := range em.saveLayout.rowFields {
			row[j] = field.valueFrom(entityValue)
		}
		saveRow := saveRow{values: row}
		var keyValues [9]any
		if len(layout.primaryIndexes) > len(keyValues) {
			panic("ormapper: Key supports up to 9 column values")
		}
		for j, idx := range layout.primaryIndexes {
			keyValues[j] = saveRow.values[idx]
		}
		planned := plannedRow{
			index: i,
			key:   newKeyFromValues(keyValues[:len(layout.primaryIndexes)]),
			row:   saveRow,
		}

		intent := saveRowManualKey
		generatedIndexes := layout.generatedPrimaryIndexes
		if len(generatedIndexes) > 0 {
			zeroGeneratedFields := 0
			for _, idx := range generatedIndexes {
				value := planned.row.values[idx]
				if value == nil {
					zeroGeneratedFields++
					continue
				}
				v := reflect.ValueOf(value)
				if !v.IsValid() || v.IsZero() {
					zeroGeneratedFields++
				}
			}
			switch zeroGeneratedFields {
			case len(generatedIndexes):
				intent = saveRowGeneratedInsert
			case 0:
				intent = saveRowGeneratedUpdate
			default:
				return nil, nil, fmt.Errorf("%w: generated primary key fields must be all zero or all non-zero", ErrUnsupportedSemantic)
			}
		}
		if intent != saveRowGeneratedInsert {
			if previous, ok := submittedKeys[planned.key]; ok {
				return nil, nil, fmt.Errorf("%w: duplicate submitted key %v at entity indexes %d and %d", ErrConsistency, planned.key, previous, i)
			}
			submittedKeys[planned.key] = i
		}
		rows[i] = planned
		intents[i] = intent
	}

	return rows, intents, nil
}

func (u *persistence) savePlannedRows(ctx context.Context, em *entityMapping, entities []any, toInsert, toUpdate []plannedRow) ([]saveResult, error) {
	saveOp := saveRowsOp{
		schema: em.schema,
		table:  em.table,
		layout: &em.saveLayout.rows,
	}

	savedRows := make([]savedRow, 0, len(entities))
	inserted := make([]bool, len(entities))
	if len(toInsert) > 0 {
		insertedRows, err := u.backend.InsertRows(ctx, saveOp, toInsert)
		if err != nil {
			return nil, err
		}
		if len(insertedRows) != len(toInsert) {
			return nil, fmt.Errorf("%w: expected %d inserted rows, got %d", ErrConsistency, len(toInsert), len(insertedRows))
		}
		savedRows = append(savedRows, insertedRows...)
		for _, row := range toInsert {
			inserted[row.index] = true
		}
	}
	if len(toUpdate) > 0 {
		updatedRows, err := u.backend.UpdateRows(ctx, saveOp, toUpdate)
		if err != nil {
			return nil, err
		}
		if len(updatedRows) != len(toUpdate) {
			return nil, fmt.Errorf("%w: expected %d updated rows, got %d", ErrStaleEntity, len(toUpdate), len(updatedRows))
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

		if err := u.applyReturnedValues(entities[saved.index], em.saveLayout.returningFields, saved.values); err != nil {
			return nil, err
		}
		result[saved.index] = saveResult{
			entity:   entities[saved.index],
			key:      em.extractPrimaryKey(entities[saved.index]),
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
		keyTypes:   em.primaryPlan.types,
		keys:       keys,
	})
}

func (u *persistence) applyReturnedValues(entity any, fields []*field, values []any) error {
	if len(values) != len(fields) {
		return fmt.Errorf("expected %d returned values, got %d", len(fields), len(values))
	}
	entityValue := reflect.ValueOf(entity).Elem()
	for i, field := range fields {
		field.setOn(entityValue, values[i])
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

	rowSet, err := u.backend.LoadRows(ctx, loadRowsOp{
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

	return scanTypedKeys(rowSet, em.primaryPlan.types)
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
