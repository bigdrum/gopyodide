package pyodide

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"log/slog"
	"time"

	"github.com/tommie/v8go"
)

func (rt *Runtime) polyfill() {
	rt.polyfillConsole()
	rt.polyfillImportScripts()
	rt.polyfillFetch()
	rt.polyfillSetTimeout()
	rt.polyfillSetInterval()
	rt.polyfillBtoa()
	rt.polyfillQueueTask()
}

func (rt *Runtime) polyfillConsole() {
	iso := rt.isolate
	global := rt.context.Global()

	logFn := v8go.NewFunctionTemplate(iso, func(info *v8go.FunctionCallbackInfo) *v8go.Value {
		if rt.logger.Enabled(context.Background(), slog.LevelInfo) {
			args := info.Args()
			s := ""
			for i, a := range args {
				if i > 0 {
					s += " "
				}
				s += a.String()
			}
			rt.logger.Info("JS_LOG", "message", s)
		}
		return nil
	})
	consoleObjTempl := v8go.NewObjectTemplate(iso)
	consoleObjTempl.Set("log", logFn)
	consoleObjTempl.Set("error", logFn)
	consoleObjTempl.Set("warn", logFn)
	consoleObjTempl.Set("info", logFn)
	consoleObjTempl.Set("debug", logFn)
	consoleObj, _ := consoleObjTempl.NewInstance(rt.context)
	global.Set("console", consoleObj)
}

func (rt *Runtime) polyfillImportScripts() {
	iso := rt.isolate
	global := rt.context.Global()

	importScriptsFn := v8go.NewFunctionTemplate(iso, func(info *v8go.FunctionCallbackInfo) *v8go.Value {
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
		args := info.Args()
		resolver, _ := v8go.NewPromiseResolver(rt.context)

		if len(args) < 1 {
			val, _ := v8go.NewValue(iso, "TypeError: 1 argument required, but only 0 present.")
			resolver.Reject(iso.ThrowException(val))
			return resolver.GetPromise().Value
		}
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
				hexData := hex.EncodeToString(data)
				arg, err := v8go.NewValue(iso, hexData)
				if err != nil {
					val := rt.errorValue(err)
					resolver.Reject(val)
					return
				}

				fn, err := rt.context.Global().Get("createFetchResponse")
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

				resp, err := f.Call(v8go.Undefined(iso), arg)
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
		args := info.Args()
		if len(args) < 1 {
			return nil
		}
		fnVal := args[0]
		ms := int32(0)
		if len(args) >= 2 {
			ms = args[1].Int32()
		}
		f, _ := fnVal.AsFunction()
		rt.spawnGo(func() {
			time.Sleep(time.Duration(ms) * time.Millisecond)
			rt.queueTask("setTimeout", func() {
				rt.mu.Lock()
				closed := rt.done == nil
				rt.mu.Unlock()
				if !closed {
					_, err := f.Call(v8go.Undefined(iso))
					if err != nil {
						rt.logger.Error("SET_TIMEOUT_ERROR", "error", err)
					}
				}
			})
		})
		return nil
	})
	global.Set("setTimeout", setTimeoutFn.GetFunction(rt.context))
}

func (rt *Runtime) polyfillSetInterval() {
	iso := rt.isolate
	global := rt.context.Global()

	setIntervalFn := v8go.NewFunctionTemplate(iso, func(info *v8go.FunctionCallbackInfo) *v8go.Value {
		args := info.Args()
		if len(args) < 1 {
			return nil
		}
		fnVal := args[0]
		ms := int32(0)
		if len(args) >= 2 {
			ms = args[1].Int32()
		}
		f, _ := fnVal.AsFunction()
		ticker := time.NewTicker(time.Duration(ms) * time.Millisecond)
		rt.spawnGo(func() {
			for {
				select {
				case <-ticker.C:
					rt.queueTask("setInterval", func() {
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
					ticker.Stop()
					return
				}
			}
		})
		return nil
	})
	global.Set("setInterval", setIntervalFn.GetFunction(rt.context))
}

func (rt *Runtime) polyfillBtoa() {
	iso := rt.isolate
	global := rt.context.Global()

	btoaFn := v8go.NewFunctionTemplate(iso, func(info *v8go.FunctionCallbackInfo) *v8go.Value {
		args := info.Args()
		if len(args) < 1 {
			val, _ := v8go.NewValue(iso, "TypeError: 1 argument required, but only 0 present.")
			return iso.ThrowException(val)
		}
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
