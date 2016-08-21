// Copyright ©2016 The ev3go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package sisyphus

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"syscall"
	"testing"
	"testing/iotest"
	"time"

	"bazil.org/fuse"
)

const prefix = "testmount"

var (
	epoch time.Time
	clock func() time.Time
)

func init() {
	loc, err := time.LoadLocation("Europe/Copenhagen")
	if err != nil {
		panic(err)
	}
	epoch = time.Date(2013, time.September, 1, 0, 0, 0, 0, loc)
	clock = func() time.Time { return epoch }
}

var (
	d  = NewDir
	ro = NewRO
	rw = NewRW
	wo = NewWO
)

func sysfs(t *testing.T, comm chan string) *FileSystem {
	return NewFileSystem(0775, clock).With(
		d("sys", 0775).With(
			d("class", 0775).With(
				d("dc-motor", 0775),
				d("leds", 0775),
				d("lego-port", 0775),
				d("lego-sensor", 0775),
				d("power_supply", 0775),
				d("servo-motor", 0775),
				d("tacho-motor", 0775).With(
					wo("command", 0222, Func(func(b []byte, off int64) (int, error) {
						// Make sure we return the expected byte
						// count if we trim the trailing newline.
						n := len(b)

						if n != 0 && b[len(b)-1] == '\n' {
							b = b[:len(b)-1]
						}
						switch {
						case off == 0 && bytes.Equal(b, []byte("start")):
							select {
							case comm <- "START":
							default:
								t.Errorf("could not send for %q", b)
							}
							return n, nil
						case off == 0 && bytes.Equal(b, []byte("stop")):
							select {
							case comm <- "STOP":
							default:
								t.Errorf("could not send for %q", b)
							}
							return n, nil
						default:
							select {
							case comm <- fmt.Sprintf("unknown command: %q", b):
							default:
								t.Errorf("could not send for %q", b)
							}
							return n, syscall.EINVAL
						}
					})),
				),
			),
		),
		d("dev", 0775).With(
			rw("foo", 0666, NewBytes([]byte("with data already here"))),
			d("input", 0775).With(
				d("by-path", 0775).With(
					ro("platform-gpio-keys.0-event", 0666,
						String("constant data\n")),
				),
			),
		),
	).Sync()
}

func TestFileSystem(t *testing.T) {
	comm := make(chan string, 1)
	fs := sysfs(t, comm)
	c, err := Serve(prefix, fs, nil, fuse.AllowNonEmptyMount())
	if err != nil {
		t.Fatalf("failed to open server: %v", err)
	}
	defer func() {
		// Allow some time for the
		// server to be ready to close.
		time.Sleep(time.Second)

		err = c.Close()
		if err != nil {
			t.Errorf("failed to close server: %v", err)
		}
	}()

	t.Run("read directory", func(t *testing.T) {
		var files []string
		f, err := os.Open(filepath.Join(prefix, "sys/class"))
		if err != nil {
			t.Errorf("unexpected error opening directory: %v", err)
			return
		}
		for {
			names, err := f.Readdirnames(3)
			files = append(files, names...)
			if err != nil {
				if err != io.EOF {
					t.Fatalf("unexpected error reading directory: %v", err)
				}
				break
			}
		}
		sort.Strings(files)
		f.Close()

		f, err = os.Open(filepath.Join(prefix, "sys/class"))
		if err != nil {
			t.Fatalf("unexpected error opening directory: %v", err)
		}
		allfiles, err := f.Readdirnames(0)
		if err != nil {
			t.Fatalf("unexpected error reading directory: %v", err)
		}
		sort.Strings(allfiles)
		f.Close()
		if !reflect.DeepEqual(files, allfiles) {
			t.Errorf("mismatch between directory lists:\nby 3: %v\nall:  %v", files, allfiles)
		}
	})

	t.Run("read", func(t *testing.T) {
		f, err := os.Open(filepath.Join(prefix, "dev/input/by-path/platform-gpio-keys.0-event"))
		if err != nil {
			t.Fatalf("unexpected error opening ro file: %v", err)
		}
		var buf bytes.Buffer
		io.Copy(&buf, f)
		f.Close()
		got := buf.String()
		want := "constant data\n"
		if got != want {
			t.Errorf("expected file contents:\ngot: %q\nwant:%q", got, want)
		}
	})

	t.Run("read one byte reader", func(t *testing.T) {
		f, err := os.Open(filepath.Join(prefix, "dev/input/by-path/platform-gpio-keys.0-event"))
		if err != nil {
			t.Fatalf("unexpected error opening ro file: %v", err)
		}
		var buf bytes.Buffer
		io.Copy(&buf, iotest.OneByteReader(f))
		f.Close()
		got := buf.String()
		want := "constant data\n"
		if got != want {
			t.Errorf("expected file contents:\ngot: %q\nwant:%q", got, want)
		}
	})

	t.Run("read write", func(t *testing.T) {
		f, err := os.OpenFile(filepath.Join(prefix, "dev/foo"), os.O_RDWR|os.O_CREATE, 0666)
		if err != nil {
			t.Fatalf("unexpected error opening rw buffer: %v", err)
		}
		_, err = f.Write([]byte("... more\n"))
		if err != nil {
			t.Fatalf("unexpected error writing to rw buffer: %v", err)
		}
		f.Seek(0, 0)
		var buf bytes.Buffer
		io.Copy(&buf, f)
		f.Close()
		got := buf.String()
		want := "with data already here... more\n"
		if got != want {
			t.Errorf("expected file contents:\ngot: %q\nwant:%q", got, want)
		}
	})

	t.Run("truncate read write", func(t *testing.T) {
		f, err := os.OpenFile(filepath.Join(prefix, "dev/foo"), os.O_RDWR|os.O_CREATE, 0666)
		if err != nil {
			t.Fatalf("unexpected error opening rw buffer: %v", err)
		}
		err = f.Truncate(int64(len("with data already")))
		if err != nil {
			t.Errorf("unexpected error truncating rw buffer: %v", err)
		}

		var buf bytes.Buffer
		io.Copy(&buf, f)
		f.Close()
		got := buf.String()
		want := "with data already"
		if got != want {
			t.Errorf("expected file contents:\ngot: %q\nwant:%q", got, want)
		}
	})

	t.Run("action", func(t *testing.T) {
		for _, c := range []struct {
			send string
			ok   bool
		}{
			{send: "foo", ok: false},
			{send: "start", ok: true},
			{send: "stop", ok: true},
		} {
			t.Run(c.send, func(t *testing.T) {
				f, err := os.OpenFile(filepath.Join(prefix, "sys/class/tacho-motor/command"), os.O_WRONLY, 0222)
				if err != nil {
					t.Fatalf("unexpected error opening command: %v", err)

				}
				defer f.Close()

				_, err = f.Write([]byte(c.send))
				if err == nil != c.ok {
					t.Errorf("unexpected error state for %+v: got:%v", c, err)
				}

				select {
				case got := <-comm:
					var want string
					if c.ok {
						want = strings.ToUpper(c.send)
					} else {
						want = fmt.Sprintf("unknown command: %q", c.send)
					}
					if got != want {
						t.Errorf("unexpected command: got:%q want:%q", got, want)
					}
				case <-time.After(time.Second):
					t.Error("timed out waiting for command after one second")
				}
			})
		}
	})

	t.Run("unbind bind", func(t *testing.T) {
		path := filepath.Join(prefix, "dev")

		_, err := fs.Unbind(filepath.Join(path, "noexist"))
		if err == nil {
			t.Errorf("expected error unbinding non-existent path")
		}

		n, err := fs.Unbind(filepath.Join(path, "input"))
		if err != nil {
			t.Fatalf("unexpected error unbinding path: %v", err)
		}
		if name := n.Name(); name != "input" {
			t.Errorf("wrong node name: got:%q want:%q", name, "input")
		}

		f, err := os.Open(path)
		if err != nil {
			t.Fatalf("unexpected error opening directory: %v", err)
		}
		defer f.Close()
		files, err := f.Readdirnames(0)
		if err != nil {
			t.Errorf("unexpected error reading directory: %v", err)
		}
		for _, name := range files {
			if name == "input" {
				t.Errorf("found unbound file %q", name)
				break
			}
		}

		err = fs.Bind(filepath.Join(path, "noexist"), n)
		if err == nil {
			t.Errorf("expected error binding at non-existent path")
		}

		err = fs.Bind(path, n)
		if err != nil {
			t.Errorf("unexpected error binding %q at %v: %v", n.Name(), path, err)
		}
		f.Seek(0, io.SeekStart)
		if err != nil {
			t.Fatalf("unexpected error seeking directory: %v", err)
		}
		files, err = f.Readdirnames(0)
		if err != nil {
			t.Errorf("unexpected error reading directory: %v", err)
		}
		found := false
		for _, name := range files {
			if name == "input" {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("did not find rebound file %q", "input")
		}
	})
}
