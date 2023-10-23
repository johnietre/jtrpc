package jtrpc

import (
	"bytes"
	"container/list"
	"fmt"
	"io"
	"sync"
	"sync/atomic"

	utils "github.com/johnietre/utils/go"
)

const (
	// MsgFlagNotStream signfies the requested path is not a stream.
	MsgFlagNotStream = 0b0100_0000
	// MsgFlagTooLarge signifies the message was too large.
	MsgFlagTooLarge = 0b0010_0000
	// MsgFlagClose signifies the stream should (will) be closed.
	MsgFlagClose = 0b0000_0001
)

var (
	// ErrStreamClosed means the stream has been closed.
	ErrStreamClosed = fmt.Errorf("stream closed")
)

// Stream represents a stream.
type Stream struct {
	lw           *utils.LockedWriter
	conn         *serverConn
	req          *Request
	msgChan      chan Message
	msgBuffer    *list.List
	msgBufferMtx sync.Mutex
	isClosed     atomic.Bool
}

func newStream(
	lw *utils.LockedWriter, conn *serverConn, req *Request,
) *Stream {
	return &Stream{
		lw:   lw,
		conn: conn,
		req:  req,
		// TODO: Chan len
		msgChan:   make(chan Message, 50),
		msgBuffer: list.New(),
	}
}

// Request returns the request associated with the stream.
func (s *Stream) Request() *Request {
	return s.req
}

func (s *Stream) addMsg(msg Message) error {
	if s.isClosed.Load() {
		return ErrStreamClosed
	}
	s.msgBufferMtx.Lock()
	defer s.msgBufferMtx.Unlock()
	for e := s.msgBuffer.Front(); e != nil; e = e.Next() {
		select {
		case s.msgChan <- e.Value.(Message):
			tmp := e
			e = e.Next()
			s.msgBuffer.Remove(tmp)
		default:
			s.msgBuffer.PushBack(msg)
			return nil
		}
	}
	select {
	case s.msgChan <- msg:
	default:
		s.msgBuffer.PushBack(msg)
	}
	return nil
}

// Send sends a message on the stream.
func (s *Stream) Send(msg Message) error {
	if s.IsClosed() {
		return ErrStreamClosed
	}
	return s.send(msg)
}

func (s *Stream) send(msg Message) error {
	// TODO: Handle error
	msg.reqId = s.req.id
	_, err := msg.WriteTo(s.lw.LockWriter())
	s.lw.Unlock()
	return err
}

// Recv receives a messasge from the stream.
func (s *Stream) Recv() (Message, error) {
	if s.IsClosed() {
		return Message{}, ErrStreamClosed
	}
	msg, ok := <-s.msgChan
	if !ok {
		return Message{}, ErrStreamClosed
	}
	s.msgBufferMtx.Lock()
	// Add another one to the channel if possible
	if s.msgBuffer.Len() != 0 {
		e := s.msgBuffer.Front()
		s.msgChan <- e.Value.(Message)
		s.msgBuffer.Remove(e)
	}
	s.msgBufferMtx.Unlock()
	return msg, nil
}

// IsClosed returns whether the stream is closed.
func (s *Stream) IsClosed() bool {
	return s.isClosed.Load()
}

// Close closes the stream.
func (s *Stream) Close() error {
	return s.close(true)
}

func (s *Stream) close(send bool) error {
	if s.isClosed.Swap(true) {
		return ErrStreamClosed
	}
	close(s.msgChan)
	s.conn.streams.Apply(func(mp *map[uint64]*Stream) {
		delete(*mp, s.req.id)
	})
	if !send {
		return nil
	}
	return s.send(Message{flags: MsgFlagClose, Body: nil})
}

// Message is a message sent/received to/from a stream.
type Message struct {
	reqId uint64
	flags byte
	// Body is the body of the stream.
	Body *bytes.Buffer
}

// BodyBytes returns the message body as a byte slice.
func (msg Message) BodyBytes() []byte {
	if msg.Body == nil {
		return nil
	}
	return msg.Body.Bytes()
}

// BodyString returns the message body as a string.
func (msg Message) BodyString() string {
	if msg.Body == nil {
		return ""
	}
	return msg.Body.String()
}

// SetBodyBytes sets the message body to the given bytes.
func (msg *Message) SetBodyBytes(b []byte) {
	if msg.Body == nil {
		msg.Body = &bytes.Buffer{}
	}
	msg.Body = bytes.NewBuffer(b)
}

// SetBodyString sets the message body to the given string.
func (msg *Message) SetBodyString(s string) {
	if msg.Body == nil {
		msg.Body = &bytes.Buffer{}
	}
	msg.Body = bytes.NewBufferString(s)
}

// WriteTo writes the message to the given writer.
func (msg Message) WriteTo(w io.Writer) (n int64, err error) {
	msg.flags |= FlagStreamMsg
	buf := [17]byte{}
	place8(buf[:], msg.reqId)
	buf[8] = msg.flags
	if msg.Body != nil {
		l := uint64(msg.Body.Len())
		place8(buf[9:], l)
		n, err = utils.WriteAll(w, buf[:])
		if err == nil {
			var nw int64
			nw, err = io.CopyN(w, msg.Body, int64(l))
			n += nw
		}
	} else {
		n, err = utils.WriteAll(w, buf[:])
	}
	return
}
