package jtrpc

// Handler is a handler for request/responses.
type Handler interface {
	// Handle takes a request and modifies the response. The response must be
	// done before the function returns. These should not be used after the
	// function has returned.
	Handle(*Request, *Response)
}

// StreamHandler is a handler for streams.
type StreamHandler interface {
	// HandleStream handles the stream. The stream isn't closed until an error
	// occurs, the client closes the stream, or the server closes it (e.g., from
	// this function). This means the stream can be passed to somewhere else and
	// the function return and the stream still be active.
	HandleStream(*Stream)
}

// HandlerFunc is a function that implements the Handler interface.
type HandlerFunc func(*Request, *Response)

// Handle implements the Handler.Handle function.
func (h HandlerFunc) Handle(req *Request, resp *Response) {
	h(req, resp)
}

// StreamHandlerFunc is a function that implements the StreamHandler interface.
type StreamHandlerFunc func(*Stream)

// HandleStream implements the StreamHandler.HandleStream function.
func (h StreamHandlerFunc) HandleStream(stream *Stream) {
	h(stream)
}

// Middleware is middleware. The handler passed is the next handler in the call
// chain; it will not be called if the function doesn't call it.
type Middleware = func(next Handler) Handler

// Mux is the multiplexer used for handling request paths.
type Mux interface {
	// Handle is used to handle a regular request.
	Handle(string, Handler, ...Middleware)
	// HandleStream is used to handle streams. Middleware that takes regular
	// a request/response is passed since streams start as request/response. The
	// middleware here are used to modify the request/response for a stream path.
	// Only a response with an OK status allows the stream to be created. ANY
	// other status results in the stream not being created. This is checked
	// after ALL middleware has been run (has returned).
	HandleStream(string, StreamHandler, ...Middleware)
	// Middleware adds "global" middleware to the Mux (middleware called for all
	// requests, both regular and stream setup). This is called before any other
	// middlewares.
	Middleware(...Middleware)
	// GetHandler gets the handler for the given path.
	GetHandler(string) Handler
	// GetHandler gets the stream handler for the given path.
	GetStreamHandler(string) StreamHandler
	// GetStreamMiddleware gets the stream handler middleware for the given path.
	GetStreamMiddleware(string) Middleware
	// GetMiddleware returns the middleware mux's function.
	GetMiddleware() Middleware
}

// MapMux is the defualt multiplexer used by the server. If a path already
// exists, it will be silently replaced. Middleware passed to the Handle*
// functions is run in the order it was passed. Middleware added "globally" is
// run first, in a similar fashion.
type MapMux struct {
	middleware        Middleware
	handlers          map[string]Handler
	streamMiddlewares map[string]Middleware
	streamHandlers    map[string]StreamHandler
}

// NewMapMux creates a new MapMux.
func NewMapMux() MapMux {
	return MapMux{
		handlers:          make(map[string]Handler),
		streamMiddlewares: make(map[string]Middleware),
		streamHandlers:    make(map[string]StreamHandler),
	}
}

// Handle implements the Mux.Handle.
func (mm MapMux) Handle(path string, h Handler, ms ...Middleware) {
	// Wrap the handler/middlewares.
	if l := len(ms); l != 0 {
		for i := l - 1; i >= 0; i-- {
			h = ms[i](h)
		}
	}
	mm.handlers[path] = h
}

// HandleStream implements the Mux.HandleStream function.
func (mm MapMux) HandleStream(path string, h StreamHandler, ms ...Middleware) {
	// Wrap the handler/middlewares.
	if len(ms) != 0 {
		m := ms[0]
		for _, mid := range ms[1:] {
			om, mw := m, mid
			m = func(next Handler) Handler {
				return om(mw(next))
			}
		}
		mm.streamMiddlewares[path] = m
	}
	mm.streamHandlers[path] = h
}

// HandleFunc is an alias for Handle(path, HandlerFunc(f)) function.
func (mm MapMux) HandleFunc(
	path string, f func(*Request, *Response), ms ...Middleware,
) {
	mm.handlers[path] = HandlerFunc(f)
}

// HandleStreamFunc is an alias for HandleStream(path, StreamHandlerFunc(f)).
func (mm MapMux) HandleStreamFunc(
	path string, f func(*Stream), ms ...Middleware,
) {
	mm.streamHandlers[path] = StreamHandlerFunc(f)
}

// Middleware implements the Mux.Middleware function. This adds middleware each
// time it is called, never replacing the middleware.
func (mm MapMux) Middleware(ms ...Middleware) {
	if len(ms) == 0 {
		return
	}
	// TODO: Test
	if mm.middleware == nil {
		mm.middleware, ms = ms[0], ms[1:]
	}
	for _, mid := range ms[1:] {
		om, mw := mm.middleware, mid
		mm.middleware = func(next Handler) Handler {
			return om(mw(next))
		}
	}
}

// GetHandler implements the Mux.GetHandler function.
func (mm MapMux) GetHandler(path string) Handler {
	return mm.handlers[path]
}

// GetStreamHandler implements the Mux.GetStreamHandler function.
func (mm MapMux) GetStreamHandler(path string) StreamHandler {
	return mm.streamHandlers[path]
}

// GetStreamMiddleware implements the Mux.GetStreamMiddleware function.
func (mm MapMux) GetStreamMiddleware(path string) Middleware {
	return mm.streamMiddlewares[path]
}

// GetMiddleware implements the Mux.GetMiddleware function.
func (mm MapMux) GetMiddleware() Middleware {
	return mm.middleware
}
