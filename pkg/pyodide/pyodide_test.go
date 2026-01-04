package pyodide

import (
	"os"
	"testing"
)

const pyodideVersion = "0.22.1"
const pyodideBaseURL = "https://cdn.jsdelivr.net/pyodide/v" + pyodideVersion + "/full/"

func TestPyodideBasic(t *testing.T) {
	testDataDir := "../../testdata"
	os.MkdirAll(testDataDir, 0755)

	rt, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()

	rt.SetCacheDir(testDataDir)

	jsData, err := rt.FetchAsset(pyodideBaseURL + "pyodide.js")
	if err != nil {
		t.Fatal(err)
	}

	err = rt.LoadPyodide(string(jsData), pyodideBaseURL)
	if err != nil {
		t.Fatal(err)
	}

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
	testDataDir := "../../testdata"
	os.MkdirAll(testDataDir, 0755)

	rt, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()

	rt.SetCacheDir(testDataDir)

	jsData, err := rt.FetchAsset(pyodideBaseURL + "pyodide.js")
	if err != nil {
		t.Fatal(err)
	}

	err = rt.LoadPyodide(string(jsData), pyodideBaseURL)
	if err != nil {
		t.Fatal(err)
	}

	err = rt.LoadPackage("numpy")
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
	testDataDir := "../../testdata"
	os.MkdirAll(testDataDir, 0755)

	rt, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()

	rt.SetCacheDir(testDataDir)

	jsData, err := rt.FetchAsset(pyodideBaseURL + "pyodide.js")
	if err != nil {
		t.Fatal(err)
	}

	err = rt.LoadPyodide(string(jsData), pyodideBaseURL)
	if err != nil {
		t.Fatal(err)
	}

	err = rt.LoadPackage("six")
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
