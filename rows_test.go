package agg

import (
	"errors"
	"reflect"
	"testing"
)

var errTestRows = errors.New("test rows error")

type failingRows struct{}

func (failingRows) Next() bool        { return false }
func (failingRows) Scan(...any) error { return nil }
func (failingRows) Err() error        { return errTestRows }
func (failingRows) Close() error      { return nil }

func TestRowIterationErrorsPropagate(t *testing.T) {
	mapper := MustCompile(SQLite, Map(&mappingTestComposite{}))
	mapping := mapper.mappings[reflect.TypeOf(mappingTestComposite{})]
	persistence := newPersistence(mapper.mappings, nil)

	if _, err := persistence.scanEntities(mapping, failingRows{}, 0); !errors.Is(err, errTestRows) {
		t.Fatalf("scanEntities: expected rows error, got %v", err)
	}
	if _, err := persistence.scanTypedKeys(failingRows{}, []reflect.Type{reflect.TypeFor[int]()}); !errors.Is(err, errTestRows) {
		t.Fatalf("scanTypedKeys: expected rows error, got %v", err)
	}
	if _, _, err := persistence.scanKeyPairs(failingRows{}, []reflect.Type{reflect.TypeFor[int]()}, []reflect.Type{reflect.TypeFor[int]()}); !errors.Is(err, errTestRows) {
		t.Fatalf("scanKeyPairs: expected rows error, got %v", err)
	}
	if _, err := persistence.scanReturning(mapping, nil, nil, failingRows{}); !errors.Is(err, errTestRows) {
		t.Fatalf("scanReturning: expected rows error, got %v", err)
	}
}
