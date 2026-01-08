package pyodide_test

import (
	"context"
	"strings"
	"testing"
)

func TestPythonFileIO(t *testing.T) {
	rt, done := setup(t)
	defer done()

	// Step 1: Write a file using Python
	writeCode := `
with open("test_file.txt", "w") as f:
    f.write("Hello from Python File IO")
"File written"
`
	res, err := rt.RunPython(context.Background(), writeCode)
	if err != nil {
		t.Fatalf("Failed to write file: %v", err)
	}
	if !strings.Contains(res, "File written") {
		t.Errorf("Unexpected result from write: %s", res)
	}

	// Step 2: Read the file using Python and verify content
	readCode := `
with open("test_file.txt", "r") as f:
    content = f.read()
content
`
	res, err = rt.RunPython(context.Background(), readCode)
	if err != nil {
		t.Fatalf("Failed to read file: %v", err)
	}
	if res != "Hello from Python File IO" {
		t.Errorf("Unexpected content read from file: got %q, want 'Hello from Python File IO'", res)
	}

	// Step 3: Verify persistence across Run calls (already implicitly tested above, but let's be explicit with a structured check)
	checkCode := `
import os
os.path.exists("test_file.txt")
`
	res, err = rt.RunPython(context.Background(), checkCode)
	if err != nil {
		t.Fatalf("Failed to check file existence: %v", err)
	}
	if res != "true" {
		t.Errorf("File did not persist or wasn't found: %s", res)
	}
}

func TestPythonMkdirAndList(t *testing.T) {
	rt, done := setup(t)
	defer done()

	code := `
import os
os.makedirs("data/subdir", exist_ok=True)
with open("data/subdir/file.txt", "w") as f:
    f.write("nested")
os.listdir("data/subdir")
`
	res, err := rt.RunPython(context.Background(), code)
	if err != nil {
		t.Fatalf("Failed to manipulate directories: %v", err)
	}
	if !strings.Contains(res, "'file.txt'") {
		t.Errorf("File not found in subdirectory listing: %s", res)
	}
}
