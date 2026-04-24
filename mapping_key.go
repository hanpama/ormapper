package ormapper

func (em *entityMapping) ExtractKey(entity any, fieldNames []string) Key {
	fieldMap := em.FieldMap

	switch len(fieldNames) {
	case 0:
		return new0()
	case 1:
		v0 := fieldMap[fieldNames[0]].GetValue(entity)
		return new1(v0)
	case 2:
		v0 := fieldMap[fieldNames[0]].GetValue(entity)
		v1 := fieldMap[fieldNames[1]].GetValue(entity)
		return new2(v0, v1)
	case 3:
		v0 := fieldMap[fieldNames[0]].GetValue(entity)
		v1 := fieldMap[fieldNames[1]].GetValue(entity)
		v2 := fieldMap[fieldNames[2]].GetValue(entity)
		return new3(v0, v1, v2)
	case 4:
		v0 := fieldMap[fieldNames[0]].GetValue(entity)
		v1 := fieldMap[fieldNames[1]].GetValue(entity)
		v2 := fieldMap[fieldNames[2]].GetValue(entity)
		v3 := fieldMap[fieldNames[3]].GetValue(entity)
		return new4(v0, v1, v2, v3)
	case 5:
		v0 := fieldMap[fieldNames[0]].GetValue(entity)
		v1 := fieldMap[fieldNames[1]].GetValue(entity)
		v2 := fieldMap[fieldNames[2]].GetValue(entity)
		v3 := fieldMap[fieldNames[3]].GetValue(entity)
		v4 := fieldMap[fieldNames[4]].GetValue(entity)
		return new5(v0, v1, v2, v3, v4)
	case 6:
		v0 := fieldMap[fieldNames[0]].GetValue(entity)
		v1 := fieldMap[fieldNames[1]].GetValue(entity)
		v2 := fieldMap[fieldNames[2]].GetValue(entity)
		v3 := fieldMap[fieldNames[3]].GetValue(entity)
		v4 := fieldMap[fieldNames[4]].GetValue(entity)
		v5 := fieldMap[fieldNames[5]].GetValue(entity)
		return new6(v0, v1, v2, v3, v4, v5)
	case 7:
		v0 := fieldMap[fieldNames[0]].GetValue(entity)
		v1 := fieldMap[fieldNames[1]].GetValue(entity)
		v2 := fieldMap[fieldNames[2]].GetValue(entity)
		v3 := fieldMap[fieldNames[3]].GetValue(entity)
		v4 := fieldMap[fieldNames[4]].GetValue(entity)
		v5 := fieldMap[fieldNames[5]].GetValue(entity)
		v6 := fieldMap[fieldNames[6]].GetValue(entity)
		return new7(v0, v1, v2, v3, v4, v5, v6)
	case 8:
		v0 := fieldMap[fieldNames[0]].GetValue(entity)
		v1 := fieldMap[fieldNames[1]].GetValue(entity)
		v2 := fieldMap[fieldNames[2]].GetValue(entity)
		v3 := fieldMap[fieldNames[3]].GetValue(entity)
		v4 := fieldMap[fieldNames[4]].GetValue(entity)
		v5 := fieldMap[fieldNames[5]].GetValue(entity)
		v6 := fieldMap[fieldNames[6]].GetValue(entity)
		v7 := fieldMap[fieldNames[7]].GetValue(entity)
		return new8(v0, v1, v2, v3, v4, v5, v6, v7)
	case 9:
		v0 := fieldMap[fieldNames[0]].GetValue(entity)
		v1 := fieldMap[fieldNames[1]].GetValue(entity)
		v2 := fieldMap[fieldNames[2]].GetValue(entity)
		v3 := fieldMap[fieldNames[3]].GetValue(entity)
		v4 := fieldMap[fieldNames[4]].GetValue(entity)
		v5 := fieldMap[fieldNames[5]].GetValue(entity)
		v6 := fieldMap[fieldNames[6]].GetValue(entity)
		v7 := fieldMap[fieldNames[7]].GetValue(entity)
		v8 := fieldMap[fieldNames[8]].GetValue(entity)
		return new9(v0, v1, v2, v3, v4, v5, v6, v7, v8)
	default:
		panic("ormapper: Key supports up to 9 column values")
	}
}
