// Copyright ©2016 The ev3go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package sisyphus

import (
	"io"
	"os"
	"sync"
	"syscall"
	"time"

	"golang.org/x/net/context"

	"bazil.org/fuse"
	"bazil.org/fuse/fs"
)

// Writer is the data interface for a write only file.
type Writer interface {
	io.WriterAt
	Truncate(int64) error
	Size() (int64, error)
}

// WO is a write only file node.
type WO struct {
	mu sync.Mutex

	name string
	attr

	fs *FileSystem

	dev Writer
}

var (
	_ Node              = (*WO)(nil)
	_ fs.Node           = (*WO)(nil)
	_ fs.Handle         = (*WO)(nil)
	_ fs.NodeOpener     = (*WO)(nil)
	_ fs.HandleReleaser = (*WO)(nil)
	_ fs.HandleWriter   = (*WO)(nil)
	_ fs.HandleFlusher  = (*WO)(nil)
	_ fs.NodeSetattrer  = (*WO)(nil)
)

// NewWO returns a new WO file with the given name and file mode.
func NewWO(name string, mode os.FileMode, dev Writer) *WO {
	return &WO{
		name: name,
		attr: attr{
			mode: mode &^ (os.ModeDir | 0444),
		},
		dev: dev,
	}
}

// Own sets the uid and gid of the file.
func (f *WO) Own(uid, gid uint32) *WO {
	f.uid = uid
	f.gid = gid
	return f
}

// Name returns the name of the file.
func (f *WO) Name() string { return f.name }

// SetSys sets the file's containing file system.
func (f *WO) SetSys(filesys *FileSystem) {
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
func (f *WO) Sys() *FileSystem {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.fs
}

// Invalidate invalidates the kernel cache of the file.
func (f *WO) Invalidate() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.fs.Invalidate(f)
}

// Attr satisfies the bazil.org/fuse/fs.Node interface.
func (f *WO) Attr(ctx context.Context, a *fuse.Attr) error {
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
func (f *WO) Open(ctx context.Context, req *fuse.OpenRequest, resp *fuse.OpenResponse) (fs.Handle, error) {
	return f, nil
}

// Release satisfies the bazil.org/fuse/fs.HandleReleaser interface.
// If the WO Writer device is an io.Closer, its Close method is called.
func (f *WO) Release(ctx context.Context, req *fuse.ReleaseRequest) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if c, ok := f.dev.(io.Closer); ok {
		return c.Close()
	}
	return nil
}

// Write satisfies the bazil.org/fuse/fs.HandleWriter interface.
func (f *WO) Write(ctx context.Context, req *fuse.WriteRequest, resp *fuse.WriteResponse) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.mtime = f.fs.now()

	var err error
	resp.Size, err = f.dev.WriteAt(req.Data, req.Offset)
	return err
}

// Flush satisfies the bazil.org/fuse/fs.HandleFlusher interface.
func (f *WO) Flush(ctx context.Context, req *fuse.FlushRequest) error {
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
func (f *WO) Setattr(ctx context.Context, req *fuse.SetattrRequest, resp *fuse.SetattrResponse) error {
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
