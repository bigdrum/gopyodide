# gopyodide

`gopyodide` is a Go library that allows you to run Python (with NumPy, Pandas,
DuckDB etc.) code using [Pyodide](https://pyodide.org/) within a V8 environment
embedded in your Go application. It leverages
[v8go](https://github.com/tommie/v8go) to provide a JavaScript runtime capable
of executing WebAssembly.

This library is currently a proof of concept.

## Features

- **Embedded Python Runtime**: Run Python 3.11+ via WebAssembly in V8.
- **Go <> Python Interop**: Execute Python code from Go and retrieve results as
  strings.
- **Package Support**: Load and use popular Python packages like NumPy, Pandas,
  and DuckDB.
- **Context Awareness**: Support for context cancellation and execution
  interruption.
- **Asset Caching**: Caches downloaded Pyodide artifacts and packages for faster
  subsequent startups.

## Why JavaScript and V8?

[Pyodide](https://pyodide.org/) is a port of CPython to WebAssembly/Emscripten.
It is designed to run in a browser environment, which provides a JavaScript
runtime and WebAssembly support. To run Pyodide outside of a browser (e.g., in a
Go application), we need to emulate this environment.

`gopyodide` uses [v8go](https://github.com/tommie/v8go) to embed the V8
JavaScript engine (the same engine used in Chrome and Node.js) directly into
your Go binary. This allows us to:

1. **Execute WebAssembly**: Monitor and run the Pyodide WASM binary.
2. **Provide Browser Polyfills**: Emulate necessary browser APIs that Pyodide
   expects (like `fetch`, `console`, etc.) within the V8 context.
3. **Bridge Go and Python**: Facilitate communication between Go and the Python
   runtime running inside the JS/WASM environment.

Ideally, we would run the Python WASM binary directly using a native Go
WebAssembly runtime like [wazero](https://wazero.io/), avoiding the overhead of
a full JavaScript engine. However, the Python data science ecosystem (NumPy,
Pandas, etc.) primarily targets the Emscripten/Browser environment. While WASI
(WebAssembly System Interface) is maturing, there is currently (as of late 2025)
no simple, well-maintained set of precompiled wheels for these libraries that
target pure WASI. Pure WASM/WASI support will be explored by other projects.

## Installation

```bash
go get github.com/bigdrum/gopyodide
```

## Usage

### Basic Usage

```go
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/bigdrum/gopyodide/pkg/pyodide"
)

func main() {
	// Initialize the runtime
	rt, err := pyodide.New()
	if err != nil {
		log.Fatalf("Failed to create runtime: %v", err)
	}
	
	// Start the runtime loop and initialize the V8 context
	if err := rt.Start(); err != nil {
		log.Fatalf("Failed to start runtime: %v", err)
	}
	defer rt.Close()

	// Load Pyodide from CDN (you can also use a local path if served via HTTP or file system)
	// This downloads pyodide.js and initializes the runtime.
	err = rt.LoadPyodide("https://cdn.jsdelivr.net/pyodide/v0.25.1/full/")
	if err != nil {
		log.Fatalf("Failed to load Pyodide: %v", err)
	}

	// Run Python code
	res, err := rt.RunPython(context.Background(), "1 + 1")
	if err != nil {
		log.Fatalf("Python execution failed: %v", err)
	}
	fmt.Printf("Result: %s\n", res) // Output: Result: 2
}
```

### Loading Packages

You can dynamically load Python packages such as NumPy or Pandas.

```go
	// Load NumPy
	// The runtime handles downloading and installing the package.
	if err := rt.LoadPackage("numpy"); err != nil {
		log.Fatalf("Failed to load numpy: %v", err)
	}

	// Run code using NumPy
	code := `
import numpy as np
arr = np.array([1, 2, 3])
str(arr.sum())
`
	res, err := rt.RunPython(context.Background(), code)
	if err != nil {
		log.Fatalf("Execution failed: %v", err)
	}
	fmt.Printf("Sum: %s\n", res) // Output: Sum: 6
```

### Context Cancellation

The `RunPython` function respects `context.Context`. Cancelling the context will
trigger a Python `KeyboardInterrupt` in the running script.

```go
	// Create a context with a timeout
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	// Run a long-running Python loop (e.g., an infinite loop)
	// This will be interrupted when the context times out.
	_, err = rt.RunPython(ctx, "while True: pass")
	if err != nil {
		fmt.Printf("Execution interrupted: %v\n", err)
	}
```

## Requirements

- **CGO Enabled**: This library requires CGO to be enabled because it depends on
  `v8go`.

## License

This project is licensed under the MIT License - see the [LICENSE](LICENSE) file
for details.
