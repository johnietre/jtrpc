package main

import (
  "errors"
  "flag"
  "fmt"
  "log"
  "net"
  "net/http"
  "sync"

  "github.com/johnietre/jtrpc/go"
)

func main() {
  srvr := jtrpc.NewServer("")
  srvr.Mux = newMux()
  doneCh := make(chan struct{})
  go func() {
    if err := http.Serve(ln, srvr); err != nil {
      logger.Println("error running HTTP:", err)
    }
    close(doneCh)
  }()
  if err := srvr.Serve(ln); err != nil {
    logger.Println("error running JtRPC:", err)
  }
  _, _ = <-doneCh
}

type clientsSet utils.SyncSet[*jtrpc.Client]

func (cs *clientsSet) Get() (c *jtrpc.Client) {
  cs.Range(func(cl *jtrpc.Client) bool {
    c = cl
    return false
  })
  return
}

type clientsMap = map[string]*clientsSet

type Mux struct {
  jtrpc.MapMux
  clients *utils.RWMutex[clientsMap]
}

func newMux() *Mux {
}

func (m *Mux) GetHandler(path string) jtrpc.Handler {
  h := m.MapMux.GetHandler(path)
  if h == nil {
    h = jtrpc.HandlerFunc(m.jtrpcHandler)
  }
  return h
}

func (m *Mux) GetStreamHandler(path string) jtrpc.StreamHandler {
  h := m.MapMux.GetStreamHandler(path)
  if h == nil {
    h = jtrpc.StreamHandlerFunc(m.jtrpcStreamHandler)
  }
  return h
}

func (m *Mux) jtrpcHandler(req *jtrpc.Request, resp *jtrpc.Response) {
  first, rest := splitPath(req.Path)
  clients, ok := (*clientsSet)(nil), false
  m.clients.RApply(func(mp *clientsMap) {
    clients, ok = (*mp)[first]
  })
  if !ok {
    resp.StatusCode = jtrpc.StatusNotFound
    return
  }
  client := clients.Get()
  for first := true; first; first = false {
    if client == nil {
      client, err := jtrpc.Dial("")
      if err != nil {
        // TODO: Log
        resp.StatusCode = jtrpc.InternalServerError
        return
      }
      clients.Insert(client)
    }
    req.Path = rest
    respChan, err := client.Send(req)
    if err != nil {
      if client.IsClosed() {
        continue
      }
      // TODO: Log
      resp.StatusCode = jtrpc.InternalServerError
      return
    }
    break
  }
  pr, ok := respChan.Chan()
  if !ok {
    // TODO
    resp.StatusCode = jtrpc.InternalServerError
    return
  }
  resp.ShallowProxyFrom(pr)
}

func (m *Mux) jtrpcStreamHandler(stream *jtrpc.Stream) {
}

func splitPath(path string) (first, rest string) {
  if path == "" || path[0] != '/' {
    path = "/" + path
  }
  i := strings.IndexByte(path[1:], '/')
  if i == -1 {
    return path, ""
  }
  i++
  return path[:i], path[i:]
}

// TODO
var ErrClosed = fmt.Errorf("closed")

type HTTPListener struct {
  pl *ProxyListener
  ch chan net.Conn
}

type JtRPCListener struct {
  pl *ProxyListener
  ch chan net.Conn
}

func (jl *JtRPCListner) Accept() (net.Conn, error) {
  conn, ok := <-jl.ch
  if !ok {
    // TODO
    defer jl.pl.RUnlock()
    jl.pl.Lock()
    return nil, jl.pl.err
  }
  return conn, nil
}

func (jl *JtRPCListener) Close() error {
  jl.pl.jtrpcMtx.Lock()
  defer jl.pl.jtrpcMtx.Unlock()
  if !jl.pl.jtrpcClosed {
    jl.pl.jtrpcClosed = true
    close(jl.pl.jtrpcCh)
  }
  return nil
}

func (jl *JtRPCListener) Addr() net.Addr {
  return jl.pl.ln.Addr()
}

type ProxyListener struct {
  ln net.Listener

  httpCh, jtrpcCh chan net.Conn
  httpClosed, jtrpcClosed bool
  httpMtx, jtrpcMtx sync.RWMutex

  readTimeout time.Duration

  closed bool
  err error
  mtx sync.RWMutex
}

func (pl *ProxyListener) Run() error {
  for {
    conn, err := pl.ln.Accept()
    if err != nil {
      return err
    }
    go pl.handle(conn)
  }
}

func (pl *ProxyListener) handle(conn net.Conn) {
  if err := conn.SetReadDeadline(time.Now().Add(pl.readTimeout)); err != nil {
    // TODO
    return
  }
  var initial [1]byte
  if _, err := io.ReadFull(conn, initial[:]); err != nil {
    // TODO
    return
  }
  if err := conn.SetReadDeadline(time.Time{}); err != nil {
    // TODO
    return
  }
  conn = newConn(conn, initial)
  if initial[0] == 0 {
    pl.jtrpcMtx.RLock()
    defer pl.jtrpcMtx.RUnlock()
    if !pl.jtrpcClosed {
      pl.jtrpcCh <- conn
      return
    }
  } else {
    pl.httpCh <- conn
  }
  conn.Close()
}

func (pl *ProxyListener) Close() error {
  // TODO: Do better
  pl.mtx.Lock()
  defer pl.mtx.Unlock()
  if pl.closed {
    return pl.err
  }
  pl.closed = true
  pl.err = ErrClosed
  return ln.Close()
}

func (pl *ProxyListener) closeWithErr(err error) {
  if err == nil {
    err = ErrClosed
  }
  pl.mtx.Lock()
  defer pl.mtx.Unlock()
  if !pl.closed {
    pl.ln.Close()
    close(pl.httpCh)
    close(pl.jtrpcCh)
    pl.closed = true
    pl.err = err
  }
}

type Conn struct {
  net.Conn
  initial [1]byte
  readInitial atomic.Bool
}

func newConn(conn net.Conn, initial [1]byte) *Conn {
  return &Conn{Conn: conn, initial: initial}
}

func (c *Conn) Read(p []byte) (n int, err error) {
  if len(p) == 0 {
    // TODO: Is this correct behavior
    return 0, nil
  }
  if !c.readInitial.Swap(true) {
    n += len(c.initial)
    copy(p, c.initial[:])
    p = p[n:]
    if len(p) == 0 {
      return
    }
  }
  ni := 0
  ni, err = c.conn.Read(p)
  n += ni
  return
}

/*
T / HTTP/1\r\n\r\n
*/
