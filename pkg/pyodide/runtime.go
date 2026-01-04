package pyodide

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"rogchap.com/v8go"
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
}

func New() (*Runtime, error) {
	iso := v8go.NewIsolate()
	rt := &Runtime{
		isolate: iso,
		assets:  make(map[string][]byte),
		tasks:   make(chan task, 100),
		done:    make(chan struct{}),
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
			args := info.Args()
			s := ""
			for i, a := range args {
				if i > 0 {
					s += " "
				}
				s += a.String()
			}
			fmt.Printf("JS_LOG: %s\n", s)
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
			fmt.Printf("JS_IMPORT_START: %s\n", url)
			data, err := rt.FetchAsset(url)
			if err != nil {
				fmt.Printf("JS_IMPORT_ERROR: %s - %v\n", url, err)
				val, _ := v8go.NewValue(iso, err.Error())
				return iso.ThrowException(val)
			}
			fmt.Printf("JS_IMPORT_DONE: %s (%d bytes)\n", url, len(data))
			_, err = rt.context.RunScript(string(data), url)
			if err != nil {
				fmt.Printf("JS_IMPORT_EXEC_ERROR: %s - %v\n", url, err)
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
			fmt.Printf("JS_FETCH_URL: %s\n", url)
			resolver, _ := v8go.NewPromiseResolver(rt.context)
			go func() {
				fmt.Printf("JS_FETCH_START: %s\n", url)
				data, err := rt.FetchAsset(url)
				rt.queueTask("fetch_resolve", func() {
					if err != nil {
						fmt.Printf("JS_FETCH_ERROR: %s - %v\n", url, err)
						val, _ := v8go.NewValue(iso, err.Error())
						resolver.Reject(val)
					} else {
						fmt.Printf("JS_FETCH_DONE: %s (%d bytes)\n", url, len(data))
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
							fmt.Printf("SET_TIMEOUT_ERROR: %v\n", err)
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
									fmt.Printf("SET_INTERVAL_ERROR: %v\n", err)
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
						fmt.Printf("JS_TASK_ERROR: %v\n", err)
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
						console.log("WASM_INSTANTIATE: " + size);
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
						console.log("Polyfilling WebAssembly.instantiateStreaming");
						WebAssembly.instantiateStreaming = async (resp, importObject) => {
							console.log("WASM_STREAMING_START");
							const r = await resp;
							const buffer = await r.arrayBuffer();
							console.log("WASM_INSTANTIATE_START: " + buffer.byteLength);
							try {
								const module = new WebAssembly.Module(buffer);
								console.log("WASM_MODULE_CREATED");
								const instance = new WebAssembly.Instance(module, importObject);
								console.log("WASM_INSTANTIATE_DONE");
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
					console.log("DECODE_START: " + hex.length);
					const len = hex.length / 2;
					const buffer = new Uint8Array(len);
					for (let i = 0; i < len; i++) {
						if (i % 500000 === 0) console.log("DECODE_PROGRESS: " + i + "/" + len);
						buffer[i] = (hexTab[hex.charCodeAt(i * 2)] << 4) | hexTab[hex.charCodeAt(i * 2 + 1)];
					}
					console.log("DECODE_DONE");
					const ab = buffer.buffer;
					return {
						ok: true, status: 200, statusText: "OK",
						url: "http://localhost/asset",
						headers: new g.Headers({ 'content-type': 'application/octet-stream' }),
						arrayBuffer: () => { console.log("FETCH_ARRAYBUFFER_CALLED"); return Promise.resolve(ab); },
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
			fmt.Printf("Runtime loop start: %s\n", t.name)
			t.fn()
			fmt.Printf("Runtime loop done: %s\n", t.name)
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

func (rt *Runtime) FetchAsset(url string) ([]byte, error) {
	fmt.Println("FetchAsset: " + url)
	rt.mu.Lock()
	if d, ok := rt.assets[url]; ok {
		rt.mu.Unlock()
		fmt.Println("FetchAsset hit: " + url)
		return d, nil
	}
	fmt.Println("FetchAsset miss: " + url)
	cacheDir := rt.cacheDir
	rt.mu.Unlock()

	if cacheDir != "" {
		hash := sha256.Sum256([]byte(url))
		cachePath := filepath.Join(cacheDir, hex.EncodeToString(hash[:]))
		if d, err := os.ReadFile(cachePath); err == nil {
			rt.mu.Lock()
			rt.assets[url] = d
			rt.mu.Unlock()
			fmt.Println("FetchAsset cache hit: " + url)
			return d, nil
		}
	}

	fmt.Println("FetchAsset http: " + url)
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(url)
	fmt.Println("FetchAsset http done: " + url)
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

func (rt *Runtime) SetAsset(url string, data []byte) {
	rt.mu.Lock()
	rt.assets[url] = data
	rt.mu.Unlock()
}

func (rt *Runtime) LoadPyodide(jsSource, indexURL string) error {
	fmt.Printf("LoadPyodide: starting (jsSource len=%d, indexURL=%s)\n", len(jsSource), indexURL)

	errCh := make(chan error, 1)

	rt.queueTask("LoadPyodide", func() {
		fmt.Println("LoadPyodide task: running jsSource")
		_, err := rt.context.RunScript(jsSource, "pyodide.js")
		if err != nil {
			errCh <- fmt.Errorf("jsSource failed: %w", err)
			return
		}

		fmt.Println("LoadPyodide task: running __initDocument")
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

		fmt.Println("LoadPyodide task: running loadPyodide script")
		val, err := rt.context.RunScript(script, "load.js")
		if err != nil {
			errCh <- fmt.Errorf("loadPyodide script failed: %w", err)
			return
		}

		prom, err := val.AsPromise()
		if err != nil {
			fmt.Println("LoadPyodide task: result is not a promise, done")
			errCh <- nil
			return
		}

		fmt.Println("LoadPyodide task: attaching promise callbacks")
		resolve := v8go.NewFunctionTemplate(rt.isolate, func(info *v8go.FunctionCallbackInfo) *v8go.Value {
			fmt.Println("LoadPyodide task: Go resolve callback called")
			errCh <- nil
			return nil
		}).GetFunction(rt.context)

		reject := v8go.NewFunctionTemplate(rt.isolate, func(info *v8go.FunctionCallbackInfo) *v8go.Value {
			msg := info.Args()[0].String()
			fmt.Printf("LoadPyodide task: Go reject callback called: %s\n", msg)
			errCh <- errors.New(msg)
			return nil
		}).GetFunction(rt.context)

		waiter, _ := rt.context.RunScript(`(p, res, rej) => { p.then(res).catch(rej); }`, "await.js")
		f, _ := waiter.AsFunction()
		f.Call(v8go.Undefined(rt.isolate), prom.Value, resolve.Value, reject.Value)
		fmt.Println("LoadPyodide task: yielding loop to wait for async work")
	})

	return <-errCh
}

func (rt *Runtime) LoadPackage(name string) error {
	fmt.Printf("LoadPackage: starting (%s)\n", name)

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

		prom, err := val.AsPromise()
		if err != nil {
			errCh <- nil
			return
		}

		resolve := v8go.NewFunctionTemplate(rt.isolate, func(info *v8go.FunctionCallbackInfo) *v8go.Value {
			fmt.Println("LoadPackage: Go resolve callback called")
			errCh <- nil
			return nil
		}).GetFunction(rt.context)

		reject := v8go.NewFunctionTemplate(rt.isolate, func(info *v8go.FunctionCallbackInfo) *v8go.Value {
			fmt.Println("LoadPackage: Go reject callback called", info.Args()[0].String())
			errCh <- errors.New(info.Args()[0].String())
			return nil
		}).GetFunction(rt.context)

		waiter, _ := rt.context.RunScript(`(p, res, rej) => { p.then(res).catch(rej); }`, "await.js")
		f, _ := waiter.AsFunction()
		f.Call(v8go.Undefined(rt.isolate), prom.Value, resolve.Value, reject.Value)
		fmt.Println("LoadPackage: yielding loop to wait for async work")
	})

	select {
	case err := <-errCh:
		return err
	case <-time.After(120 * time.Second):
		return errors.New("LoadPackage timeout after 120s")
	}
}

func (rt *Runtime) Run(code string) (string, error) {
	fmt.Printf("Run: starting (code len=%d)\n", len(code))

	type result struct {
		res string
		err error
	}
	resCh := make(chan result, 1)

	rt.queueTask("Run", func() {
		fmt.Println("Run task: running script")
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
			fmt.Printf("Run task: script run failed: %v\n", err)
			resCh <- result{err: err}
			return
		}

		prom, err := val.AsPromise()
		if err != nil {
			fmt.Println("Run task: result is not a promise, returning string value")
			resCh <- result{res: val.String()}
			return
		}

		fmt.Println("Run task: attaching promise callbacks")
		resolve := v8go.NewFunctionTemplate(rt.isolate, func(info *v8go.FunctionCallbackInfo) *v8go.Value {
			fmt.Println("Run task: Go resolve callback called")
			resCh <- result{res: info.Args()[0].String()}
			return nil
		}).GetFunction(rt.context)

		reject := v8go.NewFunctionTemplate(rt.isolate, func(info *v8go.FunctionCallbackInfo) *v8go.Value {
			msg := info.Args()[0].String()
			fmt.Printf("Run task: Go reject callback called: %s\n", msg)
			resCh <- result{err: errors.New(msg)}
			return nil
		}).GetFunction(rt.context)

		waiter, _ := rt.context.RunScript(`(p, res, rej) => { p.then(res).catch(rej); }`, "await.js")
		f, _ := waiter.AsFunction()
		f.Call(v8go.Undefined(rt.isolate), prom.Value, resolve.Value, reject.Value)
		fmt.Println("Run task: yielding loop to wait for async work")
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
