// Copyright ©2016 The ev3go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package sisyphus

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"bazil.org/fuse"
	"bazil.org/fuse/fs"
)

// Node is a node in a FileSystem.
type Node interface {
	fs.Node

	// Name returns the name of the node.
	Name() string

	// Sys returns a pointer to the FileSystem
	// holding the Node.
	Sys() *FileSystem

	// SetSys sets the pointer to the FileSystem
	// holding the node. SetSys must accept a nil
	// parameter.
	SetSys(*FileSystem)
}

// FileSystem is a virtual file system.
type FileSystem struct {
	mu     sync.Mutex
	root   *Dir
	server *server

	now func() time.Time
}

var nofs *FileSystem

// NewFileSystem returns a new file system setting the mode of the root and
// the clock.
func NewFileSystem(mode os.FileMode, clock func() time.Time) *FileSystem {
	var fs FileSystem
	fs.now = clock
	fs.root = NewDir("/", mode)
	fs.root.SetSys(&fs)
	return &fs
}

// With adds nodes to the file system's root.
func (fs *FileSystem) With(nodes ...Node) *FileSystem {
	fs.root.With(nodes...)
	return fs
}

// Sync updates all internal data links within the file system. Sync must be
// called if a file system has been constructed using With.
func (fs *FileSystem) Sync() *FileSystem {
	fs.mu.Lock()
	fs.sync(fs.root)
	fs.mu.Unlock()
	return fs
}

func (fs *FileSystem) sync(n Node) {
	if n.Sys() != fs {
		n.SetSys(fs)
	}

	dir, ok := n.(*Dir)
	if !ok {
		return
	}
	for _, f := range dir.files {
		fs.sync(f)
	}
}

// Invalidate invalidates the kernel cache of the given node.
func (fs *FileSystem) Invalidate(n Node) error {
	err := fs.server.fuse.InvalidateNodeData(n)
	if err == fuse.ErrNotCached {
		err = nil
	}
	return err
}

// InvalidatePath invalidates the kernel cache of the node at the given path.
func (fs *FileSystem) InvalidatePath(path string) error {
	n, err := walkPath(fs.root, "invalidate", path)
	if err != nil {
		return err
	}
	err = fs.server.fuse.InvalidateNodeData(n)
	if err == fuse.ErrNotCached {
		err = nil
	}
	return err
}

// Bind binds the node at the given directory path.
func (fs *FileSystem) Bind(dir string, n Node) error {
	defer fs.mu.Unlock()
	fs.mu.Lock()
	return fs.bind(dir, n)
}

func (fs *FileSystem) bind(dir string, n Node) error {
	dir = filepath.Clean(dir)
	d, err := walkPath(fs.root, "open", dir)
	if os.IsNotExist(err) {
		return &os.PathError{
			Op:   "open",
			Path: dir,
			Err:  syscall.ENOENT,
		}
	}
	d.(*Dir).files[n.Name()] = n
	fs.sync(d)

	return nil
}

// Unbind unbinds to node at the given path, returning the node
// if successful.
func (fs *FileSystem) Unbind(path string) (Node, error) {
	path = filepath.Clean(path)
	if len(path) == 0 && path[0] == filepath.Separator {
		return nil, &os.PathError{Op: "unbind", Path: path, Err: syscall.EINVAL}
	}

	fs.mu.Lock()
	defer fs.mu.Unlock()

	dir, name := filepath.Split(path)
	n, err := walkPath(fs.root, "unbind", dir)
	if err != nil {
		return nil, err
	}
	d := n.(*Dir)
	node, ok := d.files[name]
	if !ok {
		return nil, &os.PathError{Op: "unbind", Path: path, Err: syscall.ENOENT}
	}
	delete(d.files, name)
	nofs.sync(node)
	return node, nil
}

func pathElements(path string) []string {
	e := strings.Split(filepath.Clean(path), string(filepath.Separator))[1:]
	if len(e) == 1 && len(e[0]) == 0 {
		return nil
	}
	return e
}

func walkPath(d *Dir, op, path string) (Node, error) {
	elements := pathElements(path)
	if len(elements) == 0 {
		return d, nil
	}
	for i, e := range elements {
		n, ok := d.files[e]
		if !ok {
			if i < len(elements)-1 {
				return nil, &os.PathError{Op: op, Path: path, Err: syscall.ENOENT}
			}
			// If we are at the end of the path and have not found our target
			// return the containing directory. Since we may have wanted it
			// to remove the target.
			return d, &os.PathError{Op: op, Path: path, Err: syscall.ENOENT}
		}
		if i == len(elements)-1 {
			return n, nil
		}
		d, ok = n.(*Dir)
		if !ok {
			return nil, &os.PathError{Op: op, Path: path, Err: syscall.ENOTDIR}
		}
	}
	panic("cannot reach")
}

var _ fs.FS = (*FileSystem)(nil)

// Root satisfies the bazil.org/fuse/fs.FS interface.
func (fs *FileSystem) Root() (fs.Node, error) { return fs.root, nil }

// errno is an error that satisfies fuse.ErrorNumber.
type errno struct {
	error
	errno fuse.Errno
}

var _ fuse.ErrorNumber = errno{}

func (e errno) Errno() fuse.Errno {
	return e.errno
}
