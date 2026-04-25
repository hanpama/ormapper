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

func newPersistence(registry mappingRegistry, backend backend) *persistence {
	return &persistence{
		registry: registry,
		backend:  backend,
	}
}

// --- Load ---

func (u *persistence) getByKeys(ctx context.Context, em *entityMapping, ids []Key) ([]any, error) {
	return u.loadEntities(ctx, em, em.primaryKey, em.primaryColumns, ids)
}

func (u *persistence) getByParentKeys(ctx context.Context, em *entityMapping, parentKeys []Key) ([]any, error) {
	return u.loadEntities(ctx, em, em.parentalKey, em.parentalColumns, parentKeys)
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
		selectColumns: em.allColumns,
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

	inserts, candidates, err := projectSaveRows(em, entities)
	if err != nil {
		return err
	}

	toInsert := inserts
	var toUpdate []plannedRow

	if len(candidates) > 0 {
		existingKeys, err := u.selectExistingKeys(ctx, em, keysFromPlannedRows(candidates))
		if err != nil {
			return err
		}
		existing := newKeySet(existingKeys)
		hasGeneratedKey := len(em.saveLayout.rows.generatedPrimaryIndexes) > 0

		toUpdate = make([]plannedRow, 0, len(candidates))
		for _, row := range candidates {
			if _, ok := existing[row.key]; ok {
				toUpdate = append(toUpdate, row)
			} else if hasGeneratedKey {
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
	submitted, keepKeys, existingParentKeys := collectSubmittedChildren(parentMapping, parentEntities, parentInserted, child, childMapping)

	existing, err := u.loadRelationState(ctx, childMapping, existingParentKeys)
	if err != nil {
		return err
	}

	if len(submitted) > 0 {
		inserts, candidates, err := projectSaveRows(childMapping, submitted)
		if err != nil {
			return err
		}

		toInsert := inserts
		hasGeneratedKey := len(childMapping.saveLayout.rows.generatedPrimaryIndexes) > 0
		var toUpdate []plannedRow

		for _, row := range candidates {
			parentKey := childMapping.extractParentalKey(submitted[row.index])
			childKey := row.key
			existingChildren := existing.childKeysByParent[parentKey]

			if _, ok := existingChildren[childKey]; ok {
				toUpdate = append(toUpdate, row)
			} else if hasGeneratedKey {
				return fmt.Errorf("%w: generated key %v does not exist in parent relation for %s", ErrStaleEntity, childKey, childMapping.entityType)
			} else {
				if owner, ok := existing.parentByChildKey[childKey]; ok && owner != parentKey {
					return fmt.Errorf("%w: key %v already exists under a different parent in %s", ErrConsistency, childKey, childMapping.entityType)
				}
				toInsert = append(toInsert, row)
			}
		}

		if err := u.savePlannedLevel(ctx, childMapping, submitted, toInsert, toUpdate); err != nil {
			return err
		}
	}

	return u.deleteByKeys(ctx, childMapping, existing.orphanedKeys(keepKeys))
}

func collectSubmittedChildren(parentMapping *entityMapping, parentEntities []any, parentInserted []bool, child *child, childMapping *entityMapping) (submitted []any, keepKeys map[Key]keySet, existingParentKeys []Key) {
	childCount := 0
	for _, entity := range parentEntities {
		childCount += child.count(entity)
	}

	submitted = make([]any, 0, childCount)
	keepKeys = make(map[Key]keySet)
	existingParentKeys = make([]Key, 0, len(parentEntities))

	for i, entity := range parentEntities {
		parentKey := parentMapping.extractPrimaryKey(entity)

		if !parentInserted[i] {
			existingParentKeys = append(existingParentKeys, parentKey)
		}

		start := len(submitted)
		submitted = child.appendTo(entity, submitted)
		for _, childEntity := range submitted[start:] {
			injectParentalKey(childEntity, childMapping, parentKey)

			childKey := childMapping.extractPrimaryKey(childEntity)
			keep := keepKeys[parentKey]
			if keep == nil {
				keep = make(keySet)
				keepKeys[parentKey] = keep
			}
			keep[childKey] = struct{}{}
		}
	}

	return
}

func injectParentalKey(childEntity any, childMapping *entityMapping, parentKey Key) {
	entityValue := reflect.ValueOf(childEntity).Elem()
	for i, field := range childMapping.parentalPlan.fields {
		field.setOn(entityValue, parentKey.At(i))
	}
}

func projectSaveRows(em *entityMapping, entities []any) (inserts, candidates []plannedRow, err error) {
	layout := &em.saveLayout.rows
	hasGeneratedKey := len(layout.generatedPrimaryIndexes) > 0
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

		isInsert := false
		if hasGeneratedKey {
			zeroGeneratedFields := 0
			for _, idx := range layout.generatedPrimaryIndexes {
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
			case len(layout.generatedPrimaryIndexes):
				isInsert = true
			case 0:
				// non-zero generated key — candidate for update
			default:
				return nil, nil, fmt.Errorf("%w: generated primary key fields must be all zero or all non-zero", ErrUnsupportedSemantic)
			}
		}

		if isInsert {
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

// --- Relation State ---

type relationState struct {
	childKeysByParent map[Key]keySet
	parentByChildKey  map[Key]Key
}

func (s *relationState) add(parentKey, childKey Key) {
	children := s.childKeysByParent[parentKey]
	if children == nil {
		children = make(keySet)
		s.childKeysByParent[parentKey] = children
	}
	children[childKey] = struct{}{}
	s.parentByChildKey[childKey] = parentKey
}

func (s relationState) orphanedKeys(keep map[Key]keySet) []Key {
	var orphaned []Key
	for parentKey, existingChildren := range s.childKeysByParent {
		keepSet := keep[parentKey]
		for childKey := range existingChildren {
			if _, ok := keepSet[childKey]; !ok {
				orphaned = append(orphaned, childKey)
			}
		}
	}
	return orphaned
}

func (u *persistence) loadRelationState(ctx context.Context, childMapping *entityMapping, parentKeys []Key) (relationState, error) {
	state := relationState{
		childKeysByParent: make(map[Key]keySet),
		parentByChildKey:  make(map[Key]Key),
	}
	if len(parentKeys) == 0 {
		return state, nil
	}

	selectColumns := make([]string, 0, len(childMapping.parentalColumns)+len(childMapping.primaryColumns))
	selectColumns = append(selectColumns, childMapping.parentalColumns...)
	selectColumns = append(selectColumns, childMapping.primaryColumns...)

	rowSet, err := u.backend.LoadRows(ctx, loadRowsOp{
		schema:        childMapping.schema,
		table:         childMapping.table,
		selectColumns: selectColumns,
		keyColumns:    childMapping.parentalColumns,
		keys:          parentKeys,
	})
	if err != nil {
		return state, err
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
			return state, err
		}
		for i, value := range parentValues {
			parentValues[i] = coerceScannedValue(normalizeScannedValue(value), parentTypes[i])
		}
		for i, value := range childValues {
			childValues[i] = coerceScannedValue(normalizeScannedValue(value), childTypes[i])
		}
		state.add(NewKey(parentValues...), NewKey(childValues...))
	}
	if err := rowSet.Close(); err != nil {
		return state, err
	}

	return state, nil
}

// --- Save Execution ---

func (u *persistence) savePlannedRows(ctx context.Context, em *entityMapping, entities []any, toInsert, toUpdate []plannedRow) ([]bool, error) {
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
	}

	for i, ok := range seen {
		if !ok {
			return nil, fmt.Errorf("missing returned row for entity index %d", i)
		}
	}

	return inserted, nil
}

func (u *persistence) selectExistingKeys(ctx context.Context, em *entityMapping, keys []Key) ([]Key, error) {
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
		keyColumns: em.primaryColumns,
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

// --- Helpers ---

func keysFromPlannedRows(rows []plannedRow) []Key {
	keys := make([]Key, len(rows))
	for i, row := range rows {
		keys[i] = row.key
	}
	return keys
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
