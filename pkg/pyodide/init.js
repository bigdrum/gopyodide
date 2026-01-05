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
			} catch(e) { return '/'; }
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
	g.cancelAnimationFrame = (id) => {};
	
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

	// Simplified Headers polyfill
	g.Headers = class {
		constructor(init) { this._map = new Map(init ? Object.entries(init) : []); }
		get(n) { return this._map.get(n.toLowerCase()) || null; }
		has(n) { return this._map.has(n.toLowerCase()); }
	};

	// Simplified Fetch Response creation
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

	// Simplified XMLHttpRequest polyfill using fetch
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
