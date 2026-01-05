package pyodide

import (
	"log/slog"
	"os"
	"testing"
)

const pyodideVersion = "0.25.1"
const pyodideBaseURL = "https://cdn.jsdelivr.net/pyodide/v" + pyodideVersion + "/full/"

func TestPyodideBasic(t *testing.T) {
	t.Parallel()
	rt, done := setup(t)
	defer done()

	t.Run("Python Math", func(t *testing.T) {
		res, err := rt.Run("1 + 1")
		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
		if res != "2" {
			t.Errorf("expected 2, got %s", res)
		}
	})

	t.Run("Python System", func(t *testing.T) {
		res, err := rt.Run("import sys; sys.version")
		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
		t.Logf("Python version: %s", res)
	})
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
		res, err := rt.Run("import numpy as np; str(np.array([1, 2, 3]).sum())")
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
		res, err := rt.Run("import six; six.__version__")
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
		res, err := rt.Run(`
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

	err := rt.LoadPackage(`https://duckdb.github.io/duckdb-pyodide/wheels/duckdb-1.2.0-cp311-cp311-emscripten_3_1_46_wasm32.whl`)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("DuckDB Query", func(t *testing.T) {
		res, err := rt.Run(`
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

func setup(t *testing.T) (*Runtime, func()) {
	t.Helper()
	testTmpDir := "../../scratch"
	os.MkdirAll(testTmpDir, 0755)

	rt, err := New()
	if err != nil {
		t.Fatal(err)
	}
	// make rt.logger logs Debug level
	rt.logger = slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))
	rt.SetCacheDir(testTmpDir)
	err = rt.Start()
	if err != nil {
		t.Fatal(err)
	}
	err = rt.LoadPyodide(pyodideBaseURL)
	if err != nil {
		rt.Close()
		t.Fatal(err)
	}
	return rt, func() { rt.Close() }
}
