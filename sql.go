package ormapper

import (
	"fmt"
	"strings"
)

type sqlNode interface {
	isSQL()
}

type sqlN struct{ Part string }

type sqlQN struct{ Part1, Part2 string }

type sqlText struct{ Text string }

type sqlParam struct{ Value any }

type sqlAll struct{ Els []sqlNode }

type sqlAny struct{ Els []sqlNode }

type sqlEq struct{ Left, Right sqlNode }

type sqlLt struct{ Left, Right sqlNode }

type sqlGt struct{ Left, Right sqlNode }

type sqlIsNull struct{ Operand sqlNode }

type sqlIsNotNull struct{ Operand sqlNode }

type sqlFragment struct{ Els []sqlNode }

// Marker method implementations
func (sqlN) isSQL()         {}
func (sqlQN) isSQL()        {}
func (sqlText) isSQL()      {}
func (sqlParam) isSQL()     {}
func (sqlAll) isSQL()       {}
func (sqlAny) isSQL()       {}
func (sqlEq) isSQL()        {}
func (sqlLt) isSQL()        {}
func (sqlGt) isSQL()        {}
func (sqlIsNull) isSQL()    {}
func (sqlIsNotNull) isSQL() {}
func (sqlFragment) isSQL()  {}

type join struct {
	Type  string // "JOIN" | "LEFT JOIN"
	Table sqlNode
	Alias sqlNode
	On    sqlNode
}

// OrderExpr is an opaque ORDER BY expression created by Query.Asc or Query.Desc.
type OrderExpr struct {
	expr      sqlNode
	ascending bool
	nullsLast bool
}

type sqlQuery struct {
	Select    []sqlNode
	FromTable sqlNode
	FromAlias sqlNode
	Joins     []join
	Where     *sqlNode
	OrderBys  []OrderExpr
	GroupBy   []sqlNode
	Having    *sqlNode
	Limit     *sqlNode
	Offset    *sqlNode
}

func parseSQL(sql string, params ...any) sqlNode {
	// Pre-allocate tokens slice with correct capacity
	// N params means N params + (N+1) text segments = 2N+1 tokens
	tokens := make([]sqlNode, 0, 2*len(params)+1)
	paramIdx := 0
	var textBuilder strings.Builder
	inSingleQuote := false
	inDoubleQuote := false

	for i := 0; i < len(sql); i++ {
		ch := sql[i]

		if ch == '\'' && !inDoubleQuote {
			inSingleQuote = !inSingleQuote
			textBuilder.WriteByte(ch)
		} else if ch == '"' && !inSingleQuote {
			inDoubleQuote = !inDoubleQuote
			textBuilder.WriteByte(ch)
		} else if ch == '?' && !inSingleQuote && !inDoubleQuote {
			// Only treat ? as parameter placeholder if not inside quotes
			if textBuilder.Len() > 0 {
				tokens = append(tokens, sqlText{Text: textBuilder.String()})
				textBuilder.Reset()
			}
			if paramIdx >= len(params) {
				panic(fmt.Sprintf("Not enough parameters: expected at least %d, got %d", paramIdx+1, len(params)))
			}
			tokens = append(tokens, sqlParam{Value: params[paramIdx]})
			paramIdx++
		} else {
			textBuilder.WriteByte(ch)
		}
	}

	if textBuilder.Len() > 0 {
		tokens = append(tokens, sqlText{Text: textBuilder.String()})
	}

	if paramIdx != len(params) {
		panic(fmt.Sprintf("Too many parameters: expected %d, got %d", paramIdx, len(params)))
	}

	return sqlFragment{Els: tokens}
}
