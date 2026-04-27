package agg

import (
	"fmt"
	"strings"
)

type sqlNode interface {
	isSQL()
}

type sqlN struct{ part string }

type sqlQN struct{ part1, part2 string }

type sqlText struct{ text string }

type sqlParam struct{ value any }

type sqlAll struct{ els []sqlNode }

type sqlAny struct{ els []sqlNode }

type sqlEq struct{ left, right sqlNode }

type sqlLt struct{ left, right sqlNode }

type sqlGt struct{ left, right sqlNode }

type sqlIsNull struct{ operand sqlNode }

type sqlIsNotNull struct{ operand sqlNode }

type sqlFragment struct{ els []sqlNode }

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
	typ   string // "JOIN" | "LEFT JOIN"
	table sqlNode
	alias sqlNode
	on    sqlNode
}

// OrderExpr is an opaque ORDER BY expression created by Query.Asc or Query.Desc.
type OrderExpr struct {
	expr      sqlNode
	ascending bool
	nullsLast bool
}

type sqlQuery struct {
	selectColumns []sqlNode
	fromTable     sqlNode
	fromAlias     sqlNode
	joins         []join
	where         *sqlNode
	orderBys      []OrderExpr
	groupBy       []sqlNode
	having        *sqlNode
	limit         *sqlNode
	offset        *sqlNode
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
				tokens = append(tokens, sqlText{text: textBuilder.String()})
				textBuilder.Reset()
			}
			if paramIdx >= len(params) {
				panic(fmt.Sprintf("Not enough parameters: expected at least %d, got %d", paramIdx+1, len(params)))
			}
			tokens = append(tokens, sqlParam{value: params[paramIdx]})
			paramIdx++
		} else {
			textBuilder.WriteByte(ch)
		}
	}

	if textBuilder.Len() > 0 {
		tokens = append(tokens, sqlText{text: textBuilder.String()})
	}

	if paramIdx != len(params) {
		panic(fmt.Sprintf("Too many parameters: expected %d, got %d", paramIdx, len(params)))
	}

	return sqlFragment{els: tokens}
}
