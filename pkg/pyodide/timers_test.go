package pyodide_test

import (
	"testing"
	"time"

	"github.com/tommie/v8go"
)

func TestSetTimeout(t *testing.T) {
	t.Parallel()
	rt, done := setup(t)
	defer done()

	ch := make(chan bool)
	err := rt.V8Context().Global().Set("go_callback", v8go.NewFunctionTemplate(rt.V8Isolate(), func(info *v8go.FunctionCallbackInfo) *v8go.Value {
		ch <- true
		return nil
	}).GetFunction(rt.V8Context()))
	if err != nil {
		t.Fatal(err)
	}

	_, err = rt.V8Context().RunScript("setTimeout(go_callback, 10)", "test.js")
	if err != nil {
		t.Fatal(err)
	}

	select {
	case <-ch:
		// success
	case <-time.After(1 * time.Second):
		t.Fatal("setTimeout callback was not called")
	}
}

func TestClearTimeout(t *testing.T) {
	t.Parallel()
	rt, done := setup(t)
	defer done()

	ch := make(chan bool)
	err := rt.V8Context().Global().Set("go_callback", v8go.NewFunctionTemplate(rt.V8Isolate(), func(info *v8go.FunctionCallbackInfo) *v8go.Value {
		ch <- true
		return nil
	}).GetFunction(rt.V8Context()))
	if err != nil {
		t.Fatal(err)
	}

	_, err = rt.V8Context().RunScript("const timerId = setTimeout(go_callback, 100); clearTimeout(timerId);", "test.js")
	if err != nil {
		t.Fatal(err)
	}

	select {
	case <-ch:
		t.Fatal("setTimeout callback was called after clearTimeout")
	case <-time.After(200 * time.Millisecond):
		// success
	}
}

func TestSetInterval(t *testing.T) {
	t.Parallel()
	rt, done := setup(t)
	defer done()

	ch := make(chan bool)
	err := rt.V8Context().Global().Set("go_callback", v8go.NewFunctionTemplate(rt.V8Isolate(), func(info *v8go.FunctionCallbackInfo) *v8go.Value {
		ch <- true
		return nil
	}).GetFunction(rt.V8Context()))
	if err != nil {
		t.Fatal(err)
	}

	_, err = rt.V8Context().RunScript("const intervalId = setInterval(go_callback, 100); setTimeout(() => clearInterval(intervalId), 250);", "test.js")
	if err != nil {
		t.Fatal(err)
	}

	count := 0
	for {
		select {
		case <-ch:
			count++
			if count > 2 {
				t.Errorf("setInterval callback called more than 2 times")
			}
		case <-time.After(500 * time.Millisecond):
			if count != 2 {
				t.Errorf("expected 2 calls, got %d", count)
			}
			return
		}
	}
}
