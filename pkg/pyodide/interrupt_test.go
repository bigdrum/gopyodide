package pyodide_test

import (
	"context"
	"testing"
	"time"
)

func TestPyodideInterrupt(t *testing.T) {
	t.Parallel()
	rt, done := setup(t)
	defer done()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Launch global cancellation in 500ms
	go func() {
		time.Sleep(500 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	// Run an infinite loop in Python.
	// We catch KeyboardInterrupt to verify it was thrown.
	code := `
import time
res = 'RUNNING'
try:
    start = time.time()
    while True:
        time.sleep(0.1)
        if time.time() - start > 10:
             res = "TIMEOUT"
             break
except KeyboardInterrupt:
    res = "INTERRUPTED"
res
`
	res, err := rt.Run(ctx, code)
	duration := time.Since(start)

	if err != nil {
		t.Logf("Run returned error: %v", err)
	}

	if res == "INTERRUPTED" {
		t.Logf("Successfully interrupted Python after %v", duration)
	} else {
		t.Errorf("Expected 'INTERRUPTED' result, got '%s', err: %v", res, err)
	}

	// Verify we didn't wait timeout
	if duration > 2000*time.Millisecond {
		t.Errorf("Interruption took too long: %v", duration)
	}
}
