package pyodide_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFileSystems(t *testing.T) {
	rt, done := setup(t)
	defer done()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	script := `
import js
fss = []
try:
    if hasattr(js.pyodide, 'FS') and hasattr(js.pyodide.FS, 'filesystems'):
        for name in dir(js.pyodide.FS.filesystems):
            fss.append(name)
except Exception as e:
    fss.append(str(e))
",".join(fss)
`
	out, err := rt.Run(ctx, script)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("Available filesystems: %s", out)
}

func TestPyodideMountHostFS(t *testing.T) {
	rt, done := setup(t)
	defer done()

	// Create a temp file on host
	tmpFile, err := os.CreateTemp("", "pyodide-host-*.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpFile.Name())
	content := "Hello from Host!"
	if _, err := tmpFile.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
	tmpFile.Close()

	hostPath := filepath.Dir(tmpFile.Name())
	fileName := filepath.Base(tmpFile.Name())

	err = rt.MountHostDir(hostPath, "/mnt", false)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	script := fmt.Sprintf(`
import os
file_path = "/mnt/%s"
with open(file_path, "r") as f:
    data = f.read()
data
`, fileName)

	res, err := rt.Run(ctx, script)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	if res != content {
		t.Errorf("Expected content %q, got %q", content, res)
	}
}

func TestPyodideMountReadOnly(t *testing.T) {
	rt, done := setup(t)
	defer done()

	tmpDir, err := os.MkdirTemp("", "pyodide-ro-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	tmpFile := filepath.Join(tmpDir, "test.txt")
	os.WriteFile(tmpFile, []byte("read only content"), 0644)

	err = rt.MountHostDir(tmpDir, "/mnt_ro", true)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Verify reading works
	res, err := rt.Run(ctx, `
with open("/mnt_ro/test.txt", "r") as f:
    data = f.read()
data
`)
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}
	if res != "read only content" {
		t.Errorf("Expected %q, got %q", "read only content", res)
	}

	// Verify writing fails
	res, err = rt.Run(ctx, `
try:
    with open("/mnt_ro/fail.txt", "w") as f:
        f.write("should fail")
    res = "SUCCESS"
except Exception as e:
    res = str(e)
res
`)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if res == "SUCCESS" {
		t.Error("Expected write to fail on read-only mount, but it succeeded")
	} else {
		t.Logf("Got expected error: %s", res)
	}
}
