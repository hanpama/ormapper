package ormapper

import "errors"

var (
	// ErrConsistency indicates a data integrity violation such as duplicate
	// submitted keys or a row count mismatch from the database.
	ErrConsistency = errors.New("ormapper consistency error")

	// ErrStaleEntity indicates that a non-zero auto-generated key does not
	// exist in the database. The entity was likely deleted or never inserted.
	ErrStaleEntity = errors.New("ormapper stale entity")

	// ErrUnsupportedSemantic indicates a mapping configuration that the
	// library cannot handle, such as a generated composite primary key or
	// partially zero generated key fields.
	ErrUnsupportedSemantic = errors.New("ormapper unsupported semantic")
)
