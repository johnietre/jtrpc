package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"strconv"
	"sync"

	webs "golang.org/x/net/websocket"
)

const (
  initialBytesLen = 16
  majorVersion uint16 = 0
  minorVersion uint16 = 1
)
var (
  initialBytesBytes = [initialBytesLen]byte{
    0, 0, 0, 0, 0, 1, 5,
    'j', 't', 'r', 'p', 'c',
    0, 0,
    1, 0,
  }
)

type Server struct {
  addr string
  ln net.Listener

  MaxHeaderLen uint32
  headerBuffers sync.Pool

  // MaxBodyLen value should not be greater than 1 << 63 - 1
  MaxBodyLen uint64
  bodyBuffers sync.Pool
}

func NewServer(addr string) *Server {
  return &Server{
    MaxHeaderLen: DefaultMaxHeaderLen,
    MaxBodyLen: DefaultMaxBodyLen,
    headerBuffers: sync.Pool{
      New: func() any {
        return new(bytes.Buffer)
      },
    },
    bodyBuffers: sync.Pool{
      New: func() any {
        return new(bytes.Buffer)
      },
    },
  }
}

func (s *Server) Run() error {
  ln, err := net.Listen("tcp", s.addr)
  if err != nil {
    return err
  }
  for {
    c, err := ln.Accept()
    if err != nil {
      return err
    }
    go s.handleConn(c)
  }
  //return nil
}

func (s *Server) handleConn(c net.Conn) {
  defer c.Close()
  lw := NewLockedWriter(c)

  maxHeaderLen := int64(s.MaxHeaderLen)
  if maxHeaderLen == 0 {
    maxHeaderLen = 1 << 32 - 1
  }
  maxBodyLen := int64(s.MaxBodyLen)
  if maxBodyLen == 0 {
    maxBodyLen = 1 << 63 - 1
  }
  for {
    buf := s.headerBuffers.Get().(*bytes.Buffer)
    buf.Reset()
    if _, err := io.CopyN(buf, c, initialBytesLen); err != nil {
      // TODO
      if err != io.ErrUnexpectedEOF {}
      return
    }
    // TODO: Handle version
    if !bytes.Equal(buf.Bytes(), initialBytesBytes[:]) {
      // TODO: Send error
      return
    }
    buf.Reset()

    // Get the request ID
    if _, err := io.CopyN(buf, c, 8); err != nil {
      // TODO
      if err != io.ErrUnexpectedEOF {}
      return
    }
    id := binary.LittleEndian.Uint64(buf.Bytes())
    // TODO: Check to see if the ID is part of stream
    buf.Reset()

    // Get the func name/path
    if _, err := io.CopyN(buf, c, 2); err != nil {
      // TODO
      if err != io.ErrUnexpectedEOF {}
      return
    }
    fnLen := int64(binary.LittleEndian.Uint16(buf.Bytes()))
    buf.Reset()
    if _, err := io.CopyN(buf, c, fnLen); err != nil {
      // TODO
      if err != io.ErrUnexpectedEOF {}
      return
    }
    fnName := string(buf.Bytes())
    buf.Reset()

    // Get the headers length
    if _, err := io.CopyN(buf, c, 4); err != nil {
      // TODO
      if err != io.ErrUnexpectedEOF {}
      return
    }
    headersLen := int64(binary.LittleEndian.Uint32(buf.Bytes()))
    buf.Reset()
    if headersLen > maxHeaderLen {
      // TODO
      return
    }

    // Get the body length
    if _, err := io.CopyN(buf, c, 8); err != nil {
      // TODO
      if err != io.ErrUnexpectedEOF {}
      return
    }
    bodyLen := int64(binary.LittleEndian.Uint64(buf.Bytes()))
    buf.Reset()
    if bodyLen > maxHeaderLen {
      // TODO
      return
    }

    var err error
    switch fnName {
    case "add":
      hb := s.headerBuffers.Get().(*bytes.Buffer)
      bb := s.bodyBuffers.Get().(*bytes.Buffer)
      err = readHeadersBodyInto(c, hb, uint32(headersLen), bb, uint64(bodyLen))
      r := newRequest[*Input](s, id, context.Background(), hb, bb)
      rw := &ResponseWriter[*Output]{reqId: id, lw: lw}
      go s.handleAdd(r, rw)
    case "sub":
      hb := s.headerBuffers.Get().(*bytes.Buffer)
      bb := s.bodyBuffers.Get().(*bytes.Buffer)
      err = readHeadersBodyInto(c, hb, uint32(headersLen), bb, uint64(bodyLen))
      r := newRequest[*Input](s, id, context.Background(), hb, bb)
      rw := &ResponseWriter[*Output]{reqId: id, lw: lw}
      go s.handleSub(r, rw)
    case "mul":
      hb := s.headerBuffers.Get().(*bytes.Buffer)
      bb := s.bodyBuffers.Get().(*bytes.Buffer)
      err = readHeadersBodyInto(c, hb, uint32(headersLen), bb, uint64(bodyLen))
      r := newRequest[*Input](s, id, context.Background(), hb, bb)
      rw := &ResponseWriter[*Output]{reqId: id, lw: lw}
      go s.handleMul(r, rw)
    case "div":
      hb := s.headerBuffers.Get().(*bytes.Buffer)
      bb := s.bodyBuffers.Get().(*bytes.Buffer)
      err = readHeadersBodyInto(c, hb, uint32(headersLen), bb, uint64(bodyLen))
      r := newRequest[*Input](s, id, context.Background(), hb, bb)
      rw := &ResponseWriter[*Output]{reqId: id, lw: lw}
      go s.handleDiv(r, rw)
    case "pow":
      hb := s.headerBuffers.Get().(*bytes.Buffer)
      bb := s.bodyBuffers.Get().(*bytes.Buffer)
      err = readHeadersBodyInto(c, hb, uint32(headersLen), bb, uint64(bodyLen))
      r := newRequest[*Input](s, id, context.Background(), hb, bb)
      rw := &ResponseWriter[*Output]{reqId: id, lw: lw}
      go s.handlePow(r, rw)
    case "stream_add":
    default:
      // TODO
      // Read/discard the headers/body
      if _, err = io.CopyN(io.Discard, c, headersLen); err != nil {
      } else if _, err = io.CopyN(io.Discard, c, bodyLen); err != nil {
      } else {
        // TODO: Send error
      }
    }
    if err != nil {
    }
  }
}

func newRequest[T Msg](
  srvr *Server, id uint64, ctx context.Context, hb, bb *bytes.Buffer,
) *Request[T] {
  return &Request[T]{
    srvr: srvr,
    id: id,
    ctx: context.Background(),
    headersBuf: hb,
    bodyBuf: bb,
  }
}

func readHeadersBodyInto(
  r io.Reader,
  headersBuf *bytes.Buffer, headersLen uint32,
  bodyBuf *bytes.Buffer, bodyLen uint64,
) error {
  if err := readHeadersInto(r, headersBuf, headersLen); err != nil {
    return err
  }
  return readBodyInto(r, bodyBuf, bodyLen)
}

type reqCtxKeyType string
type respWriterCtxKeyType string

const (
  reqCtxKey = "reqCtxKey"
  respWriterCtxKey = "respWriterCtxKey"
)

func newContext[Req, Resp Msg](r *Request[Req], w *ResponseWriter[Resp]) context.Context {
  return context.WithValue(
    context.WithValue(r.ctx, respWriterCtxKey, w),
    reqCtxKey, r,
  )
}

func (s *Server) handleAdd(r *Request[*Input], rw *ResponseWriter[*Output]) {
  ctx := newContext(r, rw)
  msg, err := s.Add(ctx, r.Data)
  resp := newResponse(r.id, msg, err)
  rw.lw.Lock()
  if err := rw.writeOutResponse(resp); err != nil {
    // TODO
  }
  println(msg, err)
}

func (s *Server) handleSub(r *Request[*Input], rw *ResponseWriter[*Output]) {
  ctx := newContext(r, rw)
  msg, err := s.Sub(ctx, r.Data)
  println(msg, err)
}

func (s *Server) handleMul(r *Request[*Input], rw *ResponseWriter[*Output]) {
  ctx := newContext(r, rw)
  msg, err := s.Mul(ctx, r.Data)
  println(msg, err)
}

func (s *Server) handleDiv(r *Request[*Input], rw *ResponseWriter[*Output]) {
  ctx := newContext(r, rw)
  msg, err := s.Div(ctx, r.Data)
  println(msg, err)
}

func (s *Server) handlePow(r *Request[*Input], rw *ResponseWriter[*Output]) {
  ctx := newContext(r, rw)
  msg, err := s.Pow(ctx, r.Data)
  println(msg, err)
}

func (s *Server) handleStreamAdd(r *Request[*Input], w *ResponseWriter[*Output]) {
}

func (s *Server) Add(ctx context.Context, in *Input) (*Output, error) {
  return &Output{Value: in.A + in.B}, nil
}

func (s *Server) Sub(ctx context.Context, in *Input) (*Output, error) {
  return &Output{Value: in.A - in.B}, nil
}

func (s *Server) Mul(ctx context.Context, in *Input) (*Output, error) {
  return &Output{Value: in.A * in.B}, nil
}

func (s *Server) Div(ctx context.Context, in *Input) (*Output, error) {
  if in.B == 0.0 {
    return nil, fmt.Errorf("divide by 0")
  }
  return &Output{Value: in.A / in.B}, nil
}

func (s *Server) Pow(ctx context.Context, in *Input) (*Output, error) {
  return &Output{Value: math.Pow(in.A, in.B)}, nil
}

func (s *Server) StreamAdd(stream *Stream[*Input, *Output]) error {
  for {
    in, err := stream.Recv()
    if err != nil {
      // TODO
      return err
    }
    if err = stream.Send(&Output{Value: in.A + in.B}); err != nil {
      // TODO
      return err
    }
  }
  //return nil
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
  name := r.URL.Path
  if len(name) != 0 && name[0] == '/' {
    name = name[1:]
  }
  name = r.Header.Get("JTRPC-FUNC")
  switch name {
  case "add":
    s.ServeAddHTTP(w, r)
  case "sub":
    s.ServeSubHTTP(w, r)
  case "mul":
    s.ServeMulHTTP(w, r)
  case "div":
    s.ServeDivHTTP(w, r)
  case "pow":
    s.ServePowHTTP(w, r)
  case "stream_add":
  default:
    http.Error(w, "not found", http.StatusNotFound)
  }
}

func (s *Server) ServeAddHTTP(w http.ResponseWriter, r *http.Request) {
  id, err := strconv.ParseUint(r.Header.Get("JTRPC-REQ-ID"), 10, 64)
  if err != nil {
    http.Error(w, "missing request id", http.StatusInternalServerError)
    return
  }
  req := Request[*Input]{id: id}
  if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
    // TODO
    http.Error(w, "internal server error", http.StatusInternalServerError)
    return
  }
  out, err := s.Add(req.Data)
  resp := &Response[*Output]{reqId: req.id, Data: out, Error: errStr(err)}
  if err := json.NewEncoder(w).Encode(resp); err != nil {
    // TODO
    http.Error(w, "internal server error", http.StatusInternalServerError)
  }
}

func (s *Server) ServeSubHTTP(w http.ResponseWriter, r *http.Request) {
  id, err := strconv.ParseUint(r.Header.Get("JTRPC-REQ-ID"), 10, 64)
  if err != nil {
    http.Error(w, "missing request id", http.StatusInternalServerError)
    return
  }
  req := Request[*Input]{id: id}
  if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
    // TODO
    http.Error(w, "internal server error", http.StatusInternalServerError)
    return
  }
  out, err := s.Sub(req.Data)
  resp := &Response[*Output]{reqId: req.id, Data: out, Error: errStr(err)}
  if err := json.NewEncoder(w).Encode(resp); err != nil {
    // TODO
    http.Error(w, "internal server error", http.StatusInternalServerError)
  }
}

func (s *Server) ServeMulHTTP(w http.ResponseWriter, r *http.Request) {
  id, err := strconv.ParseUint(r.Header.Get("JTRPC-REQ-ID"), 10, 64)
  if err != nil {
    http.Error(w, "missing request id", http.StatusInternalServerError)
    return
  }
  req := Request[*Input]{id: id}
  if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
    // TODO
    http.Error(w, "internal server error", http.StatusInternalServerError)
    return
  }
  out, err := s.Mul(req.Data)
  resp := &Response[*Output]{reqId: req.id, Data: out, Error: errStr(err)}
  if err := json.NewEncoder(w).Encode(resp); err != nil {
    // TODO
    http.Error(w, "internal server error", http.StatusInternalServerError)
  }
}

func (s *Server) ServeDivHTTP(w http.ResponseWriter, r *http.Request) {
  id, err := strconv.ParseUint(r.Header.Get("JTRPC-REQ-ID"), 10, 64)
  if err != nil {
    http.Error(w, "missing request id", http.StatusInternalServerError)
    return
  }
  req := Request[*Input]{id: id}
  if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
    // TODO
    http.Error(w, "internal server error", http.StatusInternalServerError)
    return
  }
  out, err := s.Div(req.Data)
  resp := &Response[*Output]{reqId: req.id, Data: out, Error: errStr(err)}
  if err := json.NewEncoder(w).Encode(resp); err != nil {
    // TODO
    http.Error(w, "internal server error", http.StatusInternalServerError)
  }
}

func (s *Server) ServePowHTTP(w http.ResponseWriter, r *http.Request) {
  id, err := strconv.ParseUint(r.Header.Get("JTRPC-REQ-ID"), 10, 64)
  if err != nil {
    http.Error(w, "missing request id", http.StatusInternalServerError)
    return
  }
  req := Request[*Input]{id: id}
  if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
    // TODO
    http.Error(w, "internal server error", http.StatusInternalServerError)
    return
  }
  out, err := s.Pow(req.Data)
  resp := &Response[*Output]{reqId: req.id, Data: out, Error: errStr(err)}
  if err := json.NewEncoder(w).Encode(resp); err != nil {
    // TODO
    http.Error(w, "internal server error", http.StatusInternalServerError)
  }
}

func (s *Server) ServeStreamAdd(w http.ResponseWriter, r *http.Request) {
  id, err := strconv.ParseUint(r.Header.Get("JTRPC-REQ-ID"), 10, 64)
  if err != nil {
    http.Error(w, "missing request id", http.StatusInternalServerError)
    return
  }
  ws := getWs(w, r)
  if ws == nil {
    return
  }
  // TODO
  print(id)
}
