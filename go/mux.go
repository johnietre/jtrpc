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
	// a request/response is passed since streams start as request/response.
	// See Mux.StreamMiddleware for more info on middleware behavior here.
	HandleStream(string, StreamHandler, ...Middleware)

	// GlobalMiddleware adds "global" middleware to the Mux (middleware called
	// for all requests, both regular and stream setup). This is called before
	// any other middlewares.
	GlobalMiddleware(...Middleware)
	// Middleware adds middleware that is run before all non-stream handlers and
	// handler-specific middleware.
	Middleware(...Middleware)
	// StreamMiddleware adds middleware that is run before a stream is created.
	// The stream middleware are used to modify the request/response for a stream
	// path. Only a response with an OK status allows the stream to be created.
	// ANY other status results in the stream not being created. This is checked
	// after ALL middleware has been run (has returned). This is called before
	// any handler-specific middleware.
	StreamMiddleware(...Middleware)

	// GetHandler gets the handler for the given path.
	GetHandler(string) Handler
	// GetMiddlewareFor gets the middleware for a given path.
	GetMiddlewareFor(string) Middleware

	// GetStreamHandler gets the stream handler for the given path.
	GetStreamHandler(string) StreamHandler
	// GetStreamMiddlewareFor gets the stream handler middleware for the given
	// path.
	GetStreamMiddlewareFor(string) Middleware

	// GetGlobalMiddleware returns the mux's "global" middleware.
	GetGlobalMiddleware() Middleware
	// GetMiddleware returns the middleware run before all non-stream handlers.
	GetMiddleware() Middleware
	// GetStreamMiddleware returns the middleware run before any stream is
	// created.
	GetStreamMiddleware() Middleware
}

// UnimplementedMux implements the Mux interface and may be useful for
// embedding.
type UnimplementedMux struct{}

func (UnimplementedMux) Handle(string, Handler, ...Middleware)             {}
func (UnimplementedMux) HandleStream(string, StreamHandler, ...Middleware) {}
func (UnimplementedMux) GlobalMiddleware(...Middleware)                    {}
func (UnimplementedMux) Middleware(...Middleware)                          {}
func (UnimplementedMux) StreamMiddleware(...Middleware)                    {}
func (UnimplementedMux) GetHandler(string) Handler                         { return nil }
func (UnimplementedMux) GetMiddlewareFor(string) Handler                   { return nil }
func (UnimplementedMux) GetStreamHandler(string) StreamHandler             { return nil }
func (UnimplementedMux) GetStreamMiddlewareFor(string) Middleware          { return nil }
func (UnimplementedMux) GetGlobalMiddleware() Middleware                   { return nil }
func (UnimplementedMux) GetMiddleware() Middleware                         { return nil }
func (UnimplementedMux) GetStreamMiddleware() Middleware                   { return nil }

// MapMux is the defualt multiplexer used by the server. If a path already
// exists, it will be silently replaced. Middleware passed to the Handle*
// functions is run in the order it was passed. Middleware added "globally" is
// run first, in a similar fashion.
type MapMux struct {
	globalMiddleware Middleware
	middleware       Middleware
	streamMiddleware Middleware

	handlers          map[string]Handler
	middlewares       map[string]Middleware
	streamMiddlewares map[string]Middleware
	streamHandlers    map[string]StreamHandler
}

// NewMapMux creates a new MapMux.
func NewMapMux() *MapMux {
	return &MapMux{
		handlers:          make(map[string]Handler),
		middlewares:       make(map[string]Middleware),
		streamMiddlewares: make(map[string]Middleware),
		streamHandlers:    make(map[string]StreamHandler),
	}
}

// Handle implements the Mux.Handle function.
func (mm *MapMux) Handle(path string, h Handler, ms ...Middleware) {
	if len(ms) != 0 {
		m, ms := ms[0], ms[1:]
		mm.middlewares[path] = CombineMiddleware(m, ms...)
	}
	mm.handlers[path] = h
}

// HandleStream implements the Mux.HandleStream function.
func (mm *MapMux) HandleStream(path string, h StreamHandler, ms ...Middleware) {
	if len(ms) != 0 {
		m, ms := ms[0], ms[1:]
		mm.streamMiddlewares[path] = CombineMiddleware(m, ms...)
	}
	mm.streamHandlers[path] = h
}

// HandleFunc is an alias for Handle(path, HandlerFunc(f), ms...).
func (mm *MapMux) HandleFunc(
	path string, f func(*Request, *Response), ms ...Middleware,
) {
	mm.Handle(path, HandlerFunc(f), ms...)
}

// HandleStreamFunc is an alias for
// HandleStream(path, StreamHandlerFunc(f), ms...).
func (mm *MapMux) HandleStreamFunc(
	path string, f func(*Stream), ms ...Middleware,
) {
	mm.HandleStream(path, StreamHandlerFunc(f), ms...)
}

// GlobalMiddleware implements the Mux.GlobalMiddleware function. This adds
// middleware each time it is called, never replacing the middleware.
func (mm *MapMux) GlobalMiddleware(ms ...Middleware) {
	if len(ms) == 0 {
		return
	}
	if mm.globalMiddleware == nil {
		mm.globalMiddleware, ms = ms[0], ms[1:]
	}
	mm.globalMiddleware = CombineMiddleware(mm.globalMiddleware, ms...)
}

// SetGlobalMiddleware sets the mux's global middleware.
func (mm *MapMux) SetGlobalMiddleware(m Middleware) {
	mm.globalMiddleware = m
}

// Middleware implements the Mux.Middleware function. This adds middleware each
// time it is called, never replacing the middleware.
func (mm *MapMux) Middleware(ms ...Middleware) {
	if len(ms) == 0 {
		return
	}
	if mm.middleware == nil {
		mm.middleware, ms = ms[0], ms[1:]
	}
	mm.middleware = CombineMiddleware(mm.middleware, ms...)
}

// SetMiddleware sets the mux's middleware.
func (mm *MapMux) SetMiddleware(m Middleware) {
	mm.middleware = m
}

// StreamMiddleware implements the Mux.StreamMiddleware function. This adds
// middleware each time it is called, never replacing the middleware.
func (mm *MapMux) StreamMiddleware(ms ...Middleware) {
	if len(ms) == 0 {
		return
	}
	if mm.streamMiddleware == nil {
		mm.streamMiddleware, ms = ms[0], ms[1:]
	}
	mm.streamMiddleware = CombineMiddleware(mm.streamMiddleware, ms...)
}

// SetStreamMiddleware sets the mux's middleware.
func (mm *MapMux) SetStreamMiddleware(m Middleware) {
	mm.streamMiddleware = m
}

// GetHandler implements the Mux.GetHandler function.
func (mm *MapMux) GetHandler(path string) Handler {
	return mm.handlers[path]
}

// GetMiddlewareFor implements the Mux.GetMiddlewareFor function.
func (mm *MapMux) GetMiddlewareFor(path string) Middleware {
	return mm.middlewares[path]
}

// GetStreamHandler implements the Mux.GetStreamHandler function.
func (mm *MapMux) GetStreamHandler(path string) StreamHandler {
	return mm.streamHandlers[path]
}

// GetStreamMiddlewareFor implements the Mux.GetStreamMiddlewareFor function.
func (mm *MapMux) GetStreamMiddlewareFor(path string) Middleware {
	return mm.streamMiddlewares[path]
}

// GetGlobalMiddleware implements the Mux.GetGlobalMiddleware function.
func (mm *MapMux) GetGlobalMiddleware() Middleware {
	return mm.globalMiddleware
}

// GetMiddleware implements the Mux.GetMiddleware function.
func (mm *MapMux) GetMiddleware() Middleware {
	return mm.middleware
}

// GetStreamMiddleware implements the Mux.GetStreamMiddleware function.
func (mm *MapMux) GetStreamMiddleware() Middleware {
	return mm.streamMiddleware
}

// CombineMiddleware combines multiple middlewares into one.
func CombineMiddleware(m Middleware, ms ...Middleware) Middleware {
	// TODO: Test
	if len(ms) == 0 {
		return m
	}
	for _, mid := range ms {
		om, mw := m, mid
		m = func(next Handler) Handler {
			return om(mw(next))
		}
	}
	return m
}
