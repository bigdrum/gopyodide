package pyodide_test

import (
	"context"
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
	tmpDir, err := os.MkdirTemp("", "pyodide-ro-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	tmpFile := filepath.Join(tmpDir, "test.txt")
	content := "Hello from Host!"
	if err := os.WriteFile(tmpFile, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	err = rt.MountHostDir(tmpDir, "/mnt", false)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	script := `
import os
file_path = "/mnt/test.txt"
with open(file_path, "r") as f:
    data = f.read()
data
`

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

func TestWriteFile(t *testing.T) {
	rt, done := setup(t)
	defer done()

	tmpDir, err := os.MkdirTemp("", "pyodide-write-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	err = rt.MountHostDir(tmpDir, "/mnt_write", false)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	content := "Hello from pyodide"

	script := `
import os
file_path = "/mnt_write/test_write.txt"
with open(file_path, "w") as f:
    f.write("` + content + `")
"OK"
`
	_, err = rt.Run(ctx, script)
	if err != nil {
		t.Fatal(err)
	}

	hostFilePath := filepath.Join(tmpDir, "test_write.txt")
	readContent, err := os.ReadFile(hostFilePath)
	if err != nil {
		t.Fatal(err)
	}

	if string(readContent) != content {
		t.Errorf("expected %q, got %q", content, string(readContent))
	}
}