package pyodide

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/tommie/v8go"
)

// polyfillFileSystem exposes Go filesystem operations to JS to support NODEFS.
func (rt *Runtime) polyfillFileSystem() {
	iso := rt.isolate
	global := rt.context.Global()

	// Helper to create Go functions exposed to JS
	createFn := func(name string, fn func(*v8go.FunctionCallbackInfo) *v8go.Value) {
		tmpl := v8go.NewFunctionTemplate(iso, fn)
		global.Set(name, tmpl.GetFunction(rt.context))
	}

	createFn("_go_fs_stat", func(info *v8go.FunctionCallbackInfo) *v8go.Value {
		vpath := info.Args()[0].String()
		hostPath, _, err := rt.resolvePath(vpath)
		if err != nil {
			return rt.throwError(err)
		}

		infoStat, err := os.Stat(hostPath)
		if err != nil {
			return rt.throwError(err)
		}
		return rt.createStatObject(infoStat)
	})

	createFn("_go_fs_lstat", func(info *v8go.FunctionCallbackInfo) *v8go.Value {
		vpath := info.Args()[0].String()
		hostPath, _, err := rt.resolvePath(vpath)
		if err != nil {
			return rt.throwError(err)
		}

		infoStat, err := os.Lstat(hostPath)
		if err != nil {
			return rt.throwError(err)
		}
		return rt.createStatObject(infoStat)
	})

	createFn("_go_fs_readdir", func(info *v8go.FunctionCallbackInfo) *v8go.Value {
		vpath := info.Args()[0].String()
		hostPath, _, err := rt.resolvePath(vpath)
		if err != nil {
			return rt.throwError(err)
		}

		entries, err := os.ReadDir(hostPath)
		if err != nil {
			return rt.throwError(err)
		}

		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}

		data, _ := json.Marshal(names)
		val, _ := v8go.NewValue(iso, string(data))
		return val
	})

	// We need to implement open/read/close/write for actual file access.
	// We need a file descriptor table mapping integer FDs to os.File pointers.

	createFn("_go_fs_open", func(info *v8go.FunctionCallbackInfo) *v8go.Value {
		vpath := info.Args()[0].String()
		flags := int(info.Args()[1].Int32())
		// mode := info.Args()[2].Int32() // ignored for now

		hostPath, readOnly, err := rt.resolvePath(vpath)
		if err != nil {
			return rt.throwError(err)
		}

		// Check for write flags if read-only
		// O_RDONLY is 0, O_WRONLY is 1, O_RDWR is 2
		// We should also check for O_CREAT, etc if necessary.
		if readOnly && (flags&3) != 0 {
			return rt.throwError(fmt.Errorf("EROFS: read-only file system %q", vpath))
		}

		f, err := os.OpenFile(hostPath, flags, 0644)
		if err != nil {
			return rt.throwError(err)
		}

		fd := rt.allocFD(f)
		val, _ := v8go.NewValue(iso, int32(fd))
		return val
	})

	createFn("_go_fs_close", func(info *v8go.FunctionCallbackInfo) *v8go.Value {
		fd := info.Args()[0].Int32()
		err := rt.closeFD(int(fd))
		if err != nil {
			return rt.throwError(err)
		}
		return v8go.Undefined(iso)
	})

	createFn("_go_fs_read", func(info *v8go.FunctionCallbackInfo) *v8go.Value {
		fd := int(info.Args()[0].Int32())
		// buffer arg [1] is ignored here, we return data
		// offset arg [2] is ignored for reading, we return data
		length := int(info.Args()[3].Int32())
		position := info.Args()[4] // Number or null

		f := rt.getFD(fd)
		if f == nil {
			return rt.throwError(fmt.Errorf("EBADF: bad file descriptor %d", fd))
		}

		buf := make([]byte, length)
		var n int
		var err error

		if !position.IsUndefined() && !position.IsNull() {
			pos := position.Integer() // int64
			n, err = f.ReadAt(buf, pos)
		} else {
			n, err = f.Read(buf)
		}

		if err != nil && err != io.EOF {
			return rt.throwError(err)
		}

		// Return { bytesRead: n, data: base64 }
		encoded := base64.StdEncoding.EncodeToString(buf[:n])
		resJSON := fmt.Sprintf(`{"bytesRead": %d, "data": %q}`, n, encoded)
		val, _ := v8go.NewValue(iso, resJSON)
		return val
	})

	createFn("_go_fs_write", func(info *v8go.FunctionCallbackInfo) *v8go.Value {
		fd := int(info.Args()[0].Int32())
		encoded := info.Args()[1].String()
		length := int(info.Args()[2].Int32())
		position := info.Args()[3] // Number or null

		f := rt.getFD(fd)
		if f == nil {
			return rt.throwError(fmt.Errorf("EBADF: bad file descriptor %d", fd))
		}

		data, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			return rt.throwError(err)
		}

		if len(data) > length {
			data = data[:length]
		}

		var n int
		if !position.IsUndefined() && !position.IsNull() {
			pos := position.Integer()
			n, err = f.WriteAt(data, pos)
		} else {
			n, err = f.Write(data)
		}

		if err != nil {
			return rt.throwError(err)
		}

		val, _ := v8go.NewValue(iso, int32(n))
		return val
	})

	createFn("_go_fs_mknod", func(info *v8go.FunctionCallbackInfo) *v8go.Value {
		vpath := info.Args()[0].String()
		mode := uint32(info.Args()[1].Int32())

		hostPath, readOnly, err := rt.resolvePath(vpath)
		if err != nil {
			return rt.throwError(err)
		}
		if readOnly {
			return rt.throwError(fmt.Errorf("EROFS: read-only file system %q", vpath))
		}

		f, err := os.OpenFile(hostPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, os.FileMode(mode&0777))
		if err != nil {
			return rt.throwError(err)
		}
		f.Close()
		return v8go.Undefined(iso)
	})
}

func (rt *Runtime) throwError(err error) *v8go.Value {
	// Simple error throwing. In real NODEFS we might need code: 'ENOENT' etc.
	msg := err.Error()
	val, _ := v8go.NewValue(rt.isolate, msg)
	rt.isolate.ThrowException(val)
	return val
}

func (rt *Runtime) createStatObject(info os.FileInfo) *v8go.Value {
	// NODEFS wrapper will parse it.
	mode := info.Mode()
	posixMode := uint32(mode.Perm()) // Start with permissions
	if mode.IsDir() {
		posixMode |= 0040000 // S_IFDIR
	} else if mode&os.ModeSymlink != 0 {
		posixMode |= 0120000 // S_IFLNK
	} else {
		posixMode |= 0100000 // S_IFREG
	}

	stat := map[string]interface{}{
		"size":        info.Size(),
		"mode":        posixMode,
		"mtimeMs":     info.ModTime().UnixMilli(),
		"atimeMs":     info.ModTime().UnixMilli(), // Go doesn't give atime easily
		"ctimeMs":     info.ModTime().UnixMilli(),
		"isDirectory": info.IsDir(),
		"isFile":      !info.IsDir(),
	}
	data, _ := json.Marshal(stat)
	val, _ := v8go.NewValue(rt.isolate, string(data))
	return val
}

// FDT (File Descriptor Table)
// We need to manage FDs because we pass integer FDs to JS.
// We can store them in rt.

func (rt *Runtime) allocFD(f *os.File) int {
	rt.fdsMu.Lock()
	defer rt.fdsMu.Unlock()
	fd := rt.nextFd
	rt.nextFd++
	rt.fds[fd] = f
	return fd
}

func (rt *Runtime) getFD(fd int) *os.File {
	rt.fdsMu.Lock()
	defer rt.fdsMu.Unlock()
	return rt.fds[fd]
}

func (rt *Runtime) closeFD(fd int) error {
	rt.fdsMu.Lock()
	defer rt.fdsMu.Unlock()
	f, ok := rt.fds[fd]
	if !ok {
		return fmt.Errorf("EBADF: bad file descriptor %d", fd)
	}
	delete(rt.fds, fd)
	return f.Close()
}

func (rt *Runtime) resolvePath(vpath string) (string, bool, error) {
	// Canonicalize vpath
	vpath = filepath.Clean(vpath)
	if !strings.HasPrefix(vpath, "/") {
		vpath = "/" + vpath
	}

	rt.mountsMu.RLock()
	defer rt.mountsMu.RUnlock()

	var bestMountPath string
	var bestMount mount

	// Find the longest prefix match
	for mp, m := range rt.mounts {
		if vpath == mp || (strings.HasPrefix(vpath, mp) && vpath[len(mp)] == '/') {
			if len(mp) > len(bestMountPath) {
				bestMountPath = mp
				bestMount = m
			}
		}
	}

	if bestMountPath == "" {
		return "", false, fmt.Errorf("EPERM: path %q is not in a mounted directory", vpath)
	}

	relPath := vpath[len(bestMountPath):]
	if relPath == "" {
		relPath = "."
	} else if relPath[0] == '/' {
		relPath = relPath[1:]
	}

	hostPath := filepath.Join(bestMount.hostPath, relPath)
	return hostPath, bestMount.readOnly, nil
}
