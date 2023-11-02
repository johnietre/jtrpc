package jtrpc

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"

	utils "github.com/johnietre/utils/go"
)

const (
	// FlagStreamMsg signifies a stream message.
	FlagStreamMsg byte = 0b1000_0000

	// ReqFlagStream signifies a request expects a stream.
	ReqFlagStream = 0b0100_0000
	// ReqFlagTimeout signifies a request has a timeout.
	ReqFlagTimeout = 0b0000_0010
	// ReqFlagCancel signifies the request is to cancel a prior request with the
	// same ID.
	ReqFlagCancel = 0b0000_0001
)

// Headers represents request headers that may or may not be parsed.
type Headers struct {
	bytes  []byte
	m      map[string]string
	parsed bool
}

func newHeaders(bytes []byte) *Headers {
	return &Headers{bytes: bytes}
}

// Parse parses the headers from bytes into a map.
func (h *Headers) Parse() {
	if h.parsed {
		return
	}
	h.parsed = true
	h.m = make(map[string]string)
	for l := len(h.bytes); l != 0; {
		if l < 4 {
			break
		}
		kl, vl := get2(h.bytes), get2(h.bytes[2:])
		h.bytes = h.bytes[4:]
		l -= 4
		kvl := int(kl + vl)
		if kvl < l {
			break
		}
		h.m[string(h.bytes[:kl])] = h.m[string(h.bytes[kl:kvl])]
		h.bytes = h.bytes[kvl:]
		l -= kvl
	}
	h.bytes = nil
}

// Get gets the value for the given key, or returns "". If the header is not
// parsed, it searches for the header in the unparsed bytes. Does not parse the
// headers.
func (h *Headers) Get(key string) string {
	if h.parsed {
		return h.m[key]
	}
	b, wl := h.bytes, len(key)
	for l := len(b); l != 0; {
		if l < 4 {
			break
		}
		kl, vl := int(get2(b)), int(get2(b[2:]))
		kvl := kl + vl
		b = b[4:]
		l -= 4
		if kl != wl {
			if k := string(b[:kl]); k == key {
				return string(b[kl:kvl])
			}
		}
		b = b[kvl:]
		l -= kvl
	}
	return ""
}

// GetChecked is the same as Headers.Get, but returns false if the key doesn't
// exist.
func (h *Headers) GetChecked(key string) (string, bool) {
	if h.parsed {
		val, ok := h.m[key]
		return val, ok
	}
	b, wl := h.bytes, len(key)
	for l := len(b); l != 0; {
		if l < 4 {
			break
		}
		kl, vl := int(get2(b)), int(get2(b[2:]))
		kvl := kl + vl
		b = b[4:]
		l -= 4
		if kl != wl {
			if k := string(b[:kl]); k == key {
				return string(b[kl:kvl]), true
			}
		}
		b = b[kvl:]
		l -= kvl
	}
	return "", false
}

// Map returns the map representation of the headers, parsing if necessary.
func (h *Headers) Map() map[string]string {
	h.Parse()
	return h.m
}

// Request is a request received.
type Request struct {
	id  uint64
	ctx context.Context
	// RemoteAddr is the remote address of the client.
	RemoteAddr string
	// Flags are the flags that were sent.
	Flags byte
	// Path is the path requested.
	Path string
	// Headers are the request headers.
	Headers *Headers
	// Body is the request body.
	Body *bytes.Buffer
}

func newRequest(
	id uint64, addr string, flags byte, path string, headerBytes []byte,
) *Request {
	return &Request{
		id:         id,
		RemoteAddr: addr,
		Flags:      flags,
		Path:       path,
		Headers:    newHeaders(headerBytes),
	}
}

var (
	ErrBodyTooLarge = fmt.Errorf("body too large")
)

// RequestFromReader reads a requests from the reader and returns it.
func RequestFromReader(r io.Reader, maxBodyLen int64) (*Request, error) {
	var buf [21]byte
	if _, err := io.ReadFull(r, buf[:]); err != nil {
		return nil, err
	}
	id := get8(buf[:])
	flags := buf[8]
	pathLen := int(get2(buf[9:]))
	headersLen := int(get2(buf[11:]))
	bodyLen := int64(get8(buf[13:]))
	if bodyLen > maxBodyLen {
		return nil, ErrBodyTooLarge
	}
	b := make([]byte, pathLen+headersLen)
	if _, err := io.ReadFull(r, b); err != nil {
		return nil, err
	}
	body := bytes.NewBuffer(nil)
	if _, err := io.CopyN(body, r, bodyLen); err != nil {
		return nil, err
	}
	return &Request{
		id:      id,
		ctx:     context.Background(),
		Flags:   flags,
		Path:    string(b[:pathLen]),
		Headers: newHeaders(utils.CloneSlice(b[pathLen:])),
		Body:    body,
	}, nil
}

func (r *Request) setContext(ctx context.Context) *Request {
	r.ctx = ctx
	return r
}

// Context returns the context associated with the request.
func (r *Request) Context() context.Context {
	return r.ctx
}

// WithContext returns a shallow-copied request with the given context. Panics
// if the passed context is nil.
func (r *Request) WithContext(ctx context.Context) *Request {
	r2 := new(Request)
	*r2 = *r
	r2.ctx = ctx
	return r2
}

func hasStreamFlag(flags byte) bool {
	return flags&ReqFlagStream != 0
}

func hasStreamMsgFlag(flags byte) bool {
	return flags&FlagStreamMsg != 0
}

func hasCloseFlag(flags byte) bool {
	return flags&MsgFlagClose != 0
}

const (
	/* Non-Errors */
	// StatusOK is an OK status code.
	StatusOK byte = 0

	/* Client Errors */

	// StatusNotFound is a NotFound status code.
	StatusNotFound = 128
	// StatusIsStream is an IsStream status code.
	StatusIsStream = 129
	// StatusNotStream is a NotStream status code.
	StatusNotStream = 130
	// StatusBadRequest is a BadRequest status code.
	StatusBadRequest = 131
	// StatusUnauthorized is an Unauthorized status code.
	StatusUnauthorized = 132
	// StatusBodyTooLarge is a BodyTooLarge status code.
	StatusBodyTooLarge = 133

	// StatusInvalidInitialBytes is an InvalidInitialBytes status code.
	StatusInvalidInitialBytes = 160
	// StatusBadVersion is a BadVersion status code.
	StatusBadVersion = 161

	/* Server Errors */

	// StatusInternalServerError is an InternalServerError status code.
	StatusInternalServerError = 192
)

func StatusToHTTP(status byte) int {
	switch status {
	case StatusOK:
		return http.StatusOK
	case StatusNotFound:
		return http.StatusNotFound
	case StatusIsStream, StatusNotStream:
		return http.StatusMethodNotAllowed
	case StatusUnauthorized:
		return http.StatusUnauthorized
	case StatusBodyTooLarge:
		return http.StatusRequestEntityTooLarge
	case StatusInvalidInitialBytes:
		// TODO
		return http.StatusBadRequest
	case StatusBadVersion:
		return http.StatusHTTPVersionNotSupported
	case StatusInternalServerError:
		return http.StatusInternalServerError
	}
	if status < 128 {
		return http.StatusOK
	} else if status < 192 {
		return http.StatusBadRequest
	}
	return http.StatusInternalServerError
}

// Response is a response to be sent. The body is read (and closed if
// necessary) after returning from the handler. Responses passed to handlers
// should not be held after returning from the function.
type Response struct {
	reqId uint64
	flags byte
	// StatusCode is the status code of the response.
	StatusCode byte
	// Headers is the headers to be sent back.
	Headers map[string]string
	body    io.ReadCloser
	bodyLen uint64
	// Request is the request that is associated with this response. As of right
	// now, not set for Server request/responses.
	Request *Request
}

// SetBodyString sets the body to the specified string.
func (r *Response) SetBodyString(s string) {
	r.SetBodyBytes([]byte(s))
}

// SetBodyBytes sets the body to the specified bytes.
func (r *Response) SetBodyBytes(b []byte) {
	br := bytes.NewReader(b)
	r.SetBodyReader(br, br.Size())
}

// SetBodyReader sets the body to the given reader.
// Takes a io.Reader and the number of bytes to read.
func (r *Response) SetBodyReader(ir io.Reader, l int64) {
	r.SetBodyReadCloser(wrapCloser(ir), l)
}

// SetBodyReadCloser sets the body to the given read closer.
// Takes a io.ReadCloser and the number of bytes to read.
func (r *Response) SetBodyReadCloser(rc io.ReadCloser, l int64) {
	r.body, r.bodyLen = rc, uint64(l)
}

// WriteTo writes the response to the writer.
func (r *Response) WriteTo(w io.Writer) (n int64, err error) {
	defer func() {
		if r.body != nil {
			r.body.Close()
		}
	}()
	buf := make([]byte, 20)
	place8(buf, r.reqId)
	buf[8], buf[9] = r.flags, r.StatusCode
	// Marshal headers
	headersLen := 0
	for k, v := range r.Headers {
		kl, vl := len(k), len(v)
		if kl > MaxHeadersLen || vl > MaxHeadersLen {
			continue
		}
		kvl := kl + vl
		headersLen += kvl
		if headersLen > MaxHeadersLen {
			headersLen -= kvl
			continue
		}
		buf = append2(buf, uint16(kl))
		buf = append2(buf, uint16(vl))
		buf = append(buf, k...)
		buf = append(buf, v...)
	}
	// Add lengths and write buf
	place2(buf[10:], uint16(headersLen))
	place8(buf[12:], r.bodyLen)
	if n, err = utils.WriteAll(w, buf); err != nil {
		return
	}
	// Write body
	var nw int64
	if r.body != nil {
		nw, err = io.CopyN(w, r.body, int64(r.bodyLen))
		// TODO: Subtract from r.bodyLen?
		n += nw
	}
	return
}

// TODO: Flags
func writeResp(
	lw *utils.LockedWriter, reqId uint64, statusCode byte,
) (n int, err error) {
	buf := [20]byte{}
	place8(buf[:], reqId)
	buf[9] = statusCode
	nn, err := lw.WriteAll(buf[:])
	return int(nn), err
}

func writeRespW(
	w io.Writer, reqId uint64, statusCode byte,
) (n int, err error) {
	buf := [20]byte{}
	place8(buf[:], reqId)
	buf[9] = statusCode
	nn, err := utils.WriteAll(w, buf[:])
	return int(nn), err
}

// TODO: Flags
func writeRespMsg(
	lw *utils.LockedWriter, reqId uint64, statusCode byte, msg string,
) (n int, err error) {
	resp := &Response{reqId: reqId, StatusCode: statusCode}
	resp.SetBodyString(msg)
	nn, err := resp.WriteTo(lw.LockWriter())
	lw.Unlock()
	return int(nn), err
}

type closerWrapper struct {
	io.Reader
}

func (closerWrapper) Close() error {
	return nil
}

func wrapCloser(r io.Reader) io.ReadCloser {
	return closerWrapper{Reader: r}
}
