package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"sync/atomic"
)

func readHeadersInto(r io.Reader, buf *bytes.Buffer, n uint32) error {
	_, err := io.CopyN(buf, r, int64(n))
	return err
}

func readBodyInto(r io.Reader, buf *bytes.Buffer, n uint64) error {
	_, err := io.CopyN(buf, r, int64(n))
	return err
}

type Request[T Msg] struct {
	srvr *Server
	id   uint64
	ctx  context.Context

	headersBuf *bytes.Buffer
	headers    [][2][]byte

	bodyBuf *bytes.Buffer
	Data    T

	streamRecvr chan *bytes.Buffer
}

func (r *Request[T]) Context() context.Context {
	return r.ctx
}

func (r *Request[T]) Close() {
	r.headers = nil
	r.srvr.headerBuffers.Put(r.headersBuf)
	r.headersBuf = nil

	r.srvr.bodyBuffers.Put(r.bodyBuf)
	r.bodyBuf = nil
}

type Response[T Msg] struct {
	reqId  uint64
	Status Status
	Data   T
	Error  string
}

func newResponse[T Msg](reqId uint64, msg T, err error) *Response[T] {
	resp := &Response[T]{reqId: reqId, Data: msg}
	if err != nil {
		resp.Error = err.Error()
	}
	return resp
}

var ErrClosed = fmt.Errorf("operation closed")

// TODO: Create/use message interface
type Stream[R, S Msg] struct {
	reqId            uint64
	recvChan         chan R
	sendChan         chan S
	recvReader       io.Reader
	sendWriter       io.Writer
	canSend, canRecv atomic.Bool
}

func (s *Stream[R, S]) Close() error {
	s.canSend.Store(false)
	s.canRecv.Store(false)
	return nil
}

func (s *Stream[R, S]) Send(val S) error {
	return nil
}

func (s *Stream[R, S]) CanSend() bool {
	return s.canSend.Load()
}

func (s *Stream[R, S]) Recv() (val R, err error) {
	return
}

func (s *Stream[R, S]) CanRecv() bool {
	return s.canRecv.Load()
}

type ResponseWriter[T Msg] struct {
	reqId       uint64
	headersLen  int
	headers     [][2][]byte
	trailersLen int
	trailers    [][2][]byte
	lw          *LockedWriter
}

func newResponseWriter[T Msg]() *ResponseWriter[T] {
	return nil
}

func (rw *ResponseWriter[T]) WriteHeader(key, val []byte) {
	rw.headersLen += 4 + len(key) + len(val)
	rw.headers = append(rw.headers, [2][]byte{key, val})
}

func (rw *ResponseWriter[T]) WriteHeaderString(key, val string) {
	rw.headersLen += 4 + len(key) + len(val)
	rw.headers = append(rw.headers, [2][]byte{[]byte(key), []byte(val)})
}

func (rw *ResponseWriter[T]) WriteTrailer(key, val []byte) {
	rw.trailersLen += 4 + len(key) + len(val)
	rw.trailers = append(rw.trailers, [2][]byte{key, val})
}

func (rw *ResponseWriter[T]) WriteTrailerString(key, val string) {
	rw.trailersLen += 4 + len(key) + len(val)
	rw.trailers = append(rw.trailers, [2][]byte{[]byte(key), []byte(val)})
}

// Expects the caller to hold the lock for the LockedWriter
func (rw *ResponseWriter[T]) writeOutHeaders() error {
	w := rw.lw.LockedWriter()
	var buf []byte
	for _, kv := range rw.headers {
		// Do this to get the LE encoded 2-byte key and value lengths in order
		lens := (uint32(len(kv[1])) << 16) | uint32(len(kv[0]))
		// TODO: Is there a better way to do this?
		buf = append(binary.LittleEndian.AppendUint32(buf, lens), kv[0]...)
		buf = append(buf, kv[1]...)
		if err := writeAll(w, buf); err != nil {
			return err
		}
		buf = buf[:0]
	}
	return nil
}

// Expects the caller to hold the lock for the LockedWriter
func (rw *ResponseWriter[T]) writeOutTrailers() error {
	w := rw.lw.LockedWriter()
	var buf []byte
	for _, kv := range rw.trailers {
		// Do this to get the LE encoded 2-byte key and value lengths in order
		lens := (uint32(len(kv[1])) << 16) | uint32(len(kv[0]))
		// TODO: Is there a better way to do this?
		buf = append(binary.LittleEndian.AppendUint32(buf, lens), kv[0]...)
		buf = append(buf, kv[1]...)
		if err := writeAll(w, buf); err != nil {
			return err
		}
		buf = buf[:0]
	}
	return nil
}

// Expects the caller to have locked the locked writer
func (rw *ResponseWriter[T]) writeOutResponse(resp *Response[T]) error {
	if resp.Status == StatusOk && resp.Error != "" {
		resp.Status = StatusErr
	}
	// Write the ID
	w := rw.lw.LockedWriter()
	buf := binary.LittleEndian.AppendUint64(nil, resp.reqId)
	if err := writeAll(w, buf); err != nil {
		return err
	}

	// Write the status and error
	buf = buf[:1]
	buf[0] = byte(resp.Status)
	if resp.Status != StatusOk {
		buf = binary.LittleEndian.AppendUint16(buf, uint16(len(resp.Error)))
		buf = append(buf, resp.Error...)
	}
	if err := writeAll(w, buf); err != nil {
		return err
	}

	// Write the lengths
	buf = buf[:0]
	buf = binary.LittleEndian.AppendUint32(buf, uint32(rw.headersLen))
	buf = binary.LittleEndian.AppendUint64(buf, uint64(resp.Data.SerLen()))
	buf = binary.LittleEndian.AppendUint32(buf, uint32(rw.trailersLen))
	if err := writeAll(w, buf); err != nil {
		return err
	}

	// Write the headers
	if err := rw.writeOutHeaders(); err != nil {
		return err
	}

	// Write the data
	if err := resp.Data.SerTo(w); err != nil {
		return err
	}

	// Write the trailers
	return rw.writeOutTrailers()
}

type Status byte

const (
	StatusOk  Status = 0
	StatusErr Status = 1
)
