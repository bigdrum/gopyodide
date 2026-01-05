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

	_ "embed"

	"github.com/tommie/v8go"
)

//go:embed init.js
var initJS string

type task struct {
	name string
	fn   func()
}

type Runtime struct {
	isolate       *v8go.Isolate
	context       *v8go.Context
	assets        map[string][]byte
	tasks         chan task
	done          chan struct{}
	mu            sync.Mutex
	timersMu      sync.Mutex
	timers        map[int32]interface{}
	nextTimerID   int32
	cacheDir      string
	logger        *slog.Logger
	waiter        *v8go.Function
	loopWg        sync.WaitGroup
	wg            sync.WaitGroup
	activeTask    string
	closed        bool
	interruptBuf  []byte
	interruptFree func()
}

func New() (*Runtime, error) {
	iso := v8go.NewIsolate()
	rt := &Runtime{
		isolate: iso,
		assets:  make(map[string][]byte),
		tasks:   make(chan task, 100),
		done:    make(chan struct{}),
		logger:  slog.Default(),
		timers:  make(map[int32]interface{}),
	}
	return rt, nil
}

func (rt *Runtime) Start() error {
	rt.loopWg.Add(1)
	go rt.loop()
	rt.loopWg.Add(1)
	go rt.watchdog()

	var wg sync.WaitGroup
	wg.Add(1)
	var initErr error
	rt.queueTask("init", func() {
		defer wg.Done()

		rt.context = v8go.NewContext(rt.isolate)
		rt.polyfill()

		_, initErr = rt.context.RunScript(initJS, "init.js")

		waiterVal, _ := rt.context.RunScript(`(p, res, rej) => { p.then(res).catch(rej); }`, "await.js")
		rt.waiter, _ = waiterVal.AsFunction()
	})
	wg.Wait()
	return initErr
}

func (rt *Runtime) loop() {
	defer rt.loopWg.Done()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case t := <-rt.tasks:
			rt.mu.Lock()
			rt.activeTask = t.name
			rt.mu.Unlock()
			rt.logger.Debug("Runtime loop start", "task", t.name)
			t.fn()
			rt.logger.Debug("Runtime loop done", "task", t.name)
			rt.mu.Lock()
			rt.activeTask = ""
			rt.mu.Unlock()
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

func (rt *Runtime) watchdog() {
	defer rt.loopWg.Done()
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			if n := len(rt.tasks); n > 0 {
				rt.mu.Lock()
				at := rt.activeTask
				rt.mu.Unlock()
				rt.logger.Info("Watchdog", "queue_len", n, "active_task", at)
			}
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
		return d, nil
	}
	cacheDir := rt.cacheDir
	rt.mu.Unlock()

	if cacheDir != "" {
		hash := sha256.Sum256([]byte(url))
		cachePath := filepath.Join(cacheDir, hex.EncodeToString(hash[:]))
		if d, err := os.ReadFile(cachePath); err == nil {
			rt.mu.Lock()
			rt.assets[url] = d
			rt.mu.Unlock()
			rt.logger.Info("FetchAsset local cache hit", "url", url)
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
		if err := os.MkdirAll(cacheDir, 0755); err != nil {
			rt.logger.Warn("FetchAsset: failed to create cache dir", "dir", cacheDir, "error", err)
		} else if err := os.WriteFile(cachePath, data, 0644); err != nil {
			rt.logger.Warn("FetchAsset: failed to write cache file", "path", cachePath, "error", err)
		}
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

func (rt *Runtime) LoadPyodide(indexURL string) error {
	rt.logger.Info("LoadPyodide: starting", "indexURL", indexURL)

	jsSource, err := rt.FetchAsset(indexURL + "pyodide.js")
	if err != nil {
		return err
	}
	errCh := make(chan error, 1)

	rt.queueTask("LoadPyodide", func() {
		rt.logger.Debug("LoadPyodide task: running jsSource")
		_, err := rt.context.RunScript(string(jsSource), "pyodide.js")
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
					const sab = new SharedArrayBuffer(16);
					const int32View = new Int32Array(sab);
					p.setInterruptBuffer(int32View);
					return sab; 
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
			if v.IsSharedArrayBuffer() {
				buf, free, _ := v.SharedArrayBufferGetContents()
				rt.interruptBuf = buf
				rt.interruptFree = free
				rt.logger.Info("LoadPyodide: interrupt buffer initialized", "len", len(buf))
			} else {
				rt.logger.Warn("LoadPyodide: returned value is not SharedArrayBuffer")
			}
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

func (rt *Runtime) Run(ctx context.Context, code string) (string, error) {
	rt.logger.Info("Run: starting", "code_len", len(code))

	if rt.interruptBuf != nil && len(rt.interruptBuf) > 0 {
		rt.interruptBuf[0] = 0
	}

	ctxDone := make(chan struct{})
	defer close(ctxDone)
	if rt.interruptBuf != nil {
		go func() {
			select {
			case <-ctx.Done():
				// Trigger execution interrupt
				if len(rt.interruptBuf) > 0 {
					rt.interruptBuf[0] = 2
					rt.logger.Info("Run: context cancelled, sent interrupt")
				}
			case <-ctxDone:
			}
		}()
	}

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

	select {
	case res := <-resCh:
		rt.logger.Info("Run done.")
		return res.res, res.err
	case <-ctx.Done():
		// If context is cancelled, we still wait for the result from resCh because we triggered interrupt.
		// Pyodide should throw KeyboardInterrupt and return.
		// However, we shouldn't block forever if something goes wrong.
		// But user requirement says: "wait for runPython to return".
		rt.logger.Info("Run: waiting for runPython to return after interrupt")
		res := <-resCh
		rt.logger.Info("Run: runPython returned after interrupt")
		return res.res, res.err
	}
}

func (rt *Runtime) spawnGo(fn func()) {
	rt.wg.Add(1)
	go func() {
		defer rt.wg.Done()
		fn()
	}()
}

func (rt *Runtime) Close() {
	rt.mu.Lock()
	if rt.closed {
		rt.mu.Unlock()
		return
	}
	rt.closed = true
	rt.mu.Unlock()
	close(rt.done)

	rt.timersMu.Lock()
	for id, t := range rt.timers {
		switch T := t.(type) {
		case *time.Timer:
			T.Stop()
		case *time.Ticker:
			T.Stop()
		}
		delete(rt.timers, id)
	}
	rt.timersMu.Unlock()

	if rt.interruptFree != nil {
		rt.interruptFree()
		rt.interruptFree = nil
		rt.interruptBuf = nil
	}

	rt.wg.Wait()
	rt.loopWg.Wait()
	rt.context.Close()
	rt.isolate.Dispose()
}
