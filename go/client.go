package jtrpc

import (
	"io"
	"net"
	"sync/atomic"

	utils "github.com/johnietre/utils/go"
)

var (
	// The initial bytes with the version.
	initialBytesVer = []byte{
		0x00, 0x00, 0x00, 0x00, 0x00, 0x01, 0x05,
		'j', 't', 'r', 'p', 'c',
	}
)

// Client is a JtRPC client.
type Client struct {
	reqCounter atomic.Uint64
	reqs       *utils.Mutex[map[uint64]*Request]
	streams    *utils.Mutex[map[uint64]*Stream]
	isClosed   atomic.Bool
	closeErr   atomic.Value
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
		reqs:    utils.NewMutex(make(map[uint64]*Request)),
		streams: utils.NewMutex(make(map[uint64]*Stream)),
	}
	go c.run()
	return c, nil
}

func (c *Client) run() {
	// TODO
}
