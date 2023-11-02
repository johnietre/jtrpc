package jtrpc

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"sync"
	"time"

	utils "github.com/johnietre/utils/go"
	webs "golang.org/x/net/websocket"
)

const (
	// MaxHeadersLen is the maximum number of headers bytes that can be sent.
	MaxHeadersLen = 1<<16 - 1
	// DefaultMaxBodyLen is the default max body length for requests.
	DefaultMaxBodyLen int64 = 1 << 22
	// DefaultMaxStreamBodyLen is the default max body length for stream
	// messages.
	DefaultMaxStreamBodyLen int64 = 1 << 16
	// DefaultConnectTimeout is the default connect timeout.
	DefaultConnectTimeout = time.Second * 10
)

var (
	// The initial bytes without the version.
	initialBytes = []byte{
		0x00, 0x00, 0x00, 0x00, 0x00, 0x01, 0x05,
		'j', 't', 'r', 'p', 'c',
	}

	noopHandler = Handler(HandlerFunc(func(*Request, *Response) {}))
)

// Server is a server.
type Server struct {
	// Addr is the address the server runs on.
	Addr string

	// Mux is the multiplexer used to get handlers.
	Mux Mux

	// Logger is the logger the server logs to.
	Logger *log.Logger

	// MaxBodyLen is the maximum body length accepted. If 0, uses
	// DefaultMaxBodyLen. If < 0, the max length is 1<<63 - 1.
	MaxBodyLen int64
	// MaxStreamBodyLen is the maximum stream message body length accepted. If 0,
	// uses DefaultMaxBodyLen. If < 0, the max length is 1<<63 - 1.
	MaxStreamBodyLen int64

	// ConnectTimeout is the max time for establishing the connection. If it is
	// non-positive, there is no timeout.
	ConnectTimeout time.Duration

	bufPool sync.Pool
}

// NewServer creates a new server.
func NewServer(addr string) *Server {
	return &Server{
		Addr:             addr,
		Mux:              NewMapMux(),
		MaxBodyLen:       DefaultMaxBodyLen,
		MaxStreamBodyLen: DefaultMaxStreamBodyLen,
		ConnectTimeout:   DefaultConnectTimeout,
		Logger:           log.Default(),
		bufPool: sync.Pool{
			New: func() any {
				return bytes.NewBuffer(nil)
			},
		},
	}
}

// Handle adds a handler to the Mux.
func (s *Server) Handle(path string, h Handler, ms ...Middleware) {
	s.Mux.Handle(path, h, ms...)
}

// HandleStream adds a stream handler to the Mux.
func (s *Server) HandleStream(path string, h StreamHandler, ms ...Middleware) {
	s.Mux.HandleStream(path, h, ms...)
}

// Middleware adds middleware to the Mux.
func (s *Server) Middleware(ms ...Middleware) {
	s.Mux.Middleware(ms...)
}

// HandleFunc adds a HandlerFunc to the Mux.
func (s *Server) HandleFunc(
	path string, h func(*Request, *Response), ms ...Middleware,
) {
	s.Mux.Handle(path, HandlerFunc(h), ms...)
}

// HandleStreamFunc adds a StreamHandlerFunc to the Mux.
func (s *Server) HandleStreamFunc(
	path string, h func(*Stream), ms ...Middleware,
) {
	s.Mux.HandleStream(path, StreamHandlerFunc(h), ms...)
}

// Run runs the server.
func (s *Server) Run() error {
	if s.Logger == nil {
		s.Logger = log.Default()
	}
	if s.MaxBodyLen == 0 {
		s.MaxBodyLen = DefaultMaxBodyLen
	} else if s.MaxBodyLen < 0 {
		s.MaxBodyLen = 1<<63 - 1
	}
	if s.MaxStreamBodyLen == 0 {
		s.MaxStreamBodyLen = DefaultMaxStreamBodyLen
	} else if s.MaxStreamBodyLen < 0 {
		s.MaxStreamBodyLen = 1<<63 - 1
	}

	ln, err := net.Listen("tcp", s.Addr)
	if err != nil {
		return err
	}
	for {
		c, err := ln.Accept()
		if err != nil {
			return err
		}
		go s.handle(c)
	}
}

type serverConn struct {
	net.Conn
	reqs    *utils.Mutex[map[uint64]*Request]
	streams *utils.Mutex[map[uint64]*Stream]
}

func newServerConn(conn net.Conn) *serverConn {
	return &serverConn{
		Conn:    conn,
		reqs:    utils.NewMutex(make(map[uint64]*Request)),
		streams: utils.NewMutex(make(map[uint64]*Stream)),
	}
}

// Close closes the conn and all it's streams.
func (c *serverConn) Close() error {
	err := c.Conn.Close()
	c.streams.Apply(func(mp *map[uint64]*Stream) {
		for _, stream := range *mp {
			stream.close(false)
		}
	})
	return err
}

func (s *Server) handle(nconn net.Conn) {
	var initial [14]byte
	setReadTimeout(nconn, s.ConnectTimeout)
	if _, err := io.ReadFull(nconn, initial[:]); err != nil {
		// TODO: Handle error
		nconn.Close()
		return
	}
	setReadTimeout(nconn, 0)
	// Check the bytes
	if !bytes.Equal(initial[:12], initialBytes) {
		writeRespW(nconn, 0, StatusInvalidInitialBytes)
		nconn.Close()
		return
	}
	// Get the version
	major, minor := initial[12], initial[13]
	_, _ = major, minor

	// Send the response
	if _, err := writeRespW(nconn, 0, StatusOK); err != nil {
		// TODO: Handle error
		return
	}

	conn := newServerConn(nconn)
	s.handleServerConn(conn)
}

func (s *Server) handleServerConn(conn *serverConn) {
	defer conn.Close()
	lw := utils.NewLockedWriter(conn)
	for {
		buf := s.bufPool.Get().(*bytes.Buffer)
		// Get the ID, flags, and lengths
		// 8 + 1
		if _, err := io.CopyN(buf, conn, 9); err != nil {
			// TODO: Handle error?
			return
		}
		id := get8(buf.Next(8))
		flags := buf.Next(1)[0]
		if !hasStreamMsgFlag(flags) {
			if err := s.handleReq(conn, lw, id, flags, buf); err != nil {
				// TODO: Handle error?
				return
			}
			continue
		}
		// Check for stream
		var stream *Stream
		conn.streams.Apply(func(mp *map[uint64]*Stream) {
			stream = (*mp)[id]
		})
		if stream == nil {
			msg := Message{reqId: id, flags: MsgFlagNotStream}
			_, err := msg.WriteTo(lw.LockWriter())
			lw.Unlock()
			if err != nil {
				// TODO: Handle error?
				return
			}
			continue
		}
		if err := s.handleStreamMsg(conn, stream, flags, buf); err != nil {
			// TODO: Handle error?
			return
		}
	}
}

// Handle stream msg
func (s *Server) handleStreamMsg(
	conn *serverConn, stream *Stream, flags byte, buf *bytes.Buffer,
) error {
	if _, err := io.CopyN(buf, conn, 8); err != nil {
		return err
	}
	msgLen := int64(get8(buf.Next(8)))
	if hasCloseFlag(flags) {
		var err error
		msg := Message{reqId: stream.req.id, flags: flags}
		if msgLen != 0 {
			// Still check message len
			if msgLen > s.MaxStreamBodyLen {
				errMsg := Message{
					reqId: stream.req.id,
					flags: FlagStreamMsg | MsgFlagTooLarge,
				}
				errMsg.SetBodyBytes(append8(nil, uint64(s.MaxStreamBodyLen)))
				stream.Send(errMsg)
				_, err = io.CopyN(io.Discard, conn, msgLen)
			} else {
				if _, err := io.CopyN(buf, conn, int64(msgLen)); err != nil {
					// TODO: Close stream anyway (right now, returning an error should
					// close the conn, therefore, closing the all streams)?
					return err
				}
				msg.Body = bytes.NewBuffer(utils.CloneSlice(buf.Bytes()))
				buf.Reset()
				s.bufPool.Put(buf)
			}
		}
		stream.closeWithMsgRecvd(msg)
		// TODO: Handle error
		return err
	}
	// Check the message body length
	if msgLen > s.MaxStreamBodyLen {
		msg := Message{
			reqId: stream.req.id,
			flags: FlagStreamMsg | MsgFlagTooLarge,
		}
		msg.SetBodyBytes(append8(nil, uint64(s.MaxStreamBodyLen)))
		stream.Send(msg)
		_, err := io.CopyN(io.Discard, conn, msgLen)
		return err
	}
	if _, err := io.CopyN(buf, conn, int64(msgLen)); err != nil {
		return err
	}
	// TODO: Handle error
	bodyBuf := bytes.NewBuffer(utils.CloneSlice(buf.Bytes()))
	buf.Reset()
	s.bufPool.Put(buf)
	return stream.addMsg(Message{
		reqId: stream.req.id, flags: flags, Body: bodyBuf,
	})
}

func (s *Server) handleReq(
	conn *serverConn, lw *utils.LockedWriter,
	id uint64, flags byte, buf *bytes.Buffer,
) error {
	remoteAddr := conn.RemoteAddr().String()
	hasStream := hasStreamFlag(flags)
	// Get the rest of the lengths
	if _, err := io.CopyN(buf, conn, 12); err != nil {
		return err
	}
	pathLen := int(get2(buf.Next(2)))
	headersLen := int(get2(buf.Next(2)))
	bodyLen := int(get8(buf.Next(8)))
	// Check the body length
	if bl := int64(bodyLen); bl > s.MaxBodyLen {
		writeRespMsg(lw, id, StatusBodyTooLarge, fmt.Sprint(bl))
		_, err := io.CopyN(buf, conn, bl+int64(pathLen+headersLen))
		return err
	}
	// Get path
	if _, err := io.CopyN(buf, conn, int64(pathLen)); err != nil {
		return err
	}
	path := string(buf.Next(pathLen))
	// Check path
	if hasStream {
		// Check for stream handler
		if streamHandler := s.Mux.GetStreamHandler(path); streamHandler != nil {
			// Get headers
			if _, err := io.CopyN(buf, conn, int64(headersLen)); err != nil {
				return err
			}
			headerBytes := utils.CloneSlice(buf.Next(headersLen))
			// Get body
			if _, err := io.CopyN(buf, conn, int64(bodyLen)); err != nil {
				return err
			}
			// Call the handler
			req := newRequest(id, remoteAddr, flags, path, headerBytes)
			req.Body = buf
			conn.reqs.Apply(func(mp *map[uint64]*Request) {
				(*mp)[req.id] = req
			})
			resp := &Response{reqId: id, flags: flags}
			handler := noopHandler
			// Add middlewares
			if mw := s.Mux.GetMiddleware(); mw != nil {
				// Pass a handler that does nothing just so that middleware can be run
				handler = mw(handler)
			}
			if mw := s.Mux.GetStreamMiddleware(path); mw != nil {
				handler = mw(handler)
			}
			// Run the and add the stream if necessary
			go func() {
				handler.Handle(req, resp)
				conn.reqs.Apply(func(mp *map[uint64]*Request) {
					delete(*mp, req.id)
				})
				// Write the response
				// TODO: Handle error
				_, err := resp.WriteTo(lw.LockWriter())
				lw.Unlock()
				req.Body.Reset()
				s.bufPool.Put(req.Body)
				// Handle stream
				if resp.StatusCode == StatusOK && err == nil {
					stream := newStream(lw, conn, req)
					conn.streams.Apply(func(mp *map[uint64]*Stream) {
						(*mp)[stream.req.id] = stream
					})
					streamHandler.HandleStream(stream)
				}
			}()
			return nil
		}
		// Check if it is a regular handler
		if handler := s.Mux.GetHandler(path); handler != nil {
			writeResp(lw, id, StatusNotStream)
			_, err := io.CopyN(io.Discard, conn, int64(headersLen+bodyLen))
			return err
		}
		// Not found
		// TODO: Handle error
		writeResp(lw, id, StatusNotFound)
		_, err := io.CopyN(io.Discard, conn, int64(headersLen+bodyLen))
		return err
	}

	handler := s.Mux.GetHandler(path)
	if handler == nil {
		// TODO: Handle error?
		writeResp(lw, id, StatusNotFound)
		_, err := io.CopyN(io.Discard, conn, int64(headersLen+bodyLen))
		return err
	}
	// Get headers
	if _, err := io.CopyN(buf, conn, int64(headersLen)); err != nil {
		return err
	}
	headerBytes := utils.CloneSlice(buf.Next(headersLen))
	// Get body
	if _, err := io.CopyN(buf, conn, int64(bodyLen)); err != nil {
		return err
	}
	// Call the handler
	req := newRequest(id, remoteAddr, flags, path, headerBytes)
	req.Body = buf
	conn.reqs.Apply(func(mp *map[uint64]*Request) {
		(*mp)[req.id] = req
	})
	resp := &Response{reqId: id}
	// Pass to middleware and get new handler
	if mw := s.Mux.GetMiddleware(); mw != nil {
		handler = mw(handler)
	}
	go func() {
		handler.Handle(req, resp)
		conn.reqs.Apply(func(mp *map[uint64]*Request) {
			delete(*mp, req.id)
		})
		// Write the response
		// TODO: Handle error
		resp.WriteTo(lw.LockWriter())
		lw.Unlock()
		req.Body.Reset()
		s.bufPool.Put(req.Body)
	}()
	return nil
}

// ServeHTTP implements the http.Handler interface. It can handle regular
// requests and websockets. If the path is not empty ("" or "/"), the handler
// is attempted to be retrieved. If the handler is not a stream, currently,
// any HTTP method is allowed. If the handler is a stream, a websocket hijack
// is attempted. The websocket client should send a jtrpc Request through the
// websocket to which a response will be sent. If the resposne status is not
// OK, the websocket is closed. Otherwise, the websocket can be treated like a
// normal stream. If the path is empty, then the websocket will be treated the
// same as a TCP connection connecting to the server normally. The websocket
// should follow the same process as any other TCP connection (e.g., sending
// initial connection bytes).
// When checking the path in the request, if there is no leading slash, a
// leading slash is appended. Then, if the path is not a match with the leading
// slash, it is removed and another attempt is made. The process is repeated
// for stream handlers if no regular handler is matched.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	makeReq := func(path string) *Request {
		headersMap := make(map[string]string, len(r.Header))
		for k, v := range r.Header {
			l := len(v)
			if l != 0 {
				headersMap[k] = v[l-1]
			} else {
				headersMap[k] = ""
			}
		}
		mbr := http.MaxBytesReader(w, r.Body, s.MaxBodyLen)
		body := bytes.NewBuffer(nil)
		if n, err := io.Copy(body, mbr); err != nil {
			http.Error(w, fmt.Sprint(s.MaxBodyLen), http.StatusRequestEntityTooLarge)
			return nil
		} else if n == 0 {
			body = nil
		}
		return &Request{
			ctx:        context.WithValue(r.Context(), httpReqCtxKey, r),
			RemoteAddr: r.RemoteAddr,
			Path:       path,
			Headers:    &Headers{m: headersMap},
			Body:       body,
		}
	}
	path := r.URL.Path
	if path == "" || path[0] != '/' {
		path = "/" + path
	}
	if path == "/" {
		webs.Handler(s.wsHandler).ServeHTTP(w, r)
		return
	}
	// Check for regular handler
	handler := s.Mux.GetHandler(path)
	if handler == nil {
		path = path[1:]
		handler = s.Mux.GetHandler(path)
	}
	if handler != nil {
		if mw := s.Mux.GetMiddleware(); mw != nil {
			handler = mw(handler)
		}
		req := makeReq(path)
		if req == nil {
			return
		}
		resp := &Response{
			Request: req,
		}
		handler.Handle(req, resp)
		for k, v := range resp.Headers {
			w.Header().Set(k, v)
		}
		w.WriteHeader(StatusToHTTP(resp.StatusCode))
		if resp.body != nil {
			io.CopyN(w, resp.body, int64(resp.bodyLen))
			resp.body.Close()
		}
		return
	}
	path = "/" + path
	streamHandler := s.Mux.GetStreamHandler(path)
	if streamHandler == nil {
		path = path[1:]
		streamHandler = s.Mux.GetStreamHandler(path)
	}
	if streamHandler == nil {
		http.NotFound(w, r)
		return
	}
	s.wsStreamHandler(w, r, path, streamHandler)
}

func (s *Server) wsHandler(ws *webs.Conn) {
	s.handle(ws)
}

func (s *Server) wsStreamHandler(
	w http.ResponseWriter, r *http.Request,
	path string, streamHandler StreamHandler,
) {
	webs.Handler(func(ws *webs.Conn) {
		req, err := RequestFromReader(ws, s.MaxBodyLen)
		if err != nil {
			if err == ErrBodyTooLarge {
				resp := &Response{StatusCode: StatusBodyTooLarge}
				resp.SetBodyString(ErrBodyTooLarge.Error())
				resp.WriteTo(ws)
			}
			return
		}
		resp := &Response{reqId: req.id, flags: req.Flags}
		handler := noopHandler
		if mw := s.Mux.GetMiddleware(); mw != nil {
			handler = mw(handler)
		}
		if mw := s.Mux.GetStreamMiddleware(path); mw != nil {
			handler = mw(handler)
		}
		// Run the and add the stream if necessary
		handler.Handle(req, resp)
		_, err = resp.WriteTo(ws)
		// Handle stream
		if resp.StatusCode == StatusOK && err == nil {
			sconn := newServerConn(ws)
			lw := utils.NewLockedWriter(sconn)
			stream := newStream(lw, sconn, req)
			streamHandler.HandleStream(stream)
		}
	}).ServeHTTP(w, r)
}

type httpReqCtxKeyType string

const httpReqCtxKey httpReqCtxKeyType = "jtrpc"

// GetHTTPRequest returns the http.Request the jtrpc.Request was created from,
// if there was one. Currently doesn't work with websockets.
func GetHTTPRequest(ctx context.Context) *http.Request {
	ir := ctx.Value(httpReqCtxKey)
	if ir == nil {
		return nil
	}
	return ir.(*http.Request)
}

func setReadTimeout(conn net.Conn, dur time.Duration) {
	if dur <= 0 {
		conn.SetReadDeadline(time.Time{})
	} else {
		conn.SetReadDeadline(time.Now().Add(dur))
	}
}

func setWriteTimeout(conn net.Conn, dur time.Duration) {
	if dur <= 0 {
		conn.SetWriteDeadline(time.Time{})
	} else {
		conn.SetWriteDeadline(time.Now().Add(dur))
	}
}
