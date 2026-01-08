package pyodide_test

import (
	"context"
	"testing"
)

func TestAtobBtoa(t *testing.T) {
	rt, done := setup(t)
	defer done()

	t.Run("btoa", func(t *testing.T) {
		res, err := rt.RunJS(context.Background(), "btoa('hello world')")
		if err != nil {
			t.Fatal(err)
		}
		expected := "aGVsbG8gd29ybGQ="
		if res.String() != expected {
			t.Errorf("expected %s, got %s", expected, res.String())
		}
	})

	t.Run("atob", func(t *testing.T) {
		res, err := rt.RunJS(context.Background(), "atob('aGVsbG8gd29ybGQ=')")
		if err != nil {
			t.Fatal(err)
		}
		expected := "hello world"
		if res.String() != expected {
			t.Errorf("expected %s, got %s", expected, res.String())
		}
	})

	t.Run("btoa-atob-roundtrip", func(t *testing.T) {
		res, err := rt.RunJS(context.Background(), "atob(btoa('hello world'))")
		if err != nil {
			t.Fatal(err)
		}
		expected := "hello world"
		if res.String() != expected {
			t.Errorf("expected %s, got %s", expected, res.String())
		}
	})

	t.Run("btoa-latin1", func(t *testing.T) {
		// btoa should work for characters 0-255
		// char code 255 is 'ÿ'
		res, err := rt.RunJS(context.Background(), "btoa('\\xff')")
		if err != nil {
			t.Fatal(err)
		}
		expected := "/w=="
		if res.String() != expected {
			t.Errorf("expected %s, got %s", expected, res.String())
		}
	})

	t.Run("btoa-error", func(t *testing.T) {
		_, err := rt.RunJS(context.Background(), "btoa('\\u0100')")
		if err == nil {
			t.Error("expected error for non-latin1 character")
		}
	})

	t.Run("atob-error", func(t *testing.T) {
		_, err := rt.RunJS(context.Background(), "atob('invalid base64')")
		if err == nil {
			t.Error("expected error for invalid base64")
		}
	})

	t.Run("atob-whitespace", func(t *testing.T) {
		res, err := rt.RunJS(context.Background(), "atob('aGV sbG8gd 29 ybG Q=')")
		if err != nil {
			t.Fatal(err)
		}
		expected := "hello world"
		if res.String() != expected {
			t.Errorf("expected %s, got %s", expected, res.String())
		}
	})

	t.Run("atob-latin1", func(t *testing.T) {
		// atob should return a string where charCodeAt(0) matches the byte value
		res, err := rt.RunJS(context.Background(), "atob('/w==').charCodeAt(0)")
		if err != nil {
			t.Fatal(err)
		}
		if res.Int32() != 255 {
			t.Errorf("expected 255, got %v", res.Int32())
		}
	})
}
