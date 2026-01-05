package pyodide_test

import (
	"log/slog"
	"os"
	"testing"

	"github.com/bigdrum/gopyodide/pkg/pyodide"
)

const pyodideVersion = "0.27.6"
const pyodideBaseURL = "https://cdn.jsdelivr.net/pyodide/v" + pyodideVersion + "/full/"

func setup(t *testing.T) (*pyodide.Runtime, func()) {
	t.Helper()
	testTmpDir := "../../scratch"
	os.MkdirAll(testTmpDir, 0755)

	rt, err := pyodide.New()
	if err != nil {
		t.Fatal(err)
	}

	replaceAttr := func(groups []string, a slog.Attr) slog.Attr {
		// Reduce verbosity to save tokens for ai.
		if a.Key == slog.TimeKey || a.Key == slog.LevelKey {
			return slog.Attr{}
		}
		return a
	}

	rt.SetLogger(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		// To enable debug logging, uncomment the following line:
		Level: slog.LevelDebug,
		// Level: slog.LevelInfo,
		ReplaceAttr: replaceAttr,
	})))
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
