package pyodide_test

import (
	"context"
	"testing"
)

func TestPyodideBasic(t *testing.T) {
	t.Parallel()
	rt, done := setup(t)
	defer done()

	t.Run("Python Math", func(t *testing.T) {
		res, err := rt.RunPython(context.Background(), "1 + 1")
		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
		if res != "2" {
			t.Errorf("expected 2, got %s", res)
		}
	})

	t.Run("Python System", func(t *testing.T) {
		res, err := rt.RunPython(context.Background(), "import sys; sys.version")
		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
		t.Logf("Python version: %s", res)
	})
}

func TestPyodideEnvironment(t *testing.T) {
	t.Parallel()
	rt, done := setup(t)
	defer done()

	js := `
		(() => {
			const isWorker = typeof globalThis.WorkerGlobalScope !== "undefined" &&
				typeof globalThis.self !== "undefined" &&
				globalThis.self instanceof globalThis.WorkerGlobalScope;
			return isWorker;
		})()
	`
	val, err := rt.RunJS(context.Background(), js)
	if err != nil {
		t.Fatalf("Failed to run JS: %v", err)
	}

	if val.String() != "true" {
		t.Errorf("Expected environment to be detected as Web Worker (true), got %s", val.String())
	}
}

func TestPyodideNumpy(t *testing.T) {
	t.Parallel()
	rt, done := setup(t)
	defer done()

	err := rt.LoadPackage("numpy")
	if err != nil {
		t.Fatal(err)
	}

	t.Run("Numpy Sum", func(t *testing.T) {
		res, err := rt.RunPython(context.Background(), "import numpy as np; str(np.array([1, 2, 3]).sum())")
		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
		if res != "6" {
			t.Errorf("expected 6, got %s", res)
		}
	})
}

func TestPyodideSix(t *testing.T) {
	t.Parallel()
	rt, done := setup(t)
	defer done()

	err := rt.LoadPackage("six")
	if err != nil {
		t.Fatal(err)
	}

	t.Run("Six Import", func(t *testing.T) {
		res, err := rt.RunPython(context.Background(), "import six; six.__version__")
		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
		t.Logf("six version: %s", res)
	})
}

func TestPyodidePandas(t *testing.T) {
	t.Parallel()
	rt, done := setup(t)
	defer done()

	err := rt.LoadPackage("pandas")
	if err != nil {
		t.Fatal(err)
	}

	t.Run("Pandas DataFrame", func(t *testing.T) {
		res, err := rt.RunPython(context.Background(), `
import pandas as pd
import numpy as np
df = pd.DataFrame({'a': [1, 2], 'b': [3, 4]})
str(df['a'].sum())
`)
		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
		if res != "3" {
			t.Errorf("expected 3, got %s", res)
		}
	})
}

func TestPyodideDuckDB(t *testing.T) {
	t.Parallel()
	rt, done := setup(t)
	defer done()

	err := rt.LoadPackage(`https://duckdb.github.io/duckdb-pyodide/wheels/duckdb-1.2.0-cp312-cp312-pyodide_2024_0_wasm32.whl`)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("DuckDB Query", func(t *testing.T) {
		res, err := rt.RunPython(context.Background(), `
import duckdb
con = duckdb.connect()
res = con.execute("SELECT 42").fetchone()
str(res[0])
`)
		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
		if res != "42" {
			t.Errorf("expected 42, got %s", res)
		}
	})
}
