package jtrpc

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"sync/atomic"

	utils "github.com/johnietre/utils/go"
)

var (
	ErrClientClosed = fmt.Errorf("client closed")
)

var (
	// The initial bytes with the version.
	initialBytesVer = []byte{
		0x00, 0x00, 0x00, 0x00, 0x00, 0x01, 0x05,
		'j', 't', 'r', 'p', 'c',
		0x00, 0x01,
	}
)

// Client is a JtRPC client.
type Client struct {
	lw         *utils.LockedWriter
	reqCounter atomic.Uint64
	reqs       *utils.RWMutex[map[uint64]RespChan]
	streams    *utils.RWMutex[map[uint64]*Stream]
	isClosed   atomic.Bool
	closeErr   *utils.AValue[utils.ErrorValue]
}

// Dial attempts to connect to a server with the given address using TCP.
func Dial(addr string) (*Client, error) {
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		return nil, err
	}
	return WithRW(conn, conn)
}

// WithRW attempts to set up a connection using the given reader and writer.
func WithRW(r io.Reader, w io.Writer) (*Client, error) {
	if _, err := utils.WriteAll(w, initialBytesVer); err != nil {
		return nil, err
	}
	resp := &Response{}
	if _, err := resp.ReadFrom(r); err != nil {
		return nil, err
	}
	if resp.StatusCode != StatusOK {
		return nil, &ErrorResp{Resp: resp}
	}
	c := &Client{
		lw:       utils.NewLockedWriter(w),
		reqs:     utils.NewRWMutex(make(map[uint64]RespChan)),
		streams:  utils.NewRWMutex(make(map[uint64]*Stream)),
		closeErr: utils.NewAValue(utils.ErrorValue{}),
	}
	go c.run(r)
	return c, nil
}

func (c *Client) run(reader io.Reader) {
	var err error
	for err == nil {
		buf := make([]byte, 20)
		if _, err = io.ReadFull(reader, buf[:9]); err != nil {
			break
		}
		id, flags := get8(buf), buf[8]
		if hasStreamMsgFlag(flags) {
			var stream *Stream
			c.streams.RApply(func(mp *map[uint64]*Stream) {
				stream = (*mp)[id]
			})
			if stream == nil {
				continue
			}
			err = c.handleStreamMsg(reader, stream, buf)
			continue
		}
		rc, ok := RespChan{}, false
		c.reqs.Apply(func(mp *map[uint64]RespChan) {
			rc, ok = (*mp)[id]
		})
		if !ok {
			continue
		}
		err = c.handleResp(reader, rc, buf)
	}
	c.closeWithErr(err)
}

func (c *Client) handleResp(reader io.Reader, rc RespChan, buf []byte) error {
	resp := newResponse(rc.req.id, buf[8], rc.req)
	if _, err := io.ReadFull(reader, buf[:11]); err != nil {
		return err
	}
	resp.StatusCode = buf[0]

	hl, bl := get2(buf[1:]), get8(buf[3:])
	var headerBytes []byte
	if hl != 0 {
		headerBytes = make([]byte, hl)
		if _, err := io.ReadFull(reader, headerBytes); err != nil {
			return err
		}
	}
	resp.Headers = newHeaders(headerBytes)
	if bl != 0 {
		bodyBuf, bodyLen := bytes.NewBuffer(nil), int64(bl)
		if _, err := io.CopyN(bodyBuf, reader, bodyLen); err != nil {
			return err
		}
		resp.SetBodyReader(bodyBuf, bodyLen)
	}
	if resp.StatusCode == StatusOK && hasStreamFlag(resp.flags) {
		resp.Stream = newStream(c.lw, c.streams, rc.req)
		c.streams.Apply(func(mp *map[uint64]*Stream) {
			(*mp)[resp.Request.id] = resp.Stream
		})
	}
	rc.send(resp)
	return nil
}

func (c *Client) handleStreamMsg(
	reader io.Reader, stream *Stream, buf []byte,
) error {
	msg := Message{reqId: stream.req.id, flags: buf[8]}
	if _, err := io.ReadFull(reader, buf[:8]); err != nil {
		return err
	}
	bl := get8(buf)
	if bl != 0 {
		msg.Body = bytes.NewBuffer(nil)
		if _, err := io.CopyN(msg.Body, reader, int64(bl)); err != nil {
			return err
		}
	}
	if hasMsgFlagClose(msg.flags) {
		stream.closeWithMsgRecvd(msg)
	} else {
		stream.addMsg(msg)
	}
	return nil
}

// NumRequestsActive returns the current number of requests that are currently
// active (meaning they have been sent but don't have responses).
func (c *Client) NumRequestsActive() int {
	defer c.reqs.RUnlock()
	return len(*c.reqs.RLock())
}

// NumStreamsActive returns the current number of streams that are active
// (connected to the server).
func (c *Client) NumStreamsActive() int {
	defer c.streams.RUnlock()
	return len(*c.streams.RLock())
}

// Send attempts to send a request to the server. Requests should not be reused
// (unless they are cloned).
func (c *Client) Send(req *Request) (rc RespChan, err error) {
	if c.IsClosed() {
		err = c.GetClosedErr()
		return
	}
	for {
		// TODO: Check duplicate ID
		req.id = c.reqCounter.Add(1)
		break
	}
	writer := c.lw.LockWriter()
	req.WriteTo(writer)
	c.lw.Unlock()
	if err != nil {
		err = c.closeWithErr(err)
		return
	}
	rc = newRespChan(req)
	c.reqs.Apply(func(mp *map[uint64]RespChan) {
		(*mp)[req.id] = rc
	})
	return
}

// Close closes the client, returhing the error from GetClosedErr if the client
// was already closed.
func (c *Client) Close() error {
	return c.closeWithErr(nil)
}

func (c *Client) closeWithErr(err error) error {
	if c.isClosed.Swap(true) {
		return c.GetClosedErr()
	}
	c.closeErr.Store(utils.ErrorValue{Error: ClientClosedError{Err: err}})
	c.lw.Lock()
	defer c.lw.Unlock()
	c.streams.Apply(func(mp *map[uint64]*Stream) {
		for _, stream := range *mp {
			stream.Close()
		}
		// TODO: Use nil map?
		*mp = make(map[uint64]*Stream)
	})
	c.reqs.Apply(func(mp *map[uint64]RespChan) {
		for _, rc := range *mp {
			rc.Close()
		}
		// TODO: Use nil map?
		*mp = make(map[uint64]RespChan)
	})
	return nil
}

// IsClosed returns whether the client is closed or not.
func (c *Client) IsClosed() bool {
	return c.isClosed.Load()
}

// GetClosedErr gets the error (reason) associated with the streams closure.
func (c *Client) GetClosedErr() error {
	return c.closeErr.Load().Error
}

// RespChan is used to return a response to a client or cancel the request.
type RespChan struct {
	req    *Request
	ch     chan *Response
	closed *utils.Mutex[bool]
}

func newRespChan(req *Request) RespChan {
	return RespChan{
		req:    req,
		ch:     make(chan *Response, 1),
		closed: utils.NewMutex(false),
	}
}

func (r *RespChan) send(resp *Response) {
	defer r.closed.Unlock()
	if *r.closed.Lock() {
		return
	}
	select {
	case r.ch <- resp:
	default:
	}
}

// Chan returns the chan that the response is sent on. Reads on this chan
// should check for if the chan is closed.
func (r RespChan) Chan() <-chan *Response {
	return r.ch
}

// Close closes the RespChan, canceling the request if it's still in process.
// TODO: Cancel request.
func (r RespChan) Close() bool {
	alreadyClosed := false
	r.closed.Apply(func(bp *bool) {
		if *bp {
			alreadyClosed = true
		} else {
			close(r.ch)
		}
	})
	return !alreadyClosed
}

// IsClosed returns whether the RespChan is closed.
func (r RespChan) IsClosed() bool {
	defer r.closed.Unlock()
	return *r.closed.Lock()
}

// ClientClosedError is the error returned when a client is closed. It also
// hold any other error (reason) associated with the client's closure.
type ClientClosedError struct {
	// Err is the underlying error (reason) for the client's closure. This will
	// be nil if the client's Close method was what closed the client.
	Err error
}

// Error implements the error.Error interface method.
func (cce ClientClosedError) Error() string {
	if cce.Err == nil {
		return "client closed"
	}
	return fmt.Sprintf("client closed (reason: %v)", cce.Err)
}

// Unwrap implements the error.Unwrap interface method.
func (cce ClientClosedError) Unwrap() error {
	return cce.Err
}
