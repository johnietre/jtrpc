package main

import (
	"io"
	"net/http"
	"sync"

	webs "golang.org/x/net/websocket"
)

type LengthWriter struct {
	n int64
}

func (lw *LengthWriter) Write(p []byte) (int, error) {
	l := len(p)
	lw.n += int64(l)
	return l, nil
}

func (lw *LengthWriter) NumWritten() int64 {
	return lw.n
}

func errStr(err error) string {
	if err != nil {
		return err.Error()
	}
	return ""
}

func getWs(w http.ResponseWriter, r *http.Request) (ws *webs.Conn) {
	webs.Handler(func(conn *webs.Conn) {
		ws = conn
	}).ServeHTTP(w, r)
	return
}

func writeAll(w io.Writer, b []byte) error {
	for written, l := 0, len(b); written < l; {
		n, err := w.Write(b[written:])
		if err != nil {
			return err
		}
		written += n
	}
	return nil
}

// LockedWriter is a wrapper for locking a given writer.
type LockedWriter struct {
	w io.Writer
	sync.Mutex
}

// NewLockedWriter creates a new LockedWriter with the given writer.
func NewLockedWriter(w io.Writer) *LockedWriter {
	return &LockedWriter{w: w}
}

// Write locks the writer and writes b to the underlying writer.
// Will result in a deadlock if the current thread currently holds the lock.
func (lw *LockedWriter) Write(b []byte) (int, error) {
	lw.Lock()
	defer lw.Unlock()
	return lw.LockedWrite(b)
}

// LockedWrite writes to the underlying writer without locking. Assumes the
// calling thread currently holds the lock.
func (lw *LockedWriter) LockedWrite(b []byte) (int, error) {
	return lw.w.Write(b)
}

// Writer locks the writer and returns the underlying writer.
// Will result in a deadlock if the current thread currently holds the lock.
// The underlying writer should only be used while the current thread holds the
// lock. The caller should unlock the writer (writer.Unlock()) when finished.
func (lw *LockedWriter) Writer() io.Writer {
	lw.Lock()
	return lw.LockedWriter()
}

// LockedWriter returns the underlying writer without locking. Assumes the
// calling thread currently holds the lock.
// The underlying writer should only be used while the current thread holds the
// lock. The caller should unlock the writer (writer.Unlock()) when finished.
func (lw *LockedWriter) LockedWriter() io.Writer {
	return lw.w
}
