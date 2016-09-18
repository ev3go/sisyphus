// Copyright ©2016 The ev3go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package sisyphus provides tools to build a simple user FUSE-based sysfs-like interface.
package sisyphus

import (
	"errors"
	"io"
	"os"
	"sync"
	"syscall"
	"time"

	"bazil.org/fuse"
	"bazil.org/fuse/fs"
)

// ErrBadName is returned when a new Node is created with a base name
// that contains a filepath separator.
var ErrBadName = errors.New("sisyphus: base contains filepath separator")

// server is a FUSE server for a FileSystem.
type server struct {
	mnt  string
	fuse *fs.Server
	conn *fuse.Conn

	mu  sync.Mutex
	err error
}

// Serve starts a server for filesys mounted at the specified mount point.
// It is the responsibility of the caller to close the returned io.Closer
// when the server is no longer required.
func Serve(mnt string, filesys *FileSystem, config *fs.Config, mntopts ...fuse.MountOption) (io.Closer, error) {
	c, err := fuse.Mount(mnt, mntopts...)
	if err != nil {
		return nil, err
	}

	s := &server{mnt: mnt, fuse: fs.New(c, config), conn: c}
	filesys.server = s

	go func() {
		err = s.fuse.Serve(filesys)
		if err != nil {
			s.mu.Lock()
			s.err = err
			s.mu.Unlock()
		}
	}()
	<-s.conn.Ready
	if s.conn.MountError != nil {
		return nil, s.conn.MountError
	}
	return s, nil
}

// Close closes the server.
func (s *server) Close() error {
	defer s.conn.Close()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return s.err
	}
	return fuse.Unmount(s.mnt)
}

// Bytes is a ReadWriter backed by a byte slice.
type Bytes []byte

// NewBytes returns a new Bytes backed by the provided data.
func NewBytes(data []byte) *Bytes {
	b := Bytes(data)
	return &b
}

// ReadAt satisfies the io.ReaderAt interface.
func (f *Bytes) ReadAt(b []byte, offset int64) (int, error) {
	if len(b) == 0 {
		return 0, nil
	}
	if offset >= int64(len(*f)) {
		return 0, io.EOF
	}
	n := copy(b, (*f)[offset:])
	if n <= len(b) {
		return n, io.EOF
	}
	return n, nil
}

// Truncate truncates the Bytes at n bytes from the beginning of the slice.
func (f *Bytes) Truncate(n int64) error {
	if n < 0 || n > int64(len(*f)) {
		return syscall.EINVAL
	}
	tail := (*f)[n:cap(*f)]
	for i := range tail {
		tail[i] = 0
	}
	*f = (*f)[:n]
	return nil
}

// WriteAt satisfies the io.WriterAt interface.
func (f *Bytes) WriteAt(b []byte, off int64) (int, error) {
	if off >= int64(cap(*f)) {
		t := make([]byte, off+int64(len(b)))
		copy(t, *f)
		*f = t
	}
	*f = (*f)[:off]
	*f = append(*f, b...)
	return len(b), nil
}

// Size returns the length of the backing data and a nil error.
func (f *Bytes) Size() (int64, error) { return int64(len(*f)), nil }

// Func is a Writer backed by a user defined function.
type Func func([]byte, int64) (int, error)

// WriteAt satisfies the io.WriterAt interface.
func (f Func) WriteAt(b []byte, off int64) (int, error) {
	if f == nil {
		return 0, syscall.EBADFD
	}
	return f(b, off)
}

// Truncate is a no-op.
func (f Func) Truncate(_ int64) error { return nil }

// Size returns zero and a nil error.
func (f Func) Size() (int64, error) { return 0, nil }

// String is a Reader backed by a string.
type String string

// ReadAt satisfies the io.ReaderAt interface.
func (s String) ReadAt(b []byte, off int64) (int, error) {
	if off < 0 {
		return 0, syscall.EINVAL
	}
	if off >= int64(len(s)) {
		return 0, io.EOF
	}
	n := copy(b, s[off:])
	if n <= len(b) {
		return n, io.EOF
	}
	return n, nil
}

// Size returns the length of the backing string and a nil error.
func (s String) Size() (int64, error) { return int64(len(s)), nil }

// attr is the set of node attributes/
type attr struct {
	mode  os.FileMode
	uid   uint32
	gid   uint32
	atime time.Time
	mtime time.Time
	ctime time.Time
}

// copyAttr copies node attributes to a fuse.Attr.
func copyAttr(dst *fuse.Attr, src attr) {
	dst.Mode = src.mode
	dst.Uid = src.uid
	dst.Gid = src.gid
	dst.Atime = src.atime
	dst.Mtime = src.mtime
	dst.Ctime = src.ctime
}

// setAttr copies node attributes from a *fuse.SetattrRequest.
func setAttr(dst *attr, resp *fuse.SetattrResponse, src *fuse.SetattrRequest) {
	if src.Valid&fuse.SetattrMode != 0 {
		resp.Attr.Mode = src.Mode
		dst.mode = src.Mode
	}
	if src.Valid&fuse.SetattrUid != 0 {
		resp.Attr.Uid = src.Uid
		dst.uid = src.Uid
	}
	if src.Valid&fuse.SetattrGid != 0 {
		resp.Attr.Gid = src.Gid
		dst.gid = src.Gid
	}
	if src.Valid&fuse.SetattrAtime != 0 {
		resp.Attr.Atime = src.Atime
		dst.atime = src.Atime
	}
	if src.Valid&fuse.SetattrMtime != 0 {
		resp.Attr.Mtime = src.Mtime
		dst.mtime = src.Mtime
	}
}
