(function () {
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

	// Simplified URL polyfill
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
			} catch (e) { return '/'; }
		}
		toString() { return this.href; }
	};

	// Simplified location polyfill
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

	// Simplified navigator polyfill
	g.navigator = createLogger("navigator", {
		userAgent: "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
		platform: "MacIntel",
		languages: ["en-US", "en"],
		onLine: true
	});

	// Pretending to be a Web Worker for newer Pyodide versions
	g.WorkerGlobalScope = class WorkerGlobalScope { };
	try {
		Object.setPrototypeOf(g, g.WorkerGlobalScope.prototype);
	} catch (e) {
		// Fallback for environments where setPrototypeOf on globalThis is restricted
		console.warn("Failed to set prototype of globalThis: " + e);
	}

	g.self = g;
	g.window = g;
	g.top = g;
	g.parent = g;

	// Oversimplified crypto implementation
	g.crypto = {
		getRandomValues: (arr) => {
			for (let i = 0; i < arr.length; i++) arr[i] = Math.floor(Math.random() * 256);
			return arr;
		},
		subtle: {
			digest: (algo, data) => Promise.resolve(new Uint8Array(0).buffer) // Returns empty buffer
		}
	};

	// Simplified performance polyfill
	g.performance = { now: () => Date.now() };
	g.requestAnimationFrame = (fn) => setTimeout(fn, 16);
	g.cancelAnimationFrame = (id) => { };

	// Simplified WebAssembly polyfills to handle different instantiation patterns
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

	// Simplified Blob polyfill
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

	// Simplified MessageChannel polyfill
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
						__queueTask(() => {
							const other = port._other;
							if (other) {
								const ev = { data: msg, target: other };
								if (other.onmessage) other.onmessage(ev);
								other._listeners.forEach(l => l(ev));
							}
						});
					},
					start() { }
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
		console.debug("GLOBAL_ADD_EVENT_LISTENER (unimplemented): " + ev);
	};
	g.removeEventListener = (ev, fn) => { };

	g.__createSAB = (size) => new SharedArrayBuffer(size);

	// Simplified Headers polyfill
	g.Headers = class {
		constructor(init) { this._map = new Map(init ? Object.entries(init) : []); }
		get(n) { return this._map.get(n.toLowerCase()) || null; }
		has(n) { return this._map.has(n.toLowerCase()); }
	};

	// Simplified Fetch Response creation
	g.createFetchResponse = (sab) => {
		// WebAssembly and some other APIs don't accept SharedArrayBuffer directly.
		// We copy it to a regular ArrayBuffer. Still MUCH faster than hex/base64.
		const ab = new ArrayBuffer(sab.byteLength);
		new Uint8Array(ab).set(new Uint8Array(sab));
		const buffer = new Uint8Array(ab);

		const resp = {
			ok: true, status: 200, statusText: "OK",
			url: "http://localhost/asset",
			headers: new g.Headers({ 'content-type': 'application/octet-stream' }),
			bodyUsed: false,
			_rawBuffer: ab,
			arrayBuffer: () => {
				resp.bodyUsed = true;
				return Promise.resolve(ab);
			},
			json: () => {
				resp.bodyUsed = true;
				return Promise.resolve(JSON.parse(new g.TextDecoder().decode(buffer)));
			},
			text: () => {
				resp.bodyUsed = true;
				return Promise.resolve(new g.TextDecoder().decode(buffer));
			},
			clone() { return { ...resp }; }
		};
		return resp;
	};

	// Simplified AbortController and AbortSignal polyfill
	g.AbortSignal = class {
		constructor() {
			this.aborted = false;
			this._listeners = [];
			this.reason = undefined;
			this.onabort = null;
		}
		throwIfAborted() {
			if (this.aborted) {
				throw this.reason || new Error("Aborted");
			}
		}
		addEventListener(ev, fn) {
			if (ev === 'abort') {
				if (this.aborted) fn();
				else this._listeners.push(fn);
			}
		}
		removeEventListener(ev, fn) {
			if (ev === 'abort') {
				this._listeners = this._listeners.filter(l => l !== fn);
			}
		}
	};

	g.AbortController = class {
		constructor() {
			this.signal = new g.AbortSignal();
		}
		abort(reason) {
			if (this.signal.aborted) return;
			this.signal.aborted = true;
			this.signal.reason = reason;
			if (this.signal.onabort) this.signal.onabort();
			this.signal._listeners.forEach(fn => fn());
		}
	};

	// Simplified XMLHttpRequest polyfill using fetch
	g.XMLHttpRequest = class {
		constructor() {
			this.readyState = 0;
			this.status = 0;
			this.response = null;
			this.responseText = "";
			this.responseType = "";
			this.onload = null;
			this.onerror = null;
			this.async = true;
		}
		open(method, url, async = true) {
			this.url = url;
			this.async = async;
			this.readyState = 1;
		}
		send() {
			const handleResponse = (r) => {
				this.status = r.status;
				const ab = r._rawBuffer || r; // fallback if it's already an arraybuffer
				this.responseText = new g.TextDecoder().decode(ab);
				if (this.responseType === 'arraybuffer') {
					this.response = ab;
				} else {
					this.response = this.responseText;
				}
				this.readyState = 4;
				if (this.onload) this.onload();
			};

			if (this.async) {
				fetch(this.url).then(r => {
					this.status = r.status;
					return r.arrayBuffer();
				}).then(ab => {
					handleResponse(ab);
				}).catch(e => {
					if (this.onerror) this.onerror(e);
				});
			} else {
				try {
					const r = __fetchSync(this.url);
					handleResponse(r);
				} catch (e) {
					if (this.onerror) this.onerror(e);
				}
			}
		}
		setRequestHeader() { }
		getResponseHeader() { return null; }
	};

	// Simplified TextEncoder/TextDecoder - only supports ASCII/UTF-8 correctly for single bytes
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

	// Simplified document and script loading polyfills
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
			removeEventListener: () => { }
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
		g.URL.revokeObjectURL = (url) => { };
	};

	// GoFS: Host Filesystem Bridge
	const createGoFS = () => {
		const getFS = () => (g.pyodide._module && g.pyodide._module.FS) ? g.pyodide._module.FS : g.pyodide.FS;

		var fsImpl = {
			mount: function (mount) {
				console.log("GoFS mount");
				return fsImpl.createNode(null, '/', 16895, mount); // 16895 = S_IFDIR | 0777
			},
			createNode: function (parent, name, mode, dev) {
				var FS = getFS();
				var node = FS.createNode(parent, name, mode, dev);
				node.node_ops = fsImpl.node_ops;
				node.stream_ops = fsImpl.stream_ops;
				return node;
			},
			node_ops: {
				getattr: function (node) {
					try {
						var FS = getFS();
						var path = FS.getPath(node);

						var json = _go_fs_stat(path);
						var info = JSON.parse(json);

						if (node.ino === undefined) {
							node.ino = 0;
						}

						return {
							dev: node.dev,
							ino: node.ino,
							mode: info.mode,
							nlink: 1,
							uid: 0,
							gid: 0,
							rdev: 0,
							size: info.size,
							atime: new Date(info.atimeMs),
							mtime: new Date(info.mtimeMs),
							ctime: new Date(info.ctimeMs),
							blksize: 4096,
							blocks: Math.ceil(info.size / 512)
						};
					} catch (e) {
						if (e.toString().includes("no such file") || e.toString().includes("ENOENT")) throw new FS.ErrnoError(2); // ENOENT
						throw new FS.ErrnoError(5); // EIO
					}
				},
				setattr: function (node, attr) {
					var FS = getFS();
					if (node.mount.opts.readOnly) throw new FS.ErrnoError(30); // EROFS
				},
				lookup: function (parent, name) {
					try {
						var FS = getFS();
						var parentPath = FS.getPath(parent);
						var path = parentPath === '/' ? ('/' + name) : (parentPath + '/' + name);

						// We don't need hostPath here anymore, just check if it exists via Go
						var json = _go_fs_stat(path);
						var info = JSON.parse(json);
						var mode = info.isDirectory ? 16895 : 33206;
						var node = fsImpl.createNode(parent, name, mode, parent.dev);
						return node;
					} catch (e) {
						var FS = getFS();
						throw new FS.ErrnoError(2); // ENOENT
					}
				},
				readdir: function (node) {
					var FS = getFS();
					var path = FS.getPath(node);

					try {
						var names = JSON.parse(_go_fs_readdir(path));
						names.push('.', '..');
						return names;
					} catch (e) {
						var FS = getFS();
						throw new FS.ErrnoError(2);
					}
				},
				mknod: function (parent, name, mode, dev) {
					var FS = getFS();
					if (parent.mount.opts.readOnly) throw new FS.ErrnoError(30);
					try {
						var parentPath = FS.getPath(parent);
						var path = parentPath === '/' ? ('/' + name) : (parentPath + '/' + name);
						_go_fs_mknod(path, mode);
						var node = fsImpl.createNode(parent, name, mode, dev);
						return node;
					} catch (e) {
						if (e.toString().includes("EEXIST")) throw new FS.ErrnoError(17);
						throw new FS.ErrnoError(1);
					}
				},
				rename: function (old_node, new_parent, new_name) { var FS = getFS(); throw new FS.ErrnoError(30); },
				unlink: function (parent, name) { var FS = getFS(); throw new FS.ErrnoError(30); },
				rmdir: function (parent, name) { var FS = getFS(); throw new FS.ErrnoError(30); },
				symlink: function (parent, newname, oldpath) { var FS = getFS(); throw new FS.ErrnoError(30); },
				readlink: function (node) { var FS = getFS(); throw new FS.ErrnoError(30); },
			},
			stream_ops: {
				open: function (stream) {
					try {
						var FS = getFS();
						var path = FS.getPath(stream.node);

						// console.log("GoFS open path: " + path);
						var fd = _go_fs_open(path, stream.flags, 0);
						stream.nfd = fd;
					} catch (e) {
						var FS = getFS();
						if (e.toString().includes("EROFS")) throw new FS.ErrnoError(30);
						console.log("GoFS open fail: " + e);
						throw new FS.ErrnoError(2);
					}
				},
				close: function (stream) {
					_go_fs_close(stream.nfd);
				},
				read: function (stream, buffer, offset, length, position) {
					try {
						var transit = new Uint8Array(g.__fsTransitSAB);
						var totalRead = 0;
						var currentPos = position;

						while (totalRead < length) {
							var chunkLen = Math.min(length - totalRead, transit.length);
							var n = _go_fs_read(stream.nfd, chunkLen, currentPos);
							if (n <= 0) break;

							buffer.set(transit.subarray(0, n), offset + totalRead);
							totalRead += n;

							if (currentPos !== null && currentPos !== undefined) {
								currentPos += n;
							}
						}
						return totalRead;
					} catch (e) {
						console.log("GoFS read fail: " + e);
						throw new FS.ErrnoError(5);
					}
				},
				write: function (stream, buffer, offset, length, position) {
					var FS = getFS();
					if (stream.node.mount.opts.readOnly) throw new FS.ErrnoError(30);
					try {
						var transit = new Uint8Array(g.__fsTransitSAB);
						var totalWritten = 0;
						var currentPos = position;

						while (totalWritten < length) {
							var chunkLen = Math.min(length - totalWritten, transit.length);
							var slice = buffer.subarray(offset + totalWritten, offset + totalWritten + chunkLen);
							transit.set(slice);

							var n = _go_fs_write(stream.nfd, chunkLen, currentPos);
							if (n <= 0) break;

							totalWritten += n;
							if (currentPos !== null && currentPos !== undefined) {
								currentPos += n;
							}
						}
						return totalWritten;
					} catch (e) {
						console.log("GoFS write error: " + e);
						throw new FS.ErrnoError(5);
					}
				},
				llseek: function (stream, offset, whence) {
					var position = stream.position;
					if (whence === 0) {
						position = offset;
					} else if (whence === 1) {
						position += offset;
					} else if (whence === 2) {
						if (FS.isFile(stream.node.mode)) {
							var stat = stream.node.node_ops.getattr(stream.node);
							position = stat.size + offset;
						}
					}
					if (position < 0) {
						throw new FS.ErrnoError(28); // EINVAL
					}
					return position;
				}
			}
		};
		return fsImpl;
	};
	var registerGoFS = () => {
		g.pyodide.FS.filesystems.GOFS = createGoFS();
		console.log("GoFS registered");
	};

	g.__mountGoFS = (mountPoint, readOnly) => {
		if (!g.pyodide.FS.filesystems.GOFS) {
			registerGoFS();
		}
		g.pyodide.FS.mount(g.pyodide.FS.filesystems.GOFS, { readOnly: readOnly }, mountPoint);
		console.log("GoFS mounted at " + mountPoint + " (readOnly: " + readOnly + ")");
	};
})();
