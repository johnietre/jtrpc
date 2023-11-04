package jtrpc

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

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

var (
	// ErrHeadersTooLarge means the headers are too large to encode.
	ErrHeadersTooLarge = fmt.Errorf("encoded headers exceed maximum length")
)

// GetFirstValue returns the first value in a list of values, or an empty
// string if there are no values. This is meant to be used with
// `FromHTTPHeader`.
func GetFirstValue(_, vs []string) string {
	if len(vs) == 0 {
		return ""
	}
	return vs[0]
}

// GetLastValue returns the last value in a list of values, or an empty string
// if there are no values. This is meant to be used with `FromHTTPHeader`.
func GetLastValue(_, vs []string) string {
	if l := len(vs); l != 0 {
		return vs[l-1]
	}
	return ""
}

// JoinValues joins the values with ",". This is meant to be used with
// `FromHTTPHeader`.
func JoinValues(_, vs []string) string {
	return strings.Join(vs, ",")
}

// Headers represents request headers that may or may not be parsed. A given
// key (header) can only be specified once. If multiple are specified in the
// raw bytes, expect that the first one will be retried when getting the header
// from the raw bytes and that the last one will be used when encoding all the
// headers. When writing the headers out, if they have been not parsed, the
// raw bytes are written, otherwise, the raw bytes are reconstructed; this does
// not "unparse" (encode) the bytes. When Encoding the headers from a parsed
// state, there is no guarantee of the order of the headers. If a specific
// order is desired, first, encoding the headers (set the state of the headers
// to "unparsed"), then add each header with the `Set` method as that will
// append each new header to the raw bytes.
type Headers struct {
	bytes  []byte
	m      map[string]string
	parsed bool
}

func newHeaders(bytes []byte) *Headers {
	return &Headers{bytes: bytes}
}

// NewHeaders creates a new Headers.
func NewHeaders(parsed bool) *Headers {
	var m map[string]string
	var b []byte
	if parsed {
		m = make(map[string]string)
	} else {
		b = []byte{}
	}
	return &Headers{
		bytes:  b,
		m:      m,
		parsed: parsed,
	}
}

// FromHTTPHeader takes an http.Header and returns a parsed Headers. Given that
// any header may only have one value, the header name (key) and list of values
// is pased to `f`, and the returned string is used as the value. If only the
// first or last value is desired, one can call `FromHTTPHeader` with
// `GetFirstValue` or `GetLastValue`, respectively. The best way to include all
// values is most likely to call with `JoinValues`, which will combine all the
// values using comma separators. If `parsed` is false, each key-value pair
// will be encoded. If the encoded length is longer than the max allowed
// length, ErrHeadersTooLarge is returned. No error is returned if `parsed` is
// true.
func FromHTTPHeader(
	hm http.Header, parsed bool, f func(string, []string) string,
) (*Headers, error) {
	if parsed {
		m := make(map[string]string, len(hm))
		for k, vs := range hm {
			m[k] = f(k, vs)
		}
		return &Headers{m: m, parsed: true}, nil
	}
	var b []byte
	for k, vs := range hm {
		v := f(k, vs)
		kl, vl := len(k), len(v)
		if 4+kl+vl+len(b) > MaxHeadersLen {
			return nil, ErrHeadersTooLarge
		}
		b = append2(b, uint16(kl))
		b = append2(b, uint16(vl))
		b = append(b, k...)
		b = append(b, v...)
	}
	return &Headers{bytes: b}, nil
}

// FromHTTPHeaderP, to be concise, is to FromHTTPHeader what Encode is to
// EncodeP. The returned http.Header includes all key-values pairs that weren't
// added to the map, if `parsed` is false.
func FromHTTPHeaderP(
	hm http.Header, parsed bool, f func(string, []string) string,
) (*Headers, http.Header) {
	if parsed {
		m := make(map[string]string, len(hm))
		for k, vs := range hm {
			m[k] = f(k, vs)
		}
		return &Headers{m: m, parsed: true}, nil
	}
	var b []byte
	for k, vs := range hm {
		v := f(k, vs)
		kl, vl := len(k), len(v)
		if 4+kl+vl+len(b) > MaxHeadersLen {
			continue
		}
		b = append2(b, uint16(kl))
		b = append2(b, uint16(vl))
		b = append(b, k...)
		b = append(b, v...)
		delete(hm, k)
	}
	return &Headers{bytes: b}, hm
}

// Clone clones the headers.
func (h *Headers) Clone() *Headers {
	if !h.parsed {
		return &Headers{bytes: utils.CloneSlice(h.bytes)}
	}
	m := make(map[string]string, len(h.m))
	for k, v := range h.m {
		m[k] = v
	}
	return &Headers{m: m, parsed: true}
}

// IsParsed returns whether the headers have been parsed from raw bytes into a
// map.
func (h *Headers) IsParsed() bool {
	return h.parsed
}

// Parse parses the headers from bytes into a map, setting the map in the
// headers, clearing the raw bytes, and returning the map. Calling this when
// the headers are already parsed (not encoded) just returns the cached map.
func (h *Headers) Parse() map[string]string {
	if h.parsed {
		return h.m
	}
	m := make(map[string]string)
	for l := len(h.bytes); l >= 4; {
		kl, vl := get2(h.bytes), get2(h.bytes[2:])
		h.bytes = h.bytes[4:]
		l -= 4
		kvl := int(kl + vl)
		if kvl > l {
			break
		}
		if kvl > len(h.bytes) {
			// TODO: Do something?
			break
		}
		m[string(h.bytes[:kl])] = string(h.bytes[kl:kvl])
		h.bytes = h.bytes[kvl:]
		l -= kvl
	}
	h.bytes, h.m, h.parsed = nil, m, true
	return h.m
}

// Encode encodes the headers into the raw bytes representation, setting the
// raw bytes in the headers, clearing the map, and returning the bytes.
// Calling this when the headers are already encoded (not parsed) just returns
// the cached bytes. If the headers are too large, ErrHeadersTooLarge is
// returned. When writing the headers out, if this error is returned, the
// headers are not written. If a lot of headers are added when handling a
// request, it may desirable to first call this to make sure that everything
// can be encoded. This will not cause any performance degradation since the
// resulting raw bytes are cached, this writing the bytes back to the client
// does not require encoding them again.
func (h *Headers) Encode() ([]byte, error) {
	if !h.parsed {
		return h.bytes, nil
	}
	var b []byte
	headersLen := 0
	for k, v := range h.m {
		kl, vl := len(k), len(v)
		if kl > MaxHeadersLen || vl > MaxHeadersLen {
			return nil, ErrHeadersTooLarge
		}
		kvl := kl + vl
		headersLen += kvl
		if headersLen > MaxHeadersLen {
			headersLen -= kvl
			return nil, ErrHeadersTooLarge
		}
		b = append2(b, uint16(kl))
		b = append2(b, uint16(vl))
		b = append(b, k...)
		b = append(b, v...)
	}
	h.bytes, h.m, h.parsed = b, nil, false
	return h.bytes, nil
}

// EncodeP encodes as many headers as possible, returning a map of the headers
// that were not encoded if adding them would have exceeded the maximum header
// length. If the heaeders are already encoded (not parsed), the raw bytes and
// a nil map is returned. If the length of the returned map is 0 and the map is
// not nil, it is guaranteed that all headers were encoded. Calling this when
// the headers have already been parsed does nothing. The returned map, when
// not nil, is the map that is stored internally when the headers have been
// parsed, with all the key-value pairs that were stored deleted from the map.
// This means that this also affects the map that is returned by Headers.Parse.
// Calling this from a handler before it returns may be useful if one wants to
// ensure at least some headers are written and wants to see which are left
// out. This will cause future calls to Header.Encode to return the raw bytes
// returned from this function.
func (h *Headers) EncodeP() ([]byte, map[string]string) {
	if !h.parsed {
		return h.bytes, nil
	}
	var b []byte
	headersLen := 0
	for k, v := range h.m {
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
		b = append2(b, uint16(kl))
		b = append2(b, uint16(vl))
		b = append(b, k...)
		b = append(b, v...)
		delete(h.m, k)
	}
	m := h.m
	h.bytes, h.m, h.parsed = b, nil, false
	return h.bytes, m
}

// EncodedLen returns the length of the raw bytes the headers would be encoded
// as. If the header has not been parsed, this just returns the length of the
// bytes currently stored in the headers. Otherwise, every time this is called
// when the headers have been parsed, this calculates the length. As per the
// spec, the method to get the length of an individual header is simply add
// four to the length of each key-value pair (4 + len(key) + len(value)).
func (h *Headers) EncodedLen() int {
	if !h.parsed {
		return len(h.bytes)
	}
	l := 0
	for k, v := range h.m {
		l += 4 + len(k) + len(v)
	}
	return l
}

// Get gets the value for the given key, or returns "". If the header is not
// parsed, it searches for the header in the unparsed bytes. Does not parse the
// headers. Retrieving a value does not cache the key; if the same key is
// passed and the header hasn't been parsed, the raw bytes are again searched
// for the key.
func (h *Headers) Get(key string) string {
	v, _ := h.GetChecked(key)
	return v
}

// GetChecked is the same as Headers.Get but returns false if the key doesn't
// exist.
func (h *Headers) GetChecked(key string) (string, bool) {
	if h.parsed {
		val, ok := h.m[key]
		return val, ok
	}
	b, wkl := h.bytes, len(key)
	for l := len(b); l > 0; {
		if l < 4 {
			break
		}
		kl, vl := int(get2(b)), int(get2(b[2:]))
		kvl := kl + vl
		b = b[4:]
		if kvl > len(b) {
			// TODO: Do something?
			break
		}
		l -= 4
		if kl == wkl {
			if string(b[:kl]) == key {
				return string(b[kl:kvl]), true
			}
		}
		b = b[kvl:]
		l -= kvl
	}
	return "", false
}

// Set adds the passed key, value pair to the headers. If the headers have been
// parsed, the pair is added to the map, otherwise, they are encoded appended
// and appended to the unparsed bytes. True is returned if the kv pair was
// added. This is the case if the headers have already been parsed or if the
// pair could be added without the encoded headers exceeding the maximum
// length. Therefore, false is returned iff the headers have not been parsed
// (are encoded) and adding the encoded header would cause the length of the
// raw bytes to exceed the maximum header length.
func (h *Headers) Set(key, value string) bool {
	if h.parsed {
		h.m[key] = value
		return true
	}
	kl, vl := len(key), len(value)
	if 4+kl+vl+len(h.bytes) > MaxHeadersLen {
		return false
	}
	buf := make([]byte, 4)
	utils.Place2(buf, uint16(kl))
	utils.Place2(buf[2:], uint16(vl))
	buf = append(buf, key...)
	buf = append(buf, value...)
	h.bytes = append(h.bytes, buf...)
	return true
}

// SetAll sets the header values to those in the passed map. This, in effect,
// clears the map then sets the headers. If the headers are parsed, this does
// not make a copy of the map but rather sets the internally stored map to `m`.
// In this case, `m` should not be used anymore (make a copy beforehand if the
// caller still wants to use `m`). False is returned iff the headers are
// encoded and the length of the encoded headers is too large.
func (h *Headers) SetAll(m map[string]string) bool {
	h.m = m
	if h.parsed {
		return true
	}
	h.parsed = true
	_, err := h.Encode()
	h.m, h.parsed = nil, false
	return err == nil
}

// SetAllP, to keep it concise, is to SetAll what Encode is to EncodeP. In any
// case, `m` should not be used as it will either be modified or "taken
// ownership of".
func (h *Headers) SetAllP(m map[string]string) map[string]string {
	h.m = m
	if h.parsed {
		return nil
	}
	h.parsed = true
	_, rest := h.EncodeP()
	return rest
}

// Remove removes the header with the given key, returning the value if it
// exists. If the header has not been parsed, this searches the raw bytes for
// the key and removes the header from the raw bytes, if found.
func (h *Headers) Remove(key string) string {
	v, _ := h.RemoveChecked(key)
	return v
}

// RemoveChecked is the same as Headers.Remove bur returns false if the key
// doesn't exist.
func (h *Headers) RemoveChecked(key string) (string, bool) {
	if h.parsed {
		val, ok := h.m[key]
		delete(h.m, key)
		return val, ok
	}
	b, wkl := h.bytes, len(key)
	for i, l := 0, len(b); l >= 4; {
		kl, vl := int(get2(b)), int(get2(b[2:]))
		kvl := kl + vl
		b = b[4:]
		if kvl > l {
			break
		}
		l -= 4
		if kl == wkl {
			if string(b[:kl]) == key {
				val := string(b[kl:kvl])
				h.bytes = append(h.bytes[:i], h.bytes[i+4+kvl:]...)
				return val, true
			}
		}
		b = b[kvl:]
		l -= kvl
		i += 4 + kvl
	}
	return "", false
}

// Range iterates through the headers, passing each key-value pair to `f`. If
// `f` returns false, iteration stops. If the headers are parsed, iteration is
// done through the internal header map, meaning iteration follows the rules of
// regular Go map iteration. Otherwise, iteration is done through the raw
// bytes, meaning there will be no variance in the order of key-value pairs.
func (h *Headers) Range(f func(string, string) bool) {
	if h.parsed {
		for k, v := range h.m {
			if !f(k, v) {
				break
			}
		}
		return
	}
	b := h.bytes
	for l := len(b); l >= 4; {
		kl, vl := int(get2(b)), int(get2(b[2:]))
		kvl := kl + vl
		b = b[4:]
		if kvl > l {
			break
		}
		l -= 4
		if !f(string(b[:kl]), string(b[kl:kvl])) {
			break
		}
		l -= kvl
	}
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
		ctx:        context.Background(),
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

// Clone creates a deep copy of the request with the given context.
func (r *Request) Clone(ctx context.Context) *Request {
	var headers *Headers
	if r.Headers != nil {
		headers = r.Headers.Clone()
	}
	var body *bytes.Buffer
	if r.Body != nil {
		body := bytes.NewBuffer(nil)
		body.Write(r.Body.Bytes())
	}
	return &Request{
		id:         0,
		ctx:        ctx,
		RemoteAddr: r.RemoteAddr,
		Flags:      r.Flags,
		Path:       r.Path,
		Headers:    headers,
		Body:       body,
	}
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

// SetContext sets the request's context to the passed context. This is useful
// (and possibly required) for things like middleware that sits before a stream
// handler as the stream handler gets passed the request passed to the
// middleware, therefore does not see the changes made by something like
// WithContext.
// NOTE: Any calls to this should come before any calls that clone/copy the
// request (like WithContext), if the request is meant to be modified.
func (r *Request) SetContext(ctx context.Context) *Request {
	r.ctx = ctx
	return r
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

// StatusToHTTP converts a status to its closest HTTP-equivalent status.
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
	Headers *Headers
	body    io.ReadCloser
	bodyLen uint64
	// Request is the request that is associated with this response. As of right
	// now, not set for Server request/responses.
	Request *Request
}

func newResponse(reqId uint64, flags byte, req *Request) *Response {
	return &Response{
		reqId:   reqId,
		flags:   flags,
		Headers: newHeaders(nil),
		Request: req,
	}
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

// WriteTo writes the response to the writer. This is called internally when
// writing a response back to a client. This calls Headers.Encode to get the
// encoded header bytes. If an error is returned by Header.Encode, no headers
// are written at all. A server handler can call Headers.EncodeP before the
// handler is returned to ensure at least some headers are written, more or
// less (see Headers.EncodeP for more information).
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
	headers, err := r.Headers.Encode()
	if err == nil {
		buf = append(buf, headers...)
	}
	headersLen := len(headers)
	place2(buf[10:], uint16(headersLen))
	place8(buf[12:], r.bodyLen)
	// Add lengths and write buf
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
