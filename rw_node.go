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

// ReadWriter is the data interface for a read write file.
type ReadWriter interface {
	io.ReaderAt
	io.WriterAt
	Truncate(int64) error
	Size() (int64, error)
}

// RW is a read write file node.
type RW struct {
	mu sync.Mutex

	name string
	attr

	fs *FileSystem

	dev ReadWriter
}

var (
	_ Node              = (*RW)(nil)
	_ fs.Node           = (*RW)(nil)
	_ fs.Handle         = (*RW)(nil)
	_ fs.NodeOpener     = (*RW)(nil)
	_ fs.HandleReleaser = (*RW)(nil)
	_ fs.HandleReader   = (*RW)(nil)
	_ fs.HandleWriter   = (*RW)(nil)
	_ fs.HandleFlusher  = (*RW)(nil)
	_ fs.NodeSetattrer  = (*RW)(nil)
)

// NewRW returns a new RW file with the given name and file mode.
func NewRW(name string, mode os.FileMode, dev ReadWriter) (*RW, error) {
	if strings.Contains(name, string(filepath.Separator)) {
		return nil, ErrBadName
	}
	return &RW{
		name: name,
		attr: attr{
			mode: mode &^ os.ModeDir,
		},
		dev: dev,
	}, nil
}

// MustNewRW returns a new RW with the given name and file mode. It
// will panic if name contains a filepath separator.
func MustNewRW(name string, mode os.FileMode, dev ReadWriter) *RW {
	rw, err := NewRW(name, mode, dev)
	if err != nil {
		panic(err)
	}
	return rw
}

// Own sets the uid and gid of the file.
func (f *RW) Own(uid, gid uint32) *RW {
	f.uid = uid
	f.gid = gid
	return f
}

// Name returns the name of the file.
func (f *RW) Name() string { return f.name }

// SetSys sets the file's containing file system.
func (f *RW) SetSys(filesys *FileSystem) {
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
func (f *RW) Sys() *FileSystem {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.fs
}

// Invalidate invalidates the kernel cache of the file.
func (f *RW) Invalidate() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.fs.Invalidate(f)
}

// Attr satisfies the bazil.org/fuse/fs.Node interface.
func (f *RW) Attr(ctx context.Context, a *fuse.Attr) error {
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
func (f *RW) Open(ctx context.Context, req *fuse.OpenRequest, resp *fuse.OpenResponse) (fs.Handle, error) {
	return f, nil
}

// Release satisfies the bazil.org/fuse/fs.HandleReleaser interface.
// If the RW ReadWriter device is an io.Closer, its Close method is called.
func (f *RW) Release(ctx context.Context, req *fuse.ReleaseRequest) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if c, ok := f.dev.(io.Closer); ok {
		return c.Close()
	}
	return nil
}

// Read satisfies the bazil.org/fuse/fs.HandleReader interface.
func (f *RW) Read(ctx context.Context, req *fuse.ReadRequest, resp *fuse.ReadResponse) error {
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

// Write satisfies the bazil.org/fuse/fs.HandleWriter interface.
func (f *RW) Write(ctx context.Context, req *fuse.WriteRequest, resp *fuse.WriteResponse) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.mtime = f.fs.now()

	var err error
	resp.Size, err = f.dev.WriteAt(req.Data, req.Offset)
	return err
}

// Flush satisfies the bazil.org/fuse/fs.HandleFlusher interface.
func (f *RW) Flush(ctx context.Context, req *fuse.FlushRequest) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	type syncer interface {
		Sync() error
	}
	if s, ok := f.dev.(syncer); ok {
		return s.Sync()
	}
	return nil
}

// Setattr satisfies the bazil.org/fuse/fs.NodeSetattrer interface.
func (f *RW) Setattr(ctx context.Context, req *fuse.SetattrRequest, resp *fuse.SetattrResponse) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if req.Valid&fuse.SetattrSize != 0 {
		err := f.dev.Truncate(int64(req.Size))
		if err != nil {
			return err
		}
		size, err := f.dev.Size()
		if err != nil {
			return err
		}
		resp.Attr.Size = uint64(size)
	}
	setAttr(&f.attr, resp, req)

	return nil
}
