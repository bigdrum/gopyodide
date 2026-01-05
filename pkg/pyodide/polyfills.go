package pyodide

import (
	"context"
	"encoding/base64"
	"fmt"
	"log/slog"
	"time"

	"github.com/tommie/v8go"
)

func (rt *Runtime) polyfill() {
	// We implement enough polyfills to run pyodide.
	// Some are implemented in init.js.
	rt.polyfillConsole()
	rt.polyfillImportScripts()
	rt.polyfillFetch()
	rt.polyfillSetTimeout()
	rt.polyfillSetInterval()
	rt.polyfillClearTimeout()
	rt.polyfillClearInterval()
	rt.polyfillBtoa()
	rt.polyfillAtob()
	rt.polyfillQueueTask()
}

func (rt *Runtime) polyfillConsole() {
	iso := rt.isolate
	global := rt.context.Global()

	logFn := func(level string) *v8go.FunctionTemplate {
		return v8go.NewFunctionTemplate(iso, func(info *v8go.FunctionCallbackInfo) *v8go.Value {
			if rt.logger.Enabled(context.Background(), slog.LevelInfo) {
				args := info.Args()
				s := ""
				for i, a := range args {
					if i > 0 {
						s += " "
					}
					s += a.String()
				}
				rt.logger.Info("JS_"+level, "message", s)
			}
			return nil
		})
	}
	consoleObjTempl := v8go.NewObjectTemplate(iso)
	consoleObjTempl.Set("log", logFn("LOG"))
	consoleObjTempl.Set("error", logFn("ERROR"))
	consoleObjTempl.Set("warn", logFn("WARN"))
	consoleObjTempl.Set("info", logFn("INFO"))
	consoleObjTempl.Set("debug", logFn("DEBUG"))
	consoleObj, _ := consoleObjTempl.NewInstance(rt.context)
	global.Set("console", consoleObj)
}

func (rt *Runtime) polyfillImportScripts() {
	iso := rt.isolate
	global := rt.context.Global()

	importScriptsFn := v8go.NewFunctionTemplate(iso, func(info *v8go.FunctionCallbackInfo) *v8go.Value {
		if val := rt.checkArgs(info, 1); val != nil {
			return val
		}
		url := info.Args()[0].String()
		rt.logger.Debug("JS_IMPORT_START", "url", url)
		data, err := rt.FetchAsset(url)
		if err != nil {
			rt.logger.Error("JS_IMPORT_ERROR", "url", url, "error", err)
			val := rt.errorValue(err)
			return iso.ThrowException(val)
		}
		rt.logger.Debug("JS_IMPORT_DONE", "url", url, "bytes", len(data))
		_, err = rt.context.RunScript(string(data), url)
		if err != nil {
			rt.logger.Error("JS_IMPORT_EXEC_ERROR", "url", url, "error", err)
			val := rt.errorValue(err)
			return iso.ThrowException(val)
		}
		return nil
	})
	importScripts := importScriptsFn.GetFunction(rt.context)
	global.Set("importScripts", importScripts)
}

func (rt *Runtime) errorValue(err error) *v8go.Value {
	val, err := v8go.NewValue(rt.isolate, err.Error())
	if err != nil {
		panic(err)
	}
	return val
}

func (rt *Runtime) polyfillFetch() {
	iso := rt.isolate
	global := rt.context.Global()

	fetchFn := v8go.NewFunctionTemplate(iso, func(info *v8go.FunctionCallbackInfo) *v8go.Value {
		if val := rt.checkArgs(info, 1); val != nil {
			return val
		}
		args := info.Args()
		resolver, _ := v8go.NewPromiseResolver(rt.context)
		url := args[0].String()
		rt.logger.Debug("JS_FETCH_URL", "url", url)
		rt.spawnGo(func() {
			rt.logger.Debug("JS_FETCH_START", "url", url)
			data, err := rt.FetchAsset(url)
			rt.queueTask("fetch_resolve", func() {
				if err != nil {
					rt.logger.Error("JS_FETCH_ERROR", "url", url, "error", err)
					val := rt.errorValue(err)
					resolver.Reject(val)
					return
				}

				rt.logger.Debug("JS_FETCH_DONE", "url", url, "bytes", len(data))

				global := rt.context.Global()
				createSABVal, err := global.Get("__createSAB")
				if err != nil {
					resolver.Reject(rt.errorValue(err))
					return
				}
				createSAB, err := createSABVal.AsFunction()
				if err != nil {
					resolver.Reject(rt.errorValue(err))
					return
				}

				sizeVal, _ := v8go.NewValue(iso, int32(len(data)))
				sabVal, err := createSAB.Call(v8go.Undefined(iso), sizeVal)
				if err != nil {
					resolver.Reject(rt.errorValue(err))
					return
				}

				buf, free, err := sabVal.SharedArrayBufferGetContents()
				if err != nil {
					resolver.Reject(rt.errorValue(err))
					return
				}
				defer free()
				copy(buf, data)

				fn, err := global.Get("createFetchResponse")
				if err != nil {
					val := rt.errorValue(err)
					resolver.Reject(val)
					return
				}

				f, err := fn.AsFunction()
				if err != nil {
					val := rt.errorValue(err)
					resolver.Reject(val)
					return
				}

				resp, err := f.Call(v8go.Undefined(iso), sabVal)
				if err != nil {
					val := rt.errorValue(err)
					resolver.Reject(val)
					return
				}
				resolver.Resolve(resp)
			})
		})
		return resolver.GetPromise().Value
	})
	fetch := fetchFn.GetFunction(rt.context)
	global.Set("fetch", fetch)
}

func (rt *Runtime) polyfillSetTimeout() {
	iso := rt.isolate
	global := rt.context.Global()

	setTimeoutFn := v8go.NewFunctionTemplate(iso, func(info *v8go.FunctionCallbackInfo) *v8go.Value {
		if val := rt.checkArgs(info, 1); val != nil {
			return val
		}
		args := info.Args()
		fnVal := args[0]
		ms := int32(0)
		if len(args) >= 2 {
			ms = args[1].Int32()
		}
		f, _ := fnVal.AsFunction()

		rt.timersMu.Lock()
		timerID := rt.nextTimerID
		rt.nextTimerID++
		rt.timersMu.Unlock()

		timer := time.AfterFunc(time.Duration(ms)*time.Millisecond, func() {
			rt.queueTask("setTimeout", func() {
				rt.mu.Lock()
				closed := rt.closed
				rt.mu.Unlock()
				if !closed {
					_, err := f.Call(v8go.Undefined(iso))
					if err != nil {
						rt.logger.Error("SET_TIMEOUT_ERROR", "error", err)
					}
				}
			})
			rt.timersMu.Lock()
			delete(rt.timers, timerID)
			rt.timersMu.Unlock()
		})

		rt.timersMu.Lock()
		rt.timers[timerID] = timer
		rt.timersMu.Unlock()

		val, _ := v8go.NewValue(iso, int32(timerID))
		return val
	})
	global.Set("setTimeout", setTimeoutFn.GetFunction(rt.context))
}

func (rt *Runtime) polyfillClearTimeout() {
	iso := rt.isolate
	global := rt.context.Global()

	clearTimeoutFn := v8go.NewFunctionTemplate(iso, func(info *v8go.FunctionCallbackInfo) *v8go.Value {
		if val := rt.checkArgs(info, 1); val != nil {
			return val
		}
		args := info.Args()
		timerID := args[0].Int32()

		rt.timersMu.Lock()
		defer rt.timersMu.Unlock()

		if timer, ok := rt.timers[timerID]; ok {
			if t, ok := timer.(*time.Timer); ok {
				t.Stop()
				delete(rt.timers, timerID)
			}
		}

		return nil
	})
	global.Set("clearTimeout", clearTimeoutFn.GetFunction(rt.context))
}

func (rt *Runtime) polyfillSetInterval() {
	iso := rt.isolate
	global := rt.context.Global()

	setIntervalFn := v8go.NewFunctionTemplate(iso, func(info *v8go.FunctionCallbackInfo) *v8go.Value {
		if val := rt.checkArgs(info, 1); val != nil {
			return val
		}
		args := info.Args()
		fnVal := args[0]
		ms := int32(1)
		if len(args) >= 2 {
			ms = args[1].Int32()
		}
		f, _ := fnVal.AsFunction()
		ticker := time.NewTicker(time.Duration(ms) * time.Millisecond)

		rt.timersMu.Lock()
		timerID := rt.nextTimerID
		rt.nextTimerID++
		rt.timers[timerID] = ticker
		rt.timersMu.Unlock()

		rt.spawnGo(func() {
			defer func() {
				ticker.Stop()
				rt.timersMu.Lock()
				delete(rt.timers, timerID)
				rt.timersMu.Unlock()
			}()
			for {
				select {
				case <-ticker.C:
					rt.queueTask("setInterval", func() {
						rt.timersMu.Lock()
						_, ok := rt.timers[timerID]
						rt.timersMu.Unlock()
						if !ok {
							return
						}

						rt.mu.Lock()
						closed := rt.done == nil
						rt.mu.Unlock()
						if !closed {
							_, err := f.Call(v8go.Undefined(iso))
							if err != nil {
								rt.logger.Error("SET_INTERVAL_ERROR", "error", err)
							}
						}
					})
				case <-rt.done:
					return
				}
			}
		})
		val, _ := v8go.NewValue(iso, int32(timerID))
		return val
	})
	global.Set("setInterval", setIntervalFn.GetFunction(rt.context))
}

func (rt *Runtime) polyfillClearInterval() {
	iso := rt.isolate
	global := rt.context.Global()

	clearIntervalFn := v8go.NewFunctionTemplate(iso, func(info *v8go.FunctionCallbackInfo) *v8go.Value {
		if val := rt.checkArgs(info, 1); val != nil {
			return val
		}
		args := info.Args()
		timerID := args[0].Int32()

		rt.timersMu.Lock()
		defer rt.timersMu.Unlock()

		if ticker, ok := rt.timers[timerID]; ok {
			if t, ok := ticker.(*time.Ticker); ok {
				t.Stop()
				delete(rt.timers, timerID)
			}
		}

		return nil
	})
	global.Set("clearInterval", clearIntervalFn.GetFunction(rt.context))
}

func (rt *Runtime) polyfillBtoa() {
	iso := rt.isolate
	global := rt.context.Global()

	btoaFn := v8go.NewFunctionTemplate(iso, func(info *v8go.FunctionCallbackInfo) *v8go.Value {
		if val := rt.checkArgs(info, 1); val != nil {
			return val
		}
		args := info.Args()
		str := args[0].String()
		data := make([]byte, 0, len(str))
		for _, r := range str {
			if r > 255 {
				val, _ := v8go.NewValue(iso, "InvalidCharacterError: The string to be encoded contains characters outside of the Latin1 range.")
				return iso.ThrowException(val)
			}
			data = append(data, byte(r))
		}
		encoded := base64.StdEncoding.EncodeToString(data)
		val, _ := v8go.NewValue(iso, encoded)
		return val
	})
	global.Set("btoa", btoaFn.GetFunction(rt.context))
}

func (rt *Runtime) polyfillAtob() {
	iso := rt.isolate
	global := rt.context.Global()

	atobFn := v8go.NewFunctionTemplate(iso, func(info *v8go.FunctionCallbackInfo) *v8go.Value {
		if val := rt.checkArgs(info, 1); val != nil {
			return val
		}
		args := info.Args()
		encoded := args[0].String()

		// Strip ASCII whitespace
		stripped := make([]byte, 0, len(encoded))
		for i := 0; i < len(encoded); i++ {
			c := encoded[i]
			if c == '\t' || c == '\n' || c == '\f' || c == '\r' || c == ' ' {
				continue
			}
			stripped = append(stripped, c)
		}

		data, err := base64.StdEncoding.DecodeString(string(stripped))
		if err != nil {
			val, _ := v8go.NewValue(iso, "InvalidCharacterError: The string to be decoded is not correctly encoded.")
			return iso.ThrowException(val)
		}

		res := make([]rune, len(data))
		for i, b := range data {
			res[i] = rune(b)
		}

		val, _ := v8go.NewValue(iso, string(res))
		return val
	})
	global.Set("atob", atobFn.GetFunction(rt.context))
}

func (rt *Runtime) polyfillQueueTask() {
	iso := rt.isolate
	global := rt.context.Global()

	queueTaskFn := v8go.NewFunctionTemplate(iso, func(info *v8go.FunctionCallbackInfo) *v8go.Value {
		fnVal := info.Args()[0]
		f, _ := fnVal.AsFunction()
		rt.queueTask("js_task", func() {
			rt.mu.Lock()
			closed := rt.done == nil
			rt.mu.Unlock()
			if !closed {
				_, err := f.Call(v8go.Undefined(iso))
				if err != nil {
					rt.logger.Error("JS_TASK_ERROR", "error", err)
				}
			}
		})
		return nil
	})
	global.Set("__queueTask", queueTaskFn.GetFunction(rt.context))
}

func (rt *Runtime) checkArgs(info *v8go.FunctionCallbackInfo, expected int) *v8go.Value {
	args := info.Args()
	if len(args) < expected {
		msg := fmt.Sprintf("TypeError: %d argument required, but only %d present.", expected, len(args))
		val, _ := v8go.NewValue(rt.isolate, msg)
		return rt.isolate.ThrowException(val)
	}
	return nil
}
