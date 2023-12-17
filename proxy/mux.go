package main

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync/atomic"
	"time"

	"github.com/johnietre/jtrpc/go"
	"github.com/johnietre/utils/go"
)

type clientsSet struct {
	*utils.SyncSet[*jtrpc.Client]
}

func newClientsSet() *clientsSet {
	return &clientsSet{SyncSet: utils.NewSyncSet[*jtrpc.Client]()}
}

func (cs *clientsSet) Get() (c *jtrpc.Client) {
	cs.Range(func(cl *jtrpc.Client) bool {
		c = cl
		return false
	})
	return
}

type serverMap = map[string]*Server

type Mux struct {
	jtrpc.MapMux
	servers *utils.RWMutex[serverMap]
}

func newMux(servers serverMap) *Mux {
	if servers == nil {
		servers = make(serverMap)
	}
	mux := &Mux{
		servers: utils.NewRWMutex(servers),
	}
	mux.GlobalMiddleware(mux.middleware)
	mux.StreamMiddleware(mux.streamMiddleware)
	return mux
}

func (m *Mux) GetHandler(path string) jtrpc.Handler {
	if first, _ := splitPath(path); first == "/admin" {
		return jtrpc.HandlerFunc(m.adminHandler)
	}
	h := m.MapMux.GetHandler(path)
	if h == nil {
		h = jtrpc.HandlerFunc(m.jtrpcHandler)
	}
	return h
}

func (m *Mux) GetStreamHandler(path string) jtrpc.StreamHandler {
	if first, _ := splitPath(path); first == "/admin" {
		//return jtrpc.StreamHandlerFunc(m.adminStreamHandler)
		return nil
	}
	h := m.MapMux.GetStreamHandler(path)
	if h == nil {
		h = jtrpc.StreamHandlerFunc(m.jtrpcStreamHandler)
	}
	return h
}

type ctxKeyType string

const ctxKey ctxKeyType = "ctxkey"

type MiddlewareInfo struct {
	server      *Server
	first, rest string
	stream      *jtrpc.Stream
	timer       *time.Timer
}

func (mi MiddlewareInfo) LogString() string {
	srvrLogStr := "server: N/A"
	if mi.server != nil {
		srvrLogStr = mi.server.LogString()
	}
	return fmt.Sprintf(
		"path: [%s]%s | stream: %v | server: %s",
		mi.first, mi.rest, mi.stream != nil, srvrLogStr,
	)
}

func (m *Mux) middleware(next jtrpc.Handler) jtrpc.Handler {
	return jtrpc.HandlerFunc(func(req *jtrpc.Request, resp *jtrpc.Response) {
		defer func(path string) {
			req.Path = path
		}(req.Path)
		first, rest := splitPath(req.Path)
		if first == "/admin" {
			req.SetContext(context.WithValue(
				req.Context(),
				ctxKey,
				MiddlewareInfo{first: first, rest: rest},
			))
			next.Handle(req, resp)
			return
		}
		server, ok := (*Server)(nil), false
		m.servers.RApply(func(mp *serverMap) {
			server, ok = (*mp)[first]
		})
		if !ok {
			resp.StatusCode = jtrpc.StatusNotFound
			return
		}
		ctx := context.WithValue(req.Context(), ctxKey, MiddlewareInfo{
			server: server,
			first:  first,
			rest:   rest,
		})
		req.SetContext(ctx)
		next.Handle(req, resp)
	})
}

const streamTimeout = time.Second * 10

func (m *Mux) streamMiddleware(next jtrpc.Handler) jtrpc.Handler {
	return jtrpc.HandlerFunc(func(req *jtrpc.Request, resp *jtrpc.Response) {
		info, ok := req.Context().Value(ctxKey).(MiddlewareInfo)
		if !ok {
			if first, _ := splitPath(req.Path); first == "/admin" {
				next.Handle(req, resp)
				return
			}
			logger.Print("expected info from middleware")
			resp.StatusCode = jtrpc.StatusInternalServerError
			return
		} else if info.server == nil {
			logger.Printf("nil server from info (%s)", info.LogString())
			resp.StatusCode = jtrpc.StatusInternalServerError
			return
		} else if info.server.clients == nil {
			logger.Printf("nil clients from info (%s)", info.LogString())
			resp.StatusCode = jtrpc.StatusInternalServerError
			return
		}
		/*
		   defer func(path string) {
		     req.Path = path
		   }(req.Path)
		*/
		// TODO: Clone?
		preq := req.Clone(req.Context())
		preq.Path = info.rest
		preq.SetStream(true)

		respChan, err := sendReq(preq, info.server)
		if err != nil {
			logger.Printf("error sending request: %v", err)
			resp.StatusCode = jtrpc.StatusInternalServerError
			return
		}
		pr, ok := <-respChan.Chan()
		if !ok {
			// TODO
			resp.StatusCode = jtrpc.StatusInternalServerError
			return
		}
		if pr.Stream == nil {
			// TODO
			logger.Println("expected stream")
			resp.StatusCode = jtrpc.StatusInternalServerError
			return
		}
		info.stream = pr.Stream
		info.timer = time.AfterFunc(streamTimeout, func() {
			info.stream.Close()
		})
		req.SetContext(context.WithValue(req.Context(), ctxKey, info))
		next.Handle(req, resp)
		resp.ShallowCopyFrom(pr)
	})
}

func (m *Mux) adminHandler(req *jtrpc.Request, resp *jtrpc.Response) {
	pwd, ok := req.Headers.GetChecked("password")
	if !ok || pwd != config.Password {
		resp.StatusCode = jtrpc.StatusUnauthorized
		resp.SetBodyString("invalid credentials")
		return
	}
	info, ok := req.Context().Value(ctxKey).(MiddlewareInfo)
	if !ok {
		logger.Print("expected info from middleware")
		resp.StatusCode = jtrpc.StatusInternalServerError
		return
	}
	rest := info.rest
	switch rest {
	case "/servers":
		m.getServersHandler(req, resp)
	case "/servers/new":
		m.newServersHandler(req, resp)
	case "/servers/del":
		m.delServersHandler(req, resp)
	case "/servers/replace":
		m.replaceServersHandler(req, resp)
	}
}

func (m *Mux) getServersHandler(req *jtrpc.Request, resp *jtrpc.Response) {
	var servers map[string]*Server
	m.servers.RApply(func(mp *map[string]*Server) {
		for path, srvr := range *mp {
			if !srvr.Hidden {
				servers[path] = srvr
			}
		}
	})
	if bytes, err := json.Marshal(servers); err == nil {
		resp.SetBodyBytes(bytes)
	} else {
		logger.Printf("error marshaling servers: %v", err)
		resp.StatusCode = jtrpc.StatusInternalServerError
	}
	return
	/*
	  if bytes, err := json.Marshal(*m.servers.RLock()); err == nil {
	    resp.SetBodyBytes(bytes)
	  } else {
	    logger.Printf("error marshaling servers: %v", err)
	    resp.StatusCode = jtrpc.StatusInternalServerError
	  }
	  m.servers.RUnlock()
	*/
}

func (m *Mux) newServersHandler(req *jtrpc.Request, resp *jtrpc.Response) {
	var servers map[string]*Server
	if err := json.NewDecoder(req.Body).Decode(&servers); err != nil {
		resp.SetStatusBodyString(jtrpc.StatusBadRequest, err.Error())
		return
	}
	errMap := make(map[string]string)
	m.servers.Apply(func(mp *map[string]*Server) {
		m := *mp
		for path, srvr := range servers {
			if first, _ := splitPath(path); first == "/admin" {
				errMap[path] = "First path segment cannot be /admin"
			} else if _, ok := m[path]; ok {
				errMap[path] = "Already exists"
			} else {
				srvr.init()
				m[path] = srvr
			}
		}
	})
	setAdminSrvrsErr(resp, errMap, len(servers))
}

func (m *Mux) delServersHandler(req *jtrpc.Request, resp *jtrpc.Response) {
	var paths []string
	if err := json.NewDecoder(req.Body).Decode(&paths); err != nil {
		resp.SetStatusBodyString(jtrpc.StatusBadRequest, err.Error())
		return
	}
	errMap := make(map[string]string)
	m.servers.Apply(func(mp *map[string]*Server) {
		m := *mp
		for _, path := range paths {
			if _, ok := m[path]; ok {
				delete(m, path)
			} else {
				errMap[path] = "Path doesn't exist"
			}
		}
	})
	setAdminSrvrsErr(resp, errMap, len(paths))
}

func (m *Mux) replaceServersHandler(req *jtrpc.Request, resp *jtrpc.Response) {
	var servers map[string]*Server
	if err := json.NewDecoder(req.Body).Decode(&servers); err != nil {
		resp.SetStatusBodyString(jtrpc.StatusBadRequest, err.Error())
		return
	}
	errMap := make(map[string]string)
	for path, srvr := range servers {
		if first, _ := splitPath(path); first == "/admin" {
			errMap[path] = "First path segment cannot be /admin"
			continue
		}
		srvr.init()
	}
	m.servers.Apply(func(mp *map[string]*Server) {
		*mp = servers
	})
	setAdminSrvrsErr(resp, errMap, len(servers))
}

func setAdminSrvrsErr(
	resp *jtrpc.Response, errMap map[string]string, otherLen int,
) {
	if le := len(errMap); le != 0 {
		if otherLen != le {
			if bytes, err := json.Marshal(errMap); err == nil {
				resp.SetStatusBodyBytes(jtrpc.StatusPartialError, bytes)
			} else {
				logger.Printf("error marshaling error map: %v", err)
				resp.SetStatusBodyString(
					jtrpc.StatusPartialError,
					"partial error with request; error sending response",
				)
			}
		} else {
			if bytes, err := json.Marshal(errMap); err == nil {
				resp.SetStatusBodyBytes(jtrpc.StatusBadRequest, bytes)
			} else {
				logger.Printf("error marshaling error map: %v", err)
				resp.SetStatusBodyString(
					jtrpc.StatusBadRequest,
					"error with request; error sending response",
				)
			}
		}
	}
}

func (m *Mux) jtrpcHandler(req *jtrpc.Request, resp *jtrpc.Response) {
	info, ok := req.Context().Value(ctxKey).(MiddlewareInfo)
	if !ok {
		logger.Print("expected info from middleware")
		resp.StatusCode = jtrpc.StatusInternalServerError
		return
	} else if info.server == nil {
		logger.Printf("nil server from info (%s)", info.LogString())
		resp.StatusCode = jtrpc.StatusInternalServerError
		return
	} else if info.server.clients == nil {
		logger.Printf("nil clients from info (%s)", info.LogString())
		resp.StatusCode = jtrpc.StatusInternalServerError
		return
	}
	req.Path = info.rest
	respChan, err := sendReq(req, info.server)
	if err != nil {
		logger.Printf("error sending request: %v", err)
		resp.StatusCode = jtrpc.StatusInternalServerError
		return
	}
	pr, ok := <-respChan.Chan()
	if !ok {
		// TODO
		resp.StatusCode = jtrpc.StatusInternalServerError
		return
	}
	resp.ShallowCopyFrom(pr)
}

func (m *Mux) jtrpcStreamHandler(cstream *jtrpc.Stream) {
	info, ok := cstream.Request().Context().Value(ctxKey).(MiddlewareInfo)
	if !ok {
		logger.Print("expected info from middleware")
		cstream.CloseWithMessage(jtrpc.NewMessage([]byte("internal server error")))
		return
	}
	if !info.timer.Stop() {
		<-info.timer.C
		cstream.CloseWithMessage(jtrpc.NewMessage([]byte("internal server error")))
		return
	}
	sstream := info.stream
	if sstream == nil {
		cstream.CloseWithMessage(jtrpc.NewMessage([]byte("internal server error")))
		logger.Printf("expected stream from info (%s)", info.LogString())
		return
	}
	go pipeStreams(sstream, cstream)
	go pipeStreams(cstream, sstream)
}

func pipeStreams(from, to *jtrpc.Stream) {
	for {
		msg, err := from.Recv()
		if err != nil {
			sce := jtrpc.GetStreamClosedError(err)
			if sce == nil {
				continue
			}
			if sce.HasMessage {
				to.CloseWithMessage(sce.Message)
			} else {
				to.Close()
			}
			break
		}
		if err := to.Send(msg); err != nil {
			// TODO: Log?
			to.CloseWithMessage(jtrpc.NewMessage([]byte("send error")))
			from.CloseWithMessage(jtrpc.NewMessage([]byte("send error")))
			break
		}
	}
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

const maxReqAttempts = 5

var errMaxAttempts = errors.New("max attempts reached")

func sendReq(req *jtrpc.Request, srvr *Server) (respChan jtrpc.RespChan, err error) {
	clients := srvr.clients
	for i := 0; i < maxReqAttempts; i++ {
		client := clients.Get()
		if client == nil {
			if client, err = srvr.newClient(); err != nil {
				return
			}
			clients.Insert(client)
		}
		respChan, err = client.Send(req)
		if err != nil {
			if client.IsClosed() {
				client = nil
				continue
			}
			return
		}
		return
	}
	err = errMaxAttempts
	return
}

type Server struct {
	Name   string `json:"name"`
	Path   string `json:"path,omitempty"`
	Addr   string `json:"addr"`
	Hidden bool   `json:"hidden,omitempty"`

	clients *clientsSet

	tunnelConn *utils.AValue[TConn]
	tch        chan net.Conn
	nWaiting   atomic.Int32
}

func (s *Server) init() {
	if s.clients == nil {
		s.clients = newClientsSet()
	}
	if s.tunnelConn == nil {
		s.tunnelConn = utils.NewAValue(TConn{})
	}
	if s.Addr == "tunnel" {
		// TODO?: Size
		s.tch = make(chan net.Conn, 15)
	}
}

func (srvr *Server) newClient() (client *jtrpc.Client, err error) {
	const tunnelTimeout = time.Second * 10
	if srvr.Addr != "tunnel" {
		return jtrpc.Dial(srvr.Addr)
	}

	tconn, ok := srvr.tunnelConn.LoadSafe()
	if !ok {
		return nil, fmt.Errorf("no tunnel conn")
	}
	tunnelConn := tconn.conn
	if tunnelConn == nil {
		return nil, fmt.Errorf("no tunnel conn")
	}
	buf := append(
		binary.LittleEndian.AppendUint16(
			[]byte{tunnelInitial},
			uint16(len(srvr.Path)),
		),
		srvr.Path...,
	)
	srvr.nWaiting.Add(1)
	if _, err = utils.WriteAll(tunnelConn, buf); err != nil {
		if n := srvr.nWaiting.Add(-1); n < 0 {
			srvr.nWaiting.Add(1)
		}
		// TODO
		return
	}
	timer := time.NewTimer(tunnelTimeout)
	select {
	case conn := <-srvr.tch:
		if !timer.Stop() {
			<-timer.C
		}
		client, err = jtrpc.WithRW(conn, conn)
	case <-timer.C:
		// TODO: Check this and (proxy listener)
		if n := srvr.nWaiting.Add(-1); n < 0 {
			srvr.nWaiting.Add(1)
		}
		err = jtrpc.ErrTimedOut
	}
	return
}

func (s *Server) LogString() string {
	return fmt.Sprintf(
		"name: %s | path: %s | addr: %s | hiden: %v",
		s.Name, s.Path, s.Addr, s.Hidden,
	)
}

type TConn struct {
	conn net.Conn
}
