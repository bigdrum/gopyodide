package pyodide

import (
	"log/slog"
	"os"
	"testing"
)

func setup(t *testing.T) (*Runtime, func()) {
	t.Helper()
	testTmpDir := "../../scratch"
	os.MkdirAll(testTmpDir, 0755)

	rt, err := New()
	if err != nil {
		t.Fatal(err)
	}
	rt.logger = slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		// To enable debug logging, uncomment the following line:
		// Level: slog.LevelDebug,
		Level: slog.LevelInfo,
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
