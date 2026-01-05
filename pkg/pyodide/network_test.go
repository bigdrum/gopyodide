package pyodide_test

import (
	"context"
	"testing"
)

func TestPythonNetwork(t *testing.T) {
	t.Parallel()
	rt, done := setup(t)
	defer done()

	t.Run("Pyodide Http Pyfetch", func(t *testing.T) {
		code := `
import pyodide.http
import json
resp = await pyodide.http.pyfetch("https://httpbin.org/get")
data = await resp.json()
data["url"] == "https://httpbin.org/get"
`
		res, err := rt.Run(context.Background(), code)
		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
		if res != "true" {
			t.Errorf("expected true, got %s", res)
		}
	})

	t.Run("Pyodide Open URL", func(t *testing.T) {
		code := `
import pyodide.http
import json
def test_open_url():
    response = pyodide.http.open_url("https://httpbin.org/get")
    content = response.read()
    data = json.loads(content)
    return data["url"] == "https://httpbin.org/get"
test_open_url()
`
		res, err := rt.Run(context.Background(), code)
		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
		if res != "true" {
			t.Errorf("expected true, got %s", res)
		}
	})
}
