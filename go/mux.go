package jtrpc

type Handler interface {
	// Takes a request and modifies the response.
	Handle(*Request, *Response)
}

type StreamHandler interface {
	HandleStream(*Stream)
}

type HandlerFunc func(*Request, *Response)

func (h HandlerFunc) Handle(req *Request, resp *Response) {
	h(req, resp)
}

type StreamHandlerFunc func(*Stream)

func (h StreamHandlerFunc) HandleStream(stream *Stream) {
	h(stream)
}

type Mux interface {
	Handle(string, Handler)
	HandleStream(string, StreamHandler)
	GetHandler(string) Handler
	GetStreamHandler(string) StreamHandler
}

type MapMux struct {
	handlers       map[string]Handler
	streamHandlers map[string]StreamHandler
}

func NewMapMux() MapMux {
	return MapMux{
		handlers:       make(map[string]Handler),
		streamHandlers: make(map[string]StreamHandler),
	}
}

func (mm MapMux) Handle(path string, h Handler) {
	mm.handlers[path] = h
}

func (mm MapMux) HandleStream(path string, h StreamHandler) {
	mm.streamHandlers[path] = h
}

func (mm MapMux) HandleFunc(path string, f func(*Request, *Response)) {
	mm.handlers[path] = HandlerFunc(f)
}

func (mm MapMux) HandleStreamFunc(path string, f func(*Stream)) {
	mm.streamHandlers[path] = StreamHandlerFunc(f)
}

func (mm MapMux) GetHandler(path string) Handler {
	return mm.handlers[path]
}

func (mm MapMux) GetStreamHandler(path string) StreamHandler {
	return mm.streamHandlers[path]
}
