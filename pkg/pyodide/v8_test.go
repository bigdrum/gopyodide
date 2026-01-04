package pyodide

import (
	"fmt"
	"testing"

	"rogchap.com/v8go"
)

func TestV8Raw(t *testing.T) {
	iso := v8go.NewIsolate()
	defer iso.Dispose()

	foo := v8go.NewFunctionTemplate(iso, func(info *v8go.FunctionCallbackInfo) *v8go.Value {
		fmt.Printf("DEBUG_LOG: %s\n", info.Args()[0].String())
		return nil
	})

	global := v8go.NewObjectTemplate(iso)
	global.Set("log", foo)

	ctx := v8go.NewContext(iso, global)
	defer ctx.Close()

	_, err := ctx.RunScript("log('Hello from V8')", "test.js")
	if err != nil {
		t.Fatal(err)
	}

	_, err = ctx.RunScript("if (typeof WebAssembly !== 'undefined') { log('WASM is available'); if(WebAssembly.Global) log('WASM Global is available'); else log('WASM Global is NOT available'); } else { log('WASM is NOT available'); }", "wasm.js")
	if err != nil {
		t.Fatal(err)
	}
}
