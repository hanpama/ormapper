package agg_test

import (
	"fmt"

	"github.com/hanpama/agg"
)

func ExampleCompile() {
	type Order struct {
		ID    int64 `agg:"auto"`
		Total int64
	}

	mapper, err := agg.Compile(
		agg.SQLite,
		agg.Map(&Order{}, agg.WithTable("orders")),
	)

	fmt.Println(mapper != nil, err)
	// Output: true <nil>
}
