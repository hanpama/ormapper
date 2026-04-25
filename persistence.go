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

type childRelationSnapshot struct {
	byParent      map[Key]map[Key]struct{}
	parentByChild map[Key]Key
}

type keepPair struct {
	parentKey Key
	childKey  Key
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

	return u.scanEntities(ctx, em, rowSet, 0)
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

	return u.scanEntities(ctx, em, rowSet, 0)
}

func (u *persistence) scanEntities(ctx context.Context, em *entityMapping, rowSet rows, expectedCapacity int) ([]any, error) {
	entities := make([]any, 0, expectedCapacity)
	for rowSet.Next() {
		entityPtr := em.newEntity()
		if err := u.scanEntity(entityPtr, rowSet, em.allPlan.fields); err != nil {
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
	return u.saveInRelation(ctx, em, entities, nil)
}

func (u *persistence) saveInRelation(ctx context.Context, em *entityMapping, entities []any, relation *childRelationSnapshot) ([]saveResult, error) {
	if len(entities) == 0 {
		return nil, nil
	}

	results, err := u.saveRows(ctx, em, entities, relation)
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
		snapshot, err := u.loadChildRelationSnapshot(ctx, childMapping, existingParentKeys(results))
		if err != nil {
			return nil, err
		}
		if len(toSave) > 0 {
			if _, err := u.saveInRelation(ctx, childMapping, toSave, snapshot); err != nil {
				return nil, err
			}
		}

		if err := u.deleteByKeys(ctx, childMapping, snapshot.missing(buildKeepPairs(childMapping, child, results))); err != nil {
			return nil, err
		}
	}

	return results, nil
}

func buildChildSaveSet(childMapping *entityMapping, relation *child, parents []saveResult) []any {
	childCount := 0
	for _, result := range parents {
		childCount += relation.count(result.entity)
	}

	toSave := make([]any, 0, childCount)
	for _, result := range parents {
		start := len(toSave)
		toSave = relation.appendTo(result.entity, toSave)
		for _, childEntity := range toSave[start:] {
			injectParentKey(childMapping, childEntity, result.key)
		}
	}
	return toSave
}

func existingParentKeys(parents []saveResult) []Key {
	keys := make([]Key, 0, len(parents))
	for _, result := range parents {
		if result.inserted {
			continue
		}
		keys = append(keys, result.key)
	}
	return keys
}

func keysFromPlannedRows(op saveRowsOp, rows []plannedRow) []Key {
	keys := make([]Key, len(rows))
	for i, row := range rows {
		keys[i] = primaryKeyFromRow(op.layout, row.row)
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

func classifySaveRow(layout *saveRowsLayout, row saveRow) (saveRowIntent, error) {
	generatedIndexes := layout.generatedPrimaryIndexes
	if len(generatedIndexes) == 0 {
		return saveRowManualKey, nil
	}

	zeroCount := 0
	for _, idx := range generatedIndexes {
		if valueIsZero(row.values[idx]) {
			zeroCount++
		}
	}
	switch zeroCount {
	case len(generatedIndexes):
		return saveRowGeneratedInsert, nil
	case 0:
		return saveRowGeneratedUpdate, nil
	default:
		return 0, fmt.Errorf("%w: generated primary key fields must be all zero or all non-zero", ErrUnsupportedSemantic)
	}
}

func valueIsZero(value any) bool {
	if value == nil {
		return true
	}
	v := reflect.ValueOf(value)
	return !v.IsValid() || v.IsZero()
}

func buildKeepPairs(childMapping *entityMapping, relation *child, parents []saveResult) []keepPair {
	childCount := 0
	for _, result := range parents {
		childCount += relation.count(result.entity)
	}

	keepPairs := make([]keepPair, 0, childCount)
	childEntities := make([]any, 0)
	for _, result := range parents {
		childEntities = relation.appendTo(result.entity, childEntities[:0])
		for _, childEntity := range childEntities {
			keepPairs = append(keepPairs, keepPair{
				parentKey: result.key,
				childKey:  childMapping.extractPrimaryKey(childEntity),
			})
		}
	}
	return keepPairs
}

func injectParentKey(childMapping *entityMapping, childEntity any, parentKey Key) {
	entityValue := reflect.ValueOf(childEntity).Elem()
	for i, field := range childMapping.parentalPlan.fields {
		field.setOn(entityValue, parentKey.At(i))
	}
}

func newChildRelationSnapshot() *childRelationSnapshot {
	return &childRelationSnapshot{
		byParent:      make(map[Key]map[Key]struct{}),
		parentByChild: make(map[Key]Key),
	}
}

func (s *childRelationSnapshot) add(parentKey, childKey Key) {
	children := s.byParent[parentKey]
	if children == nil {
		children = make(map[Key]struct{})
		s.byParent[parentKey] = children
	}
	children[childKey] = struct{}{}
	s.parentByChild[childKey] = parentKey
}

func (s *childRelationSnapshot) contains(parentKey, childKey Key) bool {
	children := s.byParent[parentKey]
	if children == nil {
		return false
	}
	_, ok := children[childKey]
	return ok
}

func (s *childRelationSnapshot) parentOf(childKey Key) (Key, bool) {
	parentKey, ok := s.parentByChild[childKey]
	return parentKey, ok
}

func (s *childRelationSnapshot) missing(keepPairs []keepPair) []Key {
	keepByParent := make(map[Key]map[Key]struct{})
	for _, pair := range keepPairs {
		children := keepByParent[pair.parentKey]
		if children == nil {
			children = make(map[Key]struct{})
			keepByParent[pair.parentKey] = children
		}
		children[pair.childKey] = struct{}{}
	}

	missing := make([]Key, 0)
	for parentKey, children := range s.byParent {
		keep := keepByParent[parentKey]
		for childKey := range children {
			if _, ok := keep[childKey]; !ok {
				missing = append(missing, childKey)
			}
		}
	}
	return missing
}

func (u *persistence) loadChildRelationSnapshot(ctx context.Context, em *entityMapping, parentKeys []Key) (*childRelationSnapshot, error) {
	snapshot := newChildRelationSnapshot()
	parentKeys = uniqueKeys(parentKeys)
	if len(parentKeys) == 0 {
		return snapshot, nil
	}

	selectColumns := make([]string, 0, len(em.parentalColumns)+len(em.primaryColumns))
	selectColumns = append(selectColumns, em.parentalColumns...)
	selectColumns = append(selectColumns, em.primaryColumns...)

	rowSet, err := u.backend.LoadRows(ctx, loadRowsOp{
		schema:        em.schema,
		table:         em.table,
		selectColumns: selectColumns,
		keyColumns:    em.parentalColumns,
		keys:          parentKeys,
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = rowSet.Close() }()

	parentTypes := em.parentalPlan.types
	childTypes := em.primaryPlan.types
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
			return nil, err
		}
		for i, value := range parentValues {
			parentValues[i] = coerceScannedValue(normalizeScannedValue(value), parentTypes[i])
		}
		for i, value := range childValues {
			childValues[i] = coerceScannedValue(normalizeScannedValue(value), childTypes[i])
		}
		snapshot.add(NewKey(parentValues...), NewKey(childValues...))
	}

	return snapshot, nil
}

func (u *persistence) saveRows(ctx context.Context, em *entityMapping, entities []any, relation *childRelationSnapshot) ([]saveResult, error) {
	saveOp := saveRowsOp{
		schema: em.schema,
		table:  em.table,
		layout: &em.saveLayout.rows,
	}
	generatedInserts := make([]plannedRow, 0)
	manualCandidates := make([]plannedRow, 0)
	generatedUpdates := make([]plannedRow, 0)
	insertRows := make([]plannedRow, 0, len(entities))
	updateRows := make([]plannedRow, 0, len(entities))
	submittedKeys := make(map[Key]int, len(entities))

	for i, entity := range entities {
		row := make([]any, len(em.saveLayout.rowFields))
		entityValue := reflect.ValueOf(entity).Elem()
		for j, field := range em.saveLayout.rowFields {
			row[j] = field.valueFrom(entityValue)
		}
		planned := plannedRow{index: i, row: saveRow{values: row}}
		intent, err := classifySaveRow(saveOp.layout, planned.row)
		if err != nil {
			return nil, err
		}
		if intent != saveRowGeneratedInsert {
			key := primaryKeyFromRow(saveOp.layout, planned.row)
			if previous, ok := submittedKeys[key]; ok {
				return nil, fmt.Errorf("%w: duplicate submitted key %v at entity indexes %d and %d", ErrConsistency, key, previous, i)
			}
			submittedKeys[key] = i
		}
		switch intent {
		case saveRowGeneratedInsert:
			generatedInserts = append(generatedInserts, planned)
		case saveRowGeneratedUpdate:
			if relation == nil {
				generatedUpdates = append(generatedUpdates, planned)
				continue
			}
			parentKey := em.extractParentalKey(entity)
			childKey := primaryKeyFromRow(saveOp.layout, planned.row)
			if !relation.contains(parentKey, childKey) {
				return nil, fmt.Errorf("%w: generated key %v does not exist in parent relation for %s", ErrStaleEntity, childKey, em.entityType)
			}
			updateRows = append(updateRows, planned)
		case saveRowManualKey:
			if relation == nil {
				manualCandidates = append(manualCandidates, planned)
				continue
			}
			parentKey := em.extractParentalKey(entity)
			childKey := primaryKeyFromRow(saveOp.layout, planned.row)
			if relation.contains(parentKey, childKey) {
				updateRows = append(updateRows, planned)
			} else {
				if owner, ok := relation.parentOf(childKey); ok && owner != parentKey {
					return nil, fmt.Errorf("%w: key %v already exists under a different parent in %s", ErrConsistency, childKey, em.entityType)
				}
				insertRows = append(insertRows, planned)
			}
		}
	}

	insertRows = append(insertRows, generatedInserts...)

	if len(manualCandidates) > 0 {
		existingKeys, err := u.selectExistingKeys(ctx, em, keysFromPlannedRows(saveOp, manualCandidates))
		if err != nil {
			return nil, err
		}
		existing := keySet(existingKeys)
		for _, row := range manualCandidates {
			if _, ok := existing[primaryKeyFromRow(saveOp.layout, row.row)]; ok {
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
			key := primaryKeyFromRow(saveOp.layout, row.row)
			if _, ok := existing[key]; !ok {
				return nil, fmt.Errorf("%w: generated key %v does not exist in %s", ErrStaleEntity, key, em.entityType)
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
