package main

import (
	"fmt"
	"math"
	"net"
	"sync/atomic"
)

type Client struct {
	addr       string
	conn       net.Conn
	reqCounter atomic.Uint64
}

func (c *Client) nextReqId() uint64 {
	return c.reqCounter.Add(1)
}

func (c *Client) Add(in *Input) (*Output, error) {
	return &Output{Value: in.A + in.B}, nil
}

func (c *Client) Sub(in *Input) (*Output, error) {
	return &Output{Value: in.A - in.B}, nil
}

func (c *Client) Mul(in *Input) (*Output, error) {
	return &Output{Value: in.A * in.B}, nil
}

func (c *Client) Div(in *Input) (*Output, error) {
	if in.B == 0.0 {
		return nil, fmt.Errorf("divide by 0")
	}
	return &Output{Value: in.A / in.B}, nil
}

func (c *Client) Pow(in *Input) (*Output, error) {
	return &Output{Value: math.Pow(in.A, in.B)}, nil
}
