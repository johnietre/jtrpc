package main

import (
	"bytes"
	"encoding/json"
	"io"
)

type Msg interface {
	Ser([]byte) []byte
	SerTo(io.Writer) error
	Deser([]byte) error
	SerLen() int
}

type Input struct {
	A float64
	B float64
}

func (in *Input) Ser(b []byte) []byte {
	buf := bytes.NewBuffer(b)
	json.NewEncoder(buf).Encode(in)
	return buf.Bytes()
}

func (in *Input) Deser(b []byte) error {
	return json.Unmarshal(b, in)
}

func (in *Input) SerTo(w io.Writer) error {
	return json.NewEncoder(w).Encode(in)
}

func (in *Input) SerLen() int {
	lw := &LengthWriter{}
	json.NewEncoder(lw).Encode(in)
	return int(lw.NumWritten())
}

type Output struct {
	Value float64
}

func (out *Output) Ser(b []byte) []byte {
	buf := bytes.NewBuffer(b)
	json.NewEncoder(buf).Encode(out)
	return buf.Bytes()
}

func (out *Output) Deser(b []byte) error {
	return json.Unmarshal(b, out)
}

func (out *Output) SerTo(w io.Writer) error {
	return json.NewEncoder(w).Encode(out)
}

func (out *Output) SerLen() int {
	lw := &LengthWriter{}
	json.NewEncoder(lw).Encode(out)
	return int(lw.NumWritten())
}
