# Run tests

```bash
go test -v ./pkg/pyodide -parallel 1
```

Run specific test:

```bash
go test -v ./pkg/pyodide -run TestPyodideInterrupt
```

- Most test cases should use `setup()` from `setup_test.go` to initialize the
  runtime.
