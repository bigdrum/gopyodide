package pyodide

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/tommie/v8go"
)

type task struct {
	name string
	fn   func()
}

type Runtime struct {
	isolate  *v8go.Isolate
	context  *v8go.Context
	assets   map[string][]byte
	tasks    chan task
	done     chan struct{}
	mu       sync.Mutex
	cacheDir string
	logger   *slog.Logger
	waiter   *v8go.Function
}

func New() (*Runtime, error) {
	iso := v8go.NewIsolate()
	rt := &Runtime{
		isolate: iso,
		assets:  make(map[string][]byte),
		tasks:   make(chan task, 100),
		done:    make(chan struct{}),
		logger:  slog.Default(),
	}
	go rt.loop()

	var wg sync.WaitGroup
	wg.Add(1)
	var initErr error
	rt.queueTask("init", func() {
		defer wg.Done()

		rt.context = v8go.NewContext(iso)
		global := rt.context.Global()

		// Console
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

		// importScripts
		importScriptsFn := v8go.NewFunctionTemplate(iso, func(info *v8go.FunctionCallbackInfo) *v8go.Value {
			url := info.Args()[0].String()
			rt.logger.Debug("JS_IMPORT_START", "url", url)
			data, err := rt.FetchAsset(url)
			if err != nil {
				rt.logger.Error("JS_IMPORT_ERROR", "url", url, "error", err)
				val, _ := v8go.NewValue(iso, err.Error())
				return iso.ThrowException(val)
			}
			rt.logger.Debug("JS_IMPORT_DONE", "url", url, "bytes", len(data))
			_, err = rt.context.RunScript(string(data), url)
			if err != nil {
				rt.logger.Error("JS_IMPORT_EXEC_ERROR", "url", url, "error", err)
				val, _ := v8go.NewValue(iso, err.Error())
				return iso.ThrowException(val)
			}
			return nil
		})
		importScripts := importScriptsFn.GetFunction(rt.context)
		global.Set("importScripts", importScripts)

		// Fetch
		fetchFn := v8go.NewFunctionTemplate(iso, func(info *v8go.FunctionCallbackInfo) *v8go.Value {
			args := info.Args()
			if len(args) < 1 {
				return nil
			}
			url := args[0].String()
			rt.logger.Debug("JS_FETCH_URL", "url", url)
			resolver, _ := v8go.NewPromiseResolver(rt.context)
			go func() {
				rt.logger.Debug("JS_FETCH_START", "url", url)
				data, err := rt.FetchAsset(url)
				rt.queueTask("fetch_resolve", func() {
					if err != nil {
						rt.logger.Error("JS_FETCH_ERROR", "url", url, "error", err)
						val, _ := v8go.NewValue(iso, err.Error())
						resolver.Reject(val)
					} else {
						rt.logger.Debug("JS_FETCH_DONE", "url", url, "bytes", len(data))
						hexData := hex.EncodeToString(data)
						arg, _ := v8go.NewValue(iso, hexData)
						fn, _ := rt.context.Global().Get("createFetchResponse")
						f, _ := fn.AsFunction()
						resp, _ := f.Call(v8go.Undefined(iso), arg)
						resolver.Resolve(resp)
					}
				})
			}()
			return resolver.GetPromise().Value
		})
		// setTimeout
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
			time.AfterFunc(time.Duration(ms)*time.Millisecond, func() {
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

		// setInterval
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
			go func() {
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
			}()
			return nil
		})
		global.Set("setInterval", setIntervalFn.GetFunction(rt.context))

		// QueueTask
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

		fetch := fetchFn.GetFunction(rt.context)
		global.Set("fetch", fetch)

		_, initErr = rt.context.RunScript(`
			(function() {
				const g = globalThis;
				
				const createLogger = (name, obj) => {
					return new Proxy(obj, {
						get(target, prop) {
							if (!(prop in target) && typeof prop === 'string' && !prop.startsWith('Symbol')) {
								console.log("MISSING: " + name + "." + prop);
							}
							return target[prop];
						}
					});
				};

				g.URL = class {
					constructor(url, base) {
						if (url && url.includes('://')) {
							this.href = url;
						} else if (base) {
							let b = String(base);
							if (!b.endsWith('/')) b += '/';
							if (url && url.startsWith('/')) url = url.substring(1);
							this.href = b + (url || '');
						} else {
							this.href = url;
						}
					}
					get pathname() {
						try {
							const match = this.href.match(/^https?:\/\/[^\/]+(\/[^?#]*)/);
							return match ? match[1] : (this.href.startsWith('/') ? this.href.split(/[?#]/)[0] : '/');
						} catch(e) { return '/'; }
					}
					toString() { return this.href; }
				};

				const location = {
					href: "http://localhost/",
					origin: "http://localhost",
					protocol: "http:",
					host: "localhost",
					hostname: "localhost",
					port: "",
					pathname: "/",
					search: "",
					hash: "",
					toString() { return this.href; }
				};
				g.location = createLogger("location", location);

				g.navigator = createLogger("navigator", { 
					userAgent: "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
					platform: "MacIntel",
					languages: ["en-US", "en"],
					onLine: true
				});
				
				g.self = g;
				g.window = g;
				g.top = g;
				g.parent = g;

				g.crypto = {
					getRandomValues: (arr) => {
						for (let i = 0; i < arr.length; i++) arr[i] = Math.floor(Math.random() * 256);
						return arr;
					},
					subtle: {
						digest: (algo, data) => Promise.resolve(new Uint8Array(0).buffer)
					}
				};

				g.performance = { now: () => Date.now() };
				g.requestAnimationFrame = (fn) => setTimeout(fn, 16);
				g.cancelAnimationFrame = (id) => {};
				
				g.clearTimeout = (id) => {};
				g.clearInterval = (id) => {};
				
				if (typeof WebAssembly !== 'undefined') {
					const oldInstantiate = WebAssembly.instantiate;
					WebAssembly.instantiate = (bytes, importObject) => {
						const size = bytes.byteLength || (bytes instanceof WebAssembly.Module ? "Module" : "unknown");
						try {
							if (bytes instanceof WebAssembly.Module) {
								const instance = new WebAssembly.Instance(bytes, importObject);
								return Promise.resolve(instance);
							} else {
								const module = new WebAssembly.Module(bytes);
								const instance = new WebAssembly.Instance(module, importObject);
								return Promise.resolve({ module, instance });
							}
						} catch (e) {
							console.log("WASM_INSTANTIATE_FAIL: " + e);
							return Promise.reject(e);
						}
					};
					
					if (!WebAssembly.instantiateStreaming) {
						WebAssembly.instantiateStreaming = async (resp, importObject) => {
							const r = await resp;
							const buffer = await r.arrayBuffer();
							try {
								const module = new WebAssembly.Module(buffer);
								const instance = new WebAssembly.Instance(module, importObject);
								return { module, instance };
							} catch (e) {
								console.log("WASM_INSTANTIATE_ERROR: " + e + "\nSTACK: " + e.stack);
								throw e;
							}
						};
					}
				}

				g.Blob = class {
					constructor(parts, options) {
						this.parts = parts;
						this.options = options;
						this.size = parts.reduce((acc, p) => acc + (p.byteLength || p.size || 0), 0);
					}
					async arrayBuffer() {
						const res = new Uint8Array(this.size);
						let offset = 0;
						for (const p of this.parts) {
							const b = p instanceof Uint8Array ? p : new Uint8Array(await p.arrayBuffer());
							res.set(b, offset);
							offset += b.length;
						}
						return res.buffer;
					}
				};

				g.MessageChannel = class {
					constructor() {
						const createPort = () => {
							const port = {
								onmessage: null,
								_listeners: [],
								addEventListener(ev, fn) {
									if (ev === 'message') this._listeners.push(fn);
								},
								removeEventListener(ev, fn) {
									if (ev === 'message') this._listeners = this._listeners.filter(l => l !== fn);
								},
								postMessage: (msg) => {
									console.log("PORT_POSTMESSAGE");
									__queueTask(() => {
										const other = port._other;
										if (other) {
											const ev = { data: msg, target: other };
											if (other.onmessage) other.onmessage(ev);
											other._listeners.forEach(l => l(ev));
										}
									});
								},
								start() {}
							};
							return port;
						};
						this.port1 = createPort();
						this.port2 = createPort();
						this.port1._other = this.port2;
						this.port2._other = this.port1;
					}
				};

				g.addEventListener = (ev, fn) => {
					console.log("GLOBAL_ADD_EVENT_LISTENER: " + ev);
				};
				g.removeEventListener = (ev, fn) => {};

				const hexTab = new Uint8Array(256);
				for (let i = 0; i < 16; i++) {
					hexTab["0123456789abcdef".charCodeAt(i)] = i;
					hexTab["0123456789ABCDEF".charCodeAt(i)] = i;
				}

				g.Headers = class {
					constructor(init) { this._map = new Map(init ? Object.entries(init) : []); }
					get(n) { return this._map.get(n.toLowerCase()) || null; }
					has(n) { return this._map.has(n.toLowerCase()); }
				};

				g.createFetchResponse = (hex) => {
					const len = hex.length / 2;
					const buffer = new Uint8Array(len);
					for (let i = 0; i < len; i++) {
						buffer[i] = (hexTab[hex.charCodeAt(i * 2)] << 4) | hexTab[hex.charCodeAt(i * 2 + 1)];
					}
					const ab = buffer.buffer;
					return {
						ok: true, status: 200, statusText: "OK",
						url: "http://localhost/asset",
						headers: new g.Headers({ 'content-type': 'application/octet-stream' }),
						arrayBuffer: () => { return Promise.resolve(ab); },
						json: () => Promise.resolve(JSON.parse(new g.TextDecoder().decode(buffer))),
						text: () => Promise.resolve(new g.TextDecoder().decode(buffer)),
						clone() { return this; }
					};
				};

				g.XMLHttpRequest = class {
					constructor() {
						this.readyState = 0;
						this.status = 0;
						this.response = null;
						this.onload = null;
						this.onerror = null;
					}
					open(method, url) { this.url = url; this.readyState = 1; }
					send() {
						fetch(this.url).then(r => {
							this.status = r.status;
							return r.arrayBuffer();
						}).then(ab => {
							this.response = ab;
							this.readyState = 4;
							if (this.onload) this.onload();
						}).catch(e => {
							if (this.onerror) this.onerror(e);
						});
					}
					setRequestHeader() {}
					getResponseHeader() { return null; }
				};

				g.TextEncoder = class {
					encode(s) {
						const arr = new Uint8Array(s.length);
						for (let i = 0; i < s.length; i++) arr[i] = s.charCodeAt(i);
						return arr;
					}
				};

				g.TextDecoder = class {
					decode(arr) {
						if (!arr) return "";
						const CHUNK_SIZE = 8192;
						let s = "";
						const view = (arr instanceof Uint8Array) ? arr : new Uint8Array(arr);
						for (let i = 0; i < view.length; i += CHUNK_SIZE) {
							s += String.fromCharCode.apply(null, view.subarray(i, i + CHUNK_SIZE));
						}
						return s;
					}
				};

				g.__initDocument = () => {
					const createMockElement = (tag) => createLogger("el_" + tag, {
						tagName: tag.toUpperCase(),
						src: "",
						appendChild(el) {
							if (el.tagName === 'SCRIPT' && el.src) {
								importScripts(el.src);
								if (el.onload) el.onload();
							}
						},
						setAttribute(n, v) { this[n] = v; },
						getAttribute(n) { return this[n]; },
						style: {},
						addEventListener(ev, fn) { if (ev === 'load') this.onload = fn; },
						removeEventListener: () => {}
					});

					const head = createMockElement('HEAD');
					const document = { 
						currentScript: null, 
						createElement: (tag) => createMockElement(tag),
						getElementsByTagName: (tag) => {
							if (tag.toUpperCase() === 'HEAD') return [head];
							return [createMockElement(tag)];
						},
						head: head,
						body: createMockElement('BODY'),
						createTextNode: () => ({}),
						cookie: ""
					};
					g.document = createLogger("document", document);
					g.URL.createObjectURL = (obj) => "blob:mock";
					g.URL.revokeObjectURL = (url) => {};
					console.log("Document polyfilled (deferred)");
				};

				console.log("Environment polyfilled (Base)");
			})();
		`, "init.js")

		waiterVal, _ := rt.context.RunScript(`(p, res, rej) => { p.then(res).catch(rej); }`, "await.js")
		rt.waiter, _ = waiterVal.AsFunction()
	})
	wg.Wait()
	return rt, initErr
}

func (rt *Runtime) loop() {
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case t := <-rt.tasks:
			rt.logger.Debug("Runtime loop start", "task", t.name)
			t.fn()
			rt.logger.Debug("Runtime loop done", "task", t.name)
			if rt.context != nil {
				rt.context.PerformMicrotaskCheckpoint()
			}
		case <-ticker.C:
			if rt.context != nil {
				rt.context.PerformMicrotaskCheckpoint()
			}
			// fmt.Printf("Tick\n")
		case <-rt.done:
			return
		}
	}
}

func (rt *Runtime) queueTask(name string, fn func()) {
	rt.tasks <- task{name: name, fn: fn}
}

func (rt *Runtime) runTask(name string, fn func() error) error {
	var err error
	var wg sync.WaitGroup
	wg.Add(1)
	rt.queueTask(name, func() {
		defer wg.Done()
		err = fn()
	})
	wg.Wait()
	return err
}

func (rt *Runtime) await(val *v8go.Value, onResolve func(*v8go.Value), onReject func(error)) {
	prom, err := val.AsPromise()
	if err != nil {
		onResolve(val)
		return
	}

	resolve := v8go.NewFunctionTemplate(rt.isolate, func(info *v8go.FunctionCallbackInfo) *v8go.Value {
		var res *v8go.Value
		if len(info.Args()) > 0 {
			res = info.Args()[0]
		}
		onResolve(res)
		return nil
	}).GetFunction(rt.context)

	reject := v8go.NewFunctionTemplate(rt.isolate, func(info *v8go.FunctionCallbackInfo) *v8go.Value {
		var msg string
		if len(info.Args()) > 0 {
			msg = info.Args()[0].String()
		}
		onReject(errors.New(msg))
		return nil
	}).GetFunction(rt.context)

	rt.waiter.Call(v8go.Undefined(rt.isolate), prom.Value, resolve.Value, reject.Value)
}

func (rt *Runtime) FetchAsset(url string) ([]byte, error) {
	rt.logger.Debug("FetchAsset", "url", url)
	rt.mu.Lock()
	if d, ok := rt.assets[url]; ok {
		rt.mu.Unlock()
		rt.logger.Debug("FetchAsset hit", "url", url)
		return d, nil
	}
	rt.logger.Debug("FetchAsset miss", "url", url)
	cacheDir := rt.cacheDir
	rt.mu.Unlock()

	if cacheDir != "" {
		hash := sha256.Sum256([]byte(url))
		cachePath := filepath.Join(cacheDir, hex.EncodeToString(hash[:]))
		if d, err := os.ReadFile(cachePath); err == nil {
			rt.mu.Lock()
			rt.assets[url] = d
			rt.mu.Unlock()
			rt.logger.Debug("FetchAsset cache hit", "url", url)
			return d, nil
		}
	}

	rt.logger.Info("FetchAsset http", "url", url)
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(url)
	rt.logger.Info("FetchAsset http done", "url", url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, url)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if cacheDir != "" {
		hash := sha256.Sum256([]byte(url))
		cachePath := filepath.Join(cacheDir, hex.EncodeToString(hash[:]))
		_ = os.MkdirAll(cacheDir, 0755)
		_ = os.WriteFile(cachePath, data, 0644)
	}

	rt.mu.Lock()
	rt.assets[url] = data
	rt.mu.Unlock()

	return data, nil
}

func (rt *Runtime) SetCacheDir(path string) {
	rt.mu.Lock()
	rt.cacheDir = path
	rt.mu.Unlock()
}

func (rt *Runtime) SetLogger(l *slog.Logger) {
	rt.mu.Lock()
	rt.logger = l
	rt.mu.Unlock()
}

func (rt *Runtime) SetAsset(url string, data []byte) {
	rt.mu.Lock()
	rt.assets[url] = data
	rt.mu.Unlock()
}

func (rt *Runtime) LoadPyodide(jsSource, indexURL string) error {
	rt.logger.Info("LoadPyodide: starting", "jsSource_len", len(jsSource), "indexURL", indexURL)

	errCh := make(chan error, 1)

	rt.queueTask("LoadPyodide", func() {
		rt.logger.Debug("LoadPyodide task: running jsSource")
		_, err := rt.context.RunScript(jsSource, "pyodide.js")
		if err != nil {
			errCh <- fmt.Errorf("jsSource failed: %w", err)
			return
		}

		rt.logger.Debug("LoadPyodide task: running __initDocument")
		_, err = rt.context.RunScript("__initDocument();", "init_doc.js")
		if err != nil {
			errCh <- fmt.Errorf("__initDocument failed: %w", err)
			return
		}

		script := fmt.Sprintf(`
			globalThis.loadPyodide({ indexURL: %q })
				.then(p => { 
					console.log("loadPyodide promise resolved internally");
					globalThis.pyodide = p; 
					return "OK"; 
				})
				.catch(e => {
					console.log("loadPyodide promise rejected internally: " + e + "\nSTACK: " + e.stack);
					throw e;
				});
		`, indexURL)

		rt.logger.Debug("LoadPyodide task: running loadPyodide script")
		val, err := rt.context.RunScript(script, "load.js")
		if err != nil {
			errCh <- fmt.Errorf("loadPyodide script failed: %w", err)
			return
		}

		rt.await(val, func(v *v8go.Value) {
			errCh <- nil
		}, func(err error) {
			errCh <- err
		})
	})

	return <-errCh
}

func (rt *Runtime) LoadPackage(name string) error {
	rt.logger.Info("LoadPackage: starting", "package", name)

	errCh := make(chan error, 1)

	rt.queueTask("LoadPackage", func() {
		script := fmt.Sprintf(`
			(() => {
				if (!globalThis.pyodide) {
					throw new Error("Pyodide not initialized");
				}
				console.log("LoadPackage: running loadPackage script");
				return globalThis.pyodide.loadPackage(%q).then(() => {
					console.log("LoadPackage: done");
					return "OK";
				}).catch(e => {
					console.log("LoadPackage: promise rejected internally: " + e + "\nSTACK: " + e.stack);
					throw e;
				});
			})()
		`, name)

		val, err := rt.context.RunScript(script, "load_package.js")
		if err != nil {
			errCh <- err
			return
		}

		rt.await(val, func(v *v8go.Value) {
			errCh <- nil
		}, func(err error) {
			errCh <- err
		})
	})

	select {
	case err := <-errCh:
		return err
	case <-time.After(120 * time.Second):
		return errors.New("LoadPackage timeout after 120s")
	}
}

func (rt *Runtime) Run(code string) (string, error) {
	rt.logger.Info("Run: starting", "code_len", len(code))

	type result struct {
		res string
		err error
	}
	resCh := make(chan result, 1)

	rt.queueTask("Run", func() {
		rt.logger.Debug("Run task: running script")
		script := fmt.Sprintf(`
			(async () => {
				if (!globalThis.pyodide) {
					throw new Error("Pyodide not initialized");
				}
				return await globalThis.pyodide.runPython(%q);
			})()
		`, code)

		val, err := rt.context.RunScript(script, "run.js")
		if err != nil {
			rt.logger.Error("Run task: script run failed", "error", err)
			resCh <- result{err: err}
			return
		}

		rt.await(val, func(v *v8go.Value) {
			resCh <- result{res: v.String()}
		}, func(err error) {
			resCh <- result{err: err}
		})
	})

	res := <-resCh
	return res.res, res.err
}

func (rt *Runtime) Close() {
	rt.mu.Lock()
	if rt.done != nil {
		close(rt.done)
		rt.done = nil
	}
	rt.mu.Unlock()
	// Give the loop a chance to exit before closing context
	time.Sleep(50 * time.Millisecond)
	rt.context.Close()
	rt.isolate.Dispose()
}
