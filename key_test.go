package agg

import "testing"

func TestNewKeyArities(t *testing.T) {
	for n := 0; n <= 9; n++ {
		values := make([]any, n)
		for i := range values {
			values[i] = i + 1
		}

		key := NewKey(values...)
		if key.Length() != n {
			t.Fatalf("arity %d: expected length %d, got %d", n, n, key.Length())
		}
		for i, want := range values {
			if got := key.At(i); got != want {
				t.Fatalf("arity %d index %d: expected %v, got %v", n, i, want, got)
			}
		}
	}
}

func TestNewKeyAsMapKey(t *testing.T) {
	values := map[Key]string{
		NewKey(1):       "single",
		NewKey(1, "x"):  "composite",
		NewKey(1, "y"):  "other",
		NewKey(1, "x"):  "overwritten",
		NewKey(1, 2, 3): "triple",
	}

	if values[NewKey(1)] != "single" {
		t.Fatal("single-column key lookup failed")
	}
	if values[NewKey(1, "x")] != "overwritten" {
		t.Fatal("composite key equality failed")
	}
	if values[NewKey(1, "y")] != "other" {
		t.Fatal("distinct composite key lookup failed")
	}
	if values[NewKey(1, 2, 3)] != "triple" {
		t.Fatal("triple key lookup failed")
	}
}

func TestNewKeyPanicsOnTooManyValues(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected NewKey with 10 values to panic")
		}
	}()

	_ = NewKey(1, 2, 3, 4, 5, 6, 7, 8, 9, 10)
}

func TestNewKeyPanicsOnNonComparableValue(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected NewKey with a slice value to panic")
		}
	}()

	_ = NewKey([]byte("not-comparable"))
}

func TestKeyAtPanicsOutOfBounds(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected Key.At out of bounds to panic")
		}
	}()

	_ = NewKey(1).At(1)
}
