package pyodide_test

import (
	"testing"

	"github.com/tommie/v8go"
)

// This file contains tests to learn/verify V8 features.

func TestV8Features(t *testing.T) {
	iso := v8go.NewIsolate()
	defer iso.Dispose()
	ctx := v8go.NewContext(iso)
	defer ctx.Close()

	t.Run("TextDecoder", func(t *testing.T) {
		val, err := ctx.RunScript("typeof TextDecoder", "check.js")
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("TextDecoder: %s", val.String())
	})

	t.Run("WebAssembly", func(t *testing.T) {
		val, err := ctx.RunScript("typeof WebAssembly", "check.js")
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("WebAssembly: %s", val.String())
	})

	t.Run("SharedArrayBuffer", func(t *testing.T) {
		val, err := ctx.RunScript("typeof SharedArrayBuffer", "check.js")
		if err != nil {
			t.Logf("SharedArrayBuffer error: %v", err)
		} else {
			t.Logf("SharedArrayBuffer: %s", val.String())
		}
	})

	t.Run("WebAssembly.instantiateStreaming", func(t *testing.T) {
		val, err := ctx.RunScript("typeof WebAssembly.instantiateStreaming", "check.js")
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("WebAssembly.instantiateStreaming: %s", val.String())
	})
}
