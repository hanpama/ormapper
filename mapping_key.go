package ormapper

func (em *entityMapping) extractKey(entity any, fieldNames []string) Key {
	fieldMap := em.fieldMap

	switch len(fieldNames) {
	case 0:
		return new0()
	case 1:
		v0 := fieldMap[fieldNames[0]].getValue(entity)
		return new1(v0)
	case 2:
		v0 := fieldMap[fieldNames[0]].getValue(entity)
		v1 := fieldMap[fieldNames[1]].getValue(entity)
		return new2(v0, v1)
	case 3:
		v0 := fieldMap[fieldNames[0]].getValue(entity)
		v1 := fieldMap[fieldNames[1]].getValue(entity)
		v2 := fieldMap[fieldNames[2]].getValue(entity)
		return new3(v0, v1, v2)
	case 4:
		v0 := fieldMap[fieldNames[0]].getValue(entity)
		v1 := fieldMap[fieldNames[1]].getValue(entity)
		v2 := fieldMap[fieldNames[2]].getValue(entity)
		v3 := fieldMap[fieldNames[3]].getValue(entity)
		return new4(v0, v1, v2, v3)
	case 5:
		v0 := fieldMap[fieldNames[0]].getValue(entity)
		v1 := fieldMap[fieldNames[1]].getValue(entity)
		v2 := fieldMap[fieldNames[2]].getValue(entity)
		v3 := fieldMap[fieldNames[3]].getValue(entity)
		v4 := fieldMap[fieldNames[4]].getValue(entity)
		return new5(v0, v1, v2, v3, v4)
	case 6:
		v0 := fieldMap[fieldNames[0]].getValue(entity)
		v1 := fieldMap[fieldNames[1]].getValue(entity)
		v2 := fieldMap[fieldNames[2]].getValue(entity)
		v3 := fieldMap[fieldNames[3]].getValue(entity)
		v4 := fieldMap[fieldNames[4]].getValue(entity)
		v5 := fieldMap[fieldNames[5]].getValue(entity)
		return new6(v0, v1, v2, v3, v4, v5)
	case 7:
		v0 := fieldMap[fieldNames[0]].getValue(entity)
		v1 := fieldMap[fieldNames[1]].getValue(entity)
		v2 := fieldMap[fieldNames[2]].getValue(entity)
		v3 := fieldMap[fieldNames[3]].getValue(entity)
		v4 := fieldMap[fieldNames[4]].getValue(entity)
		v5 := fieldMap[fieldNames[5]].getValue(entity)
		v6 := fieldMap[fieldNames[6]].getValue(entity)
		return new7(v0, v1, v2, v3, v4, v5, v6)
	case 8:
		v0 := fieldMap[fieldNames[0]].getValue(entity)
		v1 := fieldMap[fieldNames[1]].getValue(entity)
		v2 := fieldMap[fieldNames[2]].getValue(entity)
		v3 := fieldMap[fieldNames[3]].getValue(entity)
		v4 := fieldMap[fieldNames[4]].getValue(entity)
		v5 := fieldMap[fieldNames[5]].getValue(entity)
		v6 := fieldMap[fieldNames[6]].getValue(entity)
		v7 := fieldMap[fieldNames[7]].getValue(entity)
		return new8(v0, v1, v2, v3, v4, v5, v6, v7)
	case 9:
		v0 := fieldMap[fieldNames[0]].getValue(entity)
		v1 := fieldMap[fieldNames[1]].getValue(entity)
		v2 := fieldMap[fieldNames[2]].getValue(entity)
		v3 := fieldMap[fieldNames[3]].getValue(entity)
		v4 := fieldMap[fieldNames[4]].getValue(entity)
		v5 := fieldMap[fieldNames[5]].getValue(entity)
		v6 := fieldMap[fieldNames[6]].getValue(entity)
		v7 := fieldMap[fieldNames[7]].getValue(entity)
		v8 := fieldMap[fieldNames[8]].getValue(entity)
		return new9(v0, v1, v2, v3, v4, v5, v6, v7, v8)
	default:
		panic("ormapper: Key supports up to 9 column values")
	}
}
