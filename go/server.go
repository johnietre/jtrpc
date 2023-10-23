package jtrpc

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"net"
	"sync"
	"time"

	utils "github.com/johnietre/utils/go"
)

const (
	// MaxHeadersLen is the maximum number of headers bytes that can be sent.
	MaxHeadersLen = 1<<16 - 1
	// DefaultMaxBodyLen is the default max body length for requests.
	DefaultMaxBodyLen int64 = 1 << 22
	// DefaultMaxStreamBodyLen is the default max body length for stream
	// messages.
	DefaultMaxStreamBodyLen int64 = 1 << 16
)

var (
	// The initial bytes without the version.
	initialBytes = []byte{
		0x00, 0x00, 0x00, 0x00, 0x00, 0x01, 0x05,
		'j', 't', 'r', 'p', 'c',
	}
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

	// ConnectTimeout is the max time for establishing the connection.
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
		Logger:           log.Default(),
		bufPool: sync.Pool{
			New: func() any {
				return bytes.NewBuffer(nil)
			},
		},
	}
}

// Handle adds a handler to the Mux.
func (s *Server) Handle(path string, h Handler) {
	s.Mux.Handle(path, h)
}

// HandleStream adds a stream handler to the Mux.
func (s *Server) HandleStream(path string, h StreamHandler) {
	s.Mux.HandleStream(path, h)
}

// HandleFunc adds a HandlerFunc to the Mux.
func (s *Server) HandleFunc(path string, h func(*Request, *Response)) {
	s.Mux.Handle(path, HandlerFunc(h))
}

// HandleStreamFunc adds a StreamHandlerFunc to the Mux.
func (s *Server) HandleStreamFunc(path string, h func(*Stream)) {
	s.Mux.HandleStream(path, StreamHandlerFunc(h))
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
		if msgLen != 0 {
			// TODO?
			// TODO: Handle error
			_, err = io.CopyN(io.Discard, conn, msgLen)
		}
		// TODO: Handle error
		if e := stream.close(false); e != nil && err == nil {
			err = e
		}
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
		_, err := io.CopyN(buf, conn, msgLen)
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
		// Make sure the request is good
		if bodyLen != 0 {
			// TODO: Handle error
			writeRespMsg(lw, id, StatusBadRequest, "body length should be 0")
			_, err := io.CopyN(io.Discard, conn, int64(headersLen+bodyLen))
			return err
		}
		// Check for stream handler
		if streamHandler := s.Mux.GetStreamHandler(path); streamHandler != nil {
			// Get headers
			if _, err := io.CopyN(buf, conn, int64(headersLen)); err != nil {
				return err
			}
			headerBytes := utils.CloneSlice(buf.Next(headersLen))
			req := newRequest(id, remoteAddr, flags, path, headerBytes)
			// Write the response
			_, err := writeResp(lw, id, StatusOK)
			// Handle stream
			if err == nil {
				stream := newStream(lw, conn, req)
				conn.streams.Apply(func(mp *map[uint64]*Stream) {
					(*mp)[stream.req.id] = stream
				})
				go func() {
					streamHandler.HandleStream(stream)
				}()
			}
			// TODO: Handle error
			return err
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
