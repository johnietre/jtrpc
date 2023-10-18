package main

import (
  "context"
)

type Service interface {
  Add(context.Context, *Input) (*Output, error)
  Sub(context.Context, *Input) (*Output, error)
  Mul(context.Context, *Input) (*Output, error)
  Div(context.Context, *Input) (*Output, error)
  Pow(context.Context, *Input) (*Output, error)
  StreamAdd(context.Context, *Stream[*Input, *Output]) error
}

const (
  DefaultMaxHeaderLen uint32 = 1 << 15
  DefaultMaxBodyLen uint64 = 1 << 20
)
