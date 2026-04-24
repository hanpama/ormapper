package ormapper

import "errors"

var (
	ErrConsistency         = errors.New("ormapper consistency error")
	ErrStaleEntity         = errors.New("ormapper stale entity")
	ErrUnsupportedSemantic = errors.New("ormapper unsupported semantic")
)
