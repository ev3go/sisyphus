// Copyright ©2016 The ev3go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package sisyphus

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"bazil.org/fuse"
	"bazil.org/fuse/fs"
)

// Dir is a directory node.
type Dir struct {
	mu sync.Mutex

	name string
	attr

	files map[string]Node

	fs *FileSystem
}

var (
	_ Node                  = (*Dir)(nil)
	_ fs.Node               = (*Dir)(nil)
	_ fs.HandleReadDirAller = (*Dir)(nil)
	_ fs.NodeStringLookuper = (*Dir)(nil)
)

// NewDir returns a new Dir with the given name and file mode.
func NewDir(name string, mode os.FileMode) (*Dir, error) {
	if name != "/" && strings.Contains(name, string(filepath.Separator)) {
		return nil, ErrBadName
	}
	return &Dir{
		name: name,
		attr: attr{
			mode: os.ModeDir | mode&^(os.ModeSymlink|os.ModeNamedPipe|os.ModeSocket),
		},
		files: make(map[string]Node),
	}, nil
}

// MustNewDir returns a new Dir with the given name and file mode. It
// will panic if name contains a filepath separator unless name is "/".
func MustNewDir(name string, mode os.FileMode) *Dir {
	d, err := NewDir(name, mode)
	if err != nil {
		panic(err)
	}
	return d
}

// Own sets the uid and gid of the directory.
func (d *Dir) Own(uid, gid uint32) *Dir {
	d.uid = uid
	d.gid = gid
	d.mtime = d.fs.now()
	return d
}

// With adds nodes to the dirctory. If with is used the FileSystem Sync method
// should be called when all nodes have been added.
func (d *Dir) With(nodes ...Node) Node {
	for _, n := range nodes {
		d.files[n.Name()] = n
	}
	return d
}

// Name returns the name of the directory.
func (d *Dir) Name() string { return d.name }

// SetSys sets the directory's containing file system.
func (d *Dir) SetSys(filesys *FileSystem) {
	d.mu.Lock()
	d.fs = filesys
	var now time.Time
	if filesys != nil {
		now = filesys.now()
	}
	d.ctime = now
	d.atime = now
	d.mtime = now
	d.mu.Unlock()
}

// Sys returns the directory's containing filesystem.
func (d *Dir) Sys() *FileSystem {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.fs
}

// Invalidate invalidates the kernel cache of the directory.
func (d *Dir) Invalidate() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.fs.Invalidate(d)
}

// Attr satisfies the bazil.org/fuse/fs.Node interface.
func (d *Dir) Attr(ctx context.Context, a *fuse.Attr) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	copyAttr(a, d.attr)
	return nil
}

// ReadDirAll satisfies the bazil.org/fuse/HandleReadDirAller.Node interface.
func (d *Dir) ReadDirAll(ctx context.Context) ([]fuse.Dirent, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	files := make([]fuse.Dirent, 0, len(d.files))
	var attr fuse.Attr
	for name, f := range d.files {
		err := f.Attr(ctx, &attr)
		if err != nil {
			return files, err
		}
		files = append(files, fuse.Dirent{Inode: attr.Inode, Name: name})
	}
	d.atime = d.fs.now()
	return files, nil
}

// Lookup satisfies the bazil.org/fuse/NodeStringLookuper.Node interface.
func (d *Dir) Lookup(ctx context.Context, name string) (fs.Node, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	n, ok := d.files[name]
	d.atime = d.fs.now()
	if !ok {
		return nil, fuse.ENOENT
	}
	return n, nil
}
