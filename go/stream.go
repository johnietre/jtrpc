package jtrpc

import (
	"bytes"
	"container/list"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"time"

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

func hasMsgFlagClose(flags byte) bool {
	return flags&MsgFlagClose != 0
}

var (
	// ErrTimedOut means an operation timed out.
	ErrTimedOut = fmt.Errorf("timed out")
)

// Stream represents a stream.
type Stream struct {
	lw           *utils.LockedWriter
	streamsMtx   *utils.RWMutex[map[uint64]*Stream]
	req          *Request
	msgChan      chan Message
	msgBuffer    *list.List
	msgBufferMtx sync.Mutex
	deadline     *utils.AValue[time.Time]
	isClosed     atomic.Bool
	closeErr     *atomic.Pointer[StreamClosedError]
}

func newStream(
	lw *utils.LockedWriter,
	streamsMtx *utils.RWMutex[map[uint64]*Stream], req *Request,
) *Stream {
	stream := &Stream{
		lw:         lw,
		streamsMtx: streamsMtx,
		req:        req,
		// TODO: Chan len
		msgChan:   make(chan Message, 50),
		msgBuffer: list.New(),
		deadline:  utils.NewAValue[time.Time](time.Time{}),
		closeErr:  &atomic.Pointer[StreamClosedError]{},
	}
	return stream
}

// Request returns the request associated with the stream.
func (s *Stream) Request() *Request {
	return s.req
}

func (s *Stream) addMsg(msg Message) error {
	if s.IsClosed() {
		return s.GetClosedErr()
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
		return s.GetClosedErr()
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
	msg, ok, deadline := Message{}, false, s.deadline.Load()
	if deadline == (time.Time{}) {
		msg, ok = <-s.msgChan
	} else {
		until := time.Until(deadline)
		// Return if the deadline time has been reached
		if until <= 0 {
			return Message{}, ErrTimedOut
		}
		timer := time.NewTimer(until)
		select {
		case msg, ok = <-s.msgChan:
		case <-timer.C:
			return Message{}, ErrTimedOut
		}
	}
	if !ok {
		return Message{}, s.GetClosedErr()
	}
	s.moveMsg()
	return msg, nil
}

// RecvTimeout attempts to receive a message from the stream during the given
// duration. Ignores any timeout set by Stream.SetRecvTimeout.
func (s *Stream) RecvTimeout(dur time.Duration) (Message, error) {
	msg, ok := Message{}, false
RecvTimeoutLoop:
	for {
		// Check to see if theres already a value (timer not needed)
		select {
		case msg, ok = <-s.msgChan:
			if !ok {
				return Message{}, s.GetClosedErr()
			}
			break RecvTimeoutLoop
		default:
		}
		timer := time.NewTimer(dur)
		select {
		case msg, ok = <-s.msgChan:
			timer.Stop()
			if !ok {
				return Message{}, s.GetClosedErr()
			}
			break RecvTimeoutLoop
		case <-timer.C:
			return Message{}, ErrTimedOut
		}
	}
	s.moveMsg()
	return msg, nil
}

func (s *Stream) moveMsg() {
	s.msgBufferMtx.Lock()
	// Add another one to the channel if possible
	if s.msgBuffer.Len() != 0 {
		e := s.msgBuffer.Front()
		s.msgChan <- e.Value.(Message)
		s.msgBuffer.Remove(e)
	}
	s.msgBufferMtx.Unlock()
}

// SetRecvDeadline sets the time at which Recv calls timeout. This does not
// change the deadline for Recv calls that are currently in process. Passing
// time.Time{} removes the deadline. If the deadline has passed, all calls to
// Recv return ErrTimedOut.
func (s *Stream) SetRecvDeadline(t time.Time) {
	s.deadline.Store(t)
}

// IsClosed returns whether the stream is closed.
func (s *Stream) IsClosed() bool {
	return s.isClosed.Load()
}

// Close closes the stream.
func (s *Stream) Close() error {
	return s.closeWithMessage(true, Message{}, Message{})
}

// CloseWithMessage closes the stream with the message to be sent to the
// client.
func (s *Stream) CloseWithMessage(msg Message) error {
	return s.closeWithMessage(true, msg, Message{})
}

func (s *Stream) close(send bool) error {
	return s.closeWithMessage(send, Message{}, Message{})
}

// This is used for when a client sends a close message. There is no need for
// the `send` parameter since the client has already closed the stream.
func (s *Stream) closeWithMsgRecvd(msg Message) {
	s.closeWithMessage(false, Message{}, msg)
}

func (s *Stream) closeWithMessage(send bool, msg, msgRecvd Message) error {
	if s.isClosed.Swap(true) {
		return s.GetClosedErr()
	}
	close(s.msgChan)
	s.streamsMtx.Apply(func(mp *map[uint64]*Stream) {
		delete(*mp, s.req.id)
	})
	if !send {
		return nil
	}
	msg.flags = MsgFlagClose
	err := s.send(msg)
	s.closeErr.CompareAndSwap(nil, &StreamClosedError{
		Message:      msgRecvd,
		HasMessage:   !send,
		ClientClosed: !send,
	})
	return err
}

// GetClosedErr returns the StreamClosedError associated with the stream
// closure. Returns nil if the stream isn't closed.
func (s *Stream) GetClosedErr() *StreamClosedError {
	if !s.isClosed.Load() {
		return nil
	} else if sce := s.closeErr.Load(); sce != nil {
		return sce
	}
	return &StreamClosedError{}
}

// Message is a message sent/received to/from a stream.
type Message struct {
	reqId uint64
	flags byte
	// Body is the body of the stream.
	Body *bytes.Buffer
}

func NewMessage(body []byte) Message {
	return Message{Body: bytes.NewBuffer(body)}
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

// StreamClosedError represents an error due to the stream being closed.
type StreamClosedError struct {
	// Message is the message sent on close, if there was one. This is not
	// populated if the caller closed the stream (e.g., if a client gets this
	// message due to the client closing the stream, it won't be set).
	// NOTE: Make sure to take note that consuming the bytes from the body buffer
	// takes effect everywhere since the body buffer is a pointer. Anyone else
	// holding that specific instance of the error will also have their body
	// bytes consumed.
	Message Message
	// HasMessage is whether there was a message sent.
	HasMessage bool
	// ClientClosed is whether the client closed the stream or not. This could be
	// from a purposeful close or a close due to error (e.g., network).
	ClientClosed bool
}

// Error implements the error.Error interface method.
func (sce *StreamClosedError) Error() string {
	var l int
	if sce.Message.Body != nil {
		l = sce.Message.Body.Len()
	}
	// TODO: What/what not to show?
	return fmt.Sprintf("StreamClosedError (message len: %d)", l)
}

// IsStreamClosedError returns whether the error is a StreamClosedError.
func IsStreamClosedError(err error) bool {
	var sce *StreamClosedError
	return errors.As(err, &sce)
}

// GetStreamClosedError attempts to convert the error into a *StreamClosedError
// using errors.As, returning nil if the conversion fails.
func GetStreamClosedError(err error) *StreamClosedError {
	var sce *StreamClosedError
	errors.As(err, &sce)
	return sce
}
