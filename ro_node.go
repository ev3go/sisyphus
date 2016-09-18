// Copyright ©2016 The ev3go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package sisyphus

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/net/context"

	"bazil.org/fuse"
	"bazil.org/fuse/fs"
)

// Reader is the data interface for a read only file.
type Reader interface {
	io.ReaderAt
	Size() (int64, error)
}

// RO is a read only file node.
type RO struct {
	mu sync.Mutex

	name string
	attr

	fs *FileSystem

	dev Reader
}

var (
	_ Node              = (*RO)(nil)
	_ fs.Node           = (*RO)(nil)
	_ fs.Handle         = (*RO)(nil)
	_ fs.NodeOpener     = (*RO)(nil)
	_ fs.HandleReleaser = (*RO)(nil)
	_ fs.HandleReader   = (*RO)(nil)
)

// NewRO returns a new RO file with the given name and file mode.
func NewRO(name string, mode os.FileMode, dev Reader) (*RO, error) {
	if strings.Contains(name, string(filepath.Separator)) {
		return nil, ErrBadName
	}
	return &RO{
		name: name,
		attr: attr{
			mode: mode &^ (os.ModeDir | 0222),
		},
		dev: dev,
	}, nil
}

// MustNewRO returns a new RO with the given name and file mode. It
// will panic if name contains a filepath separator.
func MustNewRO(name string, mode os.FileMode, dev Reader) *RO {
	ro, err := NewRO(name, mode, dev)
	if err != nil {
		panic(err)
	}
	return ro
}

// Own sets the uid and gid of the file.
func (f *RO) Own(uid, gid uint32) *RO {
	f.uid = uid
	f.gid = gid
	return f
}

// Name returns the name of the file.
func (f *RO) Name() string { return f.name }

// SetSys sets the file's containing file system.
func (f *RO) SetSys(filesys *FileSystem) {
	f.mu.Lock()
	f.fs = filesys
	var now time.Time
	if filesys != nil {
		now = filesys.now()
	}
	f.ctime = now
	f.atime = now
	f.mtime = now
	f.mu.Unlock()
}

// Sys returns the file's containing filesystem.
func (f *RO) Sys() *FileSystem {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.fs
}

// Invalidate invalidates the kernel cache of the file.
func (f *RO) Invalidate() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.fs.Invalidate(f)
}

// Attr satisfies the bazil.org/fuse/fs.Node interface.
func (f *RO) Attr(ctx context.Context, a *fuse.Attr) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	copyAttr(a, f.attr)
	size, err := f.dev.Size()
	if err != nil {
		return errno{error: err, errno: fuse.Errno(syscall.EBADFD)}
	}
	a.Size = uint64(size)
	return nil
}

// Open satisfies the bazil.org/fuse/fs.NodeOpener interface.
func (f *RO) Open(ctx context.Context, req *fuse.OpenRequest, resp *fuse.OpenResponse) (fs.Handle, error) {
	return f, nil
}

// Release satisfies the bazil.org/fuse/fs.HandleReleaser interface.
// If the RO Reader device is an io.Closer, its Close method is called.
func (f *RO) Release(ctx context.Context, req *fuse.ReleaseRequest) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if c, ok := f.dev.(io.Closer); ok {
		return c.Close()
	}
	return nil
}

// Read satisfies the bazil.org/fuse/fs.HandleReader interface.
func (f *RO) Read(ctx context.Context, req *fuse.ReadRequest, resp *fuse.ReadResponse) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.atime = f.fs.now()

	n, err := f.dev.ReadAt(resp.Data[:req.Size], int64(req.Offset))
	resp.Data = resp.Data[:n]
	if err == io.EOF {
		return nil
	}
	return err
}
