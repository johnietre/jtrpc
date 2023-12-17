package main

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/johnietre/utils/go"
)

var ErrClosed = errors.New("closed")

type ProxyListener struct {
	ln          net.Listener
	readTimeout time.Duration

	httpCh, jtrpcCh *utils.UChan[net.Conn]
	tunnelConn      net.Conn

	servers *utils.RWMutex[serverMap]

	closed atomic.Bool
	errVal *utils.AValue[utils.ErrorValue]
}

func NewProxyListener(
	ln net.Listener, readTimeout time.Duration,
	servers *utils.RWMutex[serverMap],
) *ProxyListener {
	return &ProxyListener{
		ln:          ln,
		readTimeout: readTimeout,
		httpCh:      utils.NewUChan[net.Conn](50),
		jtrpcCh:     utils.NewUChan[net.Conn](50),
		servers:     servers,
		errVal:      utils.NewAValue(utils.NewErrorValue(nil)),
	}
}

func (pl *ProxyListener) tunnelTo(addr, path, pwd string) error {
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		return err
	}
	closeConn := utils.NewT(true)
	defer deferClose(conn, closeConn)

	msg := binary.LittleEndian.AppendUint16(
		[]byte{tunnelConnectInitial},
		uint16(len(pwd)),
	)
	msg = binary.LittleEndian.AppendUint16(msg, uint16(len(path)))
	msg = append(msg, pwd...)
	msg = append(msg, path...)
	if _, err := utils.WriteAll(conn, msg); err != nil {
		return err
	}

	msg = []byte{0, 0, 0}
	if _, err := io.ReadFull(conn, msg[:1]); err != nil {
		return err
	}
	if msg[0] == errorInitial {
		if _, err := io.ReadFull(conn, msg[1:]); err != nil {
			return fmt.Errorf("error reading error received: %v", err)
		}
		errMsg := make([]byte, binary.LittleEndian.Uint16(msg[1:]))
		if _, err := io.ReadFull(conn, errMsg); err != nil {
			return fmt.Errorf("error reading error received: %v", err)
		}
		return fmt.Errorf("received error: %s", errMsg)
	} else if msg[0] != tunnelConnectInitial {
		return fmt.Errorf(
			"unexpected response, expected %d, got %d",
			tunnelConnectInitial, msg[0],
		)
	}
	*closeConn, pl.tunnelConn, config.TunnelPwd = false, conn, pwd
	go pl.runTunnel(conn)
	return nil
}

func (pl *ProxyListener) HTTP() net.Listener {
	return &HTTPListener{pl: pl}
}

func (pl *ProxyListener) JtRPC() net.Listener {
	return &JtRPCListener{pl: pl}
}

func (pl *ProxyListener) Run() error {
	for {
		conn, err := pl.ln.Accept()
		if err != nil {
			pl.closeWithErr(err)
			return err
		}
		go pl.handle(conn)
	}
}

const (
	jtrpcInitial byte = 0
	// Used when the proxy that's been tunneled to needs a new connectiong from
	// the tunneling proxy
	tunnelInitial byte = 1
	// Used when a proxy is started and needs to tunnel
	tunnelConnectInitial byte = 2
	errorInitial         byte = 255
)

const initialLen = 1

func (pl *ProxyListener) handle(conn net.Conn) {
	closeConn := utils.NewT(true)
	defer deferClose(conn, closeConn)

	if pl.readTimeout != 0 {
		if err := conn.SetReadDeadline(time.Now().Add(pl.readTimeout)); err != nil {
			return
		}
	}
	var initial [initialLen]byte
	if _, err := io.ReadFull(conn, initial[:]); err != nil {
		return
	}
	if pl.readTimeout != 0 {
		if err := conn.SetReadDeadline(time.Time{}); err != nil {
			return
		}
	}
	conn = newConn(conn, initial[:])
	if initial[0] == jtrpcInitial {
		if !pl.jtrpcCh.Send(conn) {
			return
		}
	} else if initial[0] == tunnelInitial {
		go pl.handleTunnelConn(conn)
	} else if initial[0] == tunnelConnectInitial {
		go pl.handleTunnelConnect(conn)
	} else if !pl.httpCh.Send(conn) {
		return
	}
	*closeConn = false
}

func (pl *ProxyListener) handleTunnelConn(conn net.Conn) {
	closeConn := utils.NewT(true)
	defer deferClose(conn, closeConn)

	io.CopyN(io.Discard, conn, int64(initialLen))

	var lenBytes [4]byte
	// Get password
	if _, err := io.ReadFull(conn, lenBytes[:]); err != nil {
		// TODO?
		logger.Println("error reading:", err)
		return
	}
	pwd := make([]byte, binary.LittleEndian.Uint16(lenBytes[:]))
	if _, err := io.ReadFull(conn, pwd); err != nil {
		// TODO?
		logger.Println("error reading:", err)
		return
	}
	if string(pwd) != config.Password {
		writeErrMsg(conn, "incorrect password")
		return
	}

	// Get path
	path := make([]byte, binary.LittleEndian.Uint16(lenBytes[2:]))
	if _, err := io.ReadFull(conn, path); err != nil {
		// TODO?
		logger.Println("error reading:", err)
		return
	}

	// Get server
	srvr, ok := (*Server)(nil), false
	pl.servers.RApply(func(mp *serverMap) {
		srvr, ok = (*mp)[string(path)]
	})
	if !ok || srvr.tch == nil {
		writeErrMsg(conn, "server doesn't exist")
		return
	}

	if srvr.nWaiting.Load() == 0 {
		println("nope")
		writeErrMsg(conn, "server not expecting new connections")
		return
	}
	srvr.nWaiting.Add(-1)
	select {
	case srvr.tch <- conn:
		// Send ready byte
		if _, err := conn.Write([]byte{tunnelInitial}); err != nil {
			// TODO
		} else {
			*closeConn = false
		}
		return
	default:
	}
	writeErrMsg(conn, "internal server error") // TODO
}

func (pl *ProxyListener) runTunnel(tconn net.Conn) {
	defer tconn.Close()
	for {
		buf := make([]byte, 3)
		if _, err := tconn.Read(buf); err != nil {
			logger.Printf("error reading tunnel conn: %v", err)
			return
		}
		if buf[0] != tunnelInitial {
			// TODO
			continue
		}
		buf = append(buf, make([]byte, binary.LittleEndian.Uint16(buf[1:]))...)
		path := buf[3:]
		if _, err := io.ReadFull(tconn, path); err != nil {
			// TODO
			return
		}
		go func(path []byte) {
			addr := tconn.RemoteAddr()
			conn, err := net.Dial(addr.Network(), addr.String())
			if err != nil {
				logger.Printf(
					"error creating new tunnel (connecting to) proxy: %v",
					err,
				)
				return
			}

			closeConn := utils.NewT(true)
			defer deferClose(conn, closeConn)

			bytes := binary.LittleEndian.AppendUint16(
				[]byte{tunnelInitial},
				uint16(len(config.TunnelPwd)),
			)
			bytes = binary.LittleEndian.AppendUint16(bytes, uint16(len(path)))
			bytes = append(bytes, config.TunnelPwd...)
			bytes = append(bytes, path...)
			if _, err := conn.Write(bytes); err != nil {
				logger.Printf("error writing to new tunnel: %v", err)
				return
			}
			if _, err := conn.Read(bytes[:1]); err != nil {
				logger.Printf("error reading from new tunnel: %v", err)
				return
			} else if bytes[0] == errorInitial {
				if _, err := io.ReadFull(conn, bytes[1:3]); err != nil {
					logger.Printf("error reading from new tunnel: %v", err)
					return
				}
				errMsg := make([]byte, binary.LittleEndian.Uint16(bytes[1:]))
				if _, err := io.ReadFull(conn, errMsg); err != nil {
					logger.Printf("error reading from new tunnel: %v", err)
					return
				}
				logger.Printf("error received when creating new tunnel: %s", errMsg)
				return
			} else if bytes[0] != tunnelInitial {
				logger.Printf(
					"unexpected response, expected %d, got %d",
					tunnelInitial, bytes[0],
				)
				return
			}
			*closeConn = false
			pl.handle(conn)
		}(path)
	}
}

func (pl *ProxyListener) handleTunnelConnect(conn net.Conn) {
	closeConn := utils.NewT(true)
	defer deferClose(conn, closeConn)
	buf := make([]byte, 5)
	if _, err := io.ReadFull(conn, buf); err != nil {
		// TODO
		return
	}
	pwdLen := binary.LittleEndian.Uint16(buf[1:])
	pathLen := binary.LittleEndian.Uint16(buf[3:])
	// Get password
	pwd := make([]byte, pwdLen)
	if _, err := io.ReadFull(conn, pwd); err != nil {
		// TODO?
		return
	}
	if string(pwd) != config.Password {
		writeErrMsg(conn, "incorrect password")
		return
	}
	// Get path
	path := make([]byte, pathLen)
	if _, err := io.ReadFull(conn, path); err != nil {
		// TODO?
		return
	}
	srvrs := *pl.servers.RLock()
	srvr, ok := srvrs[string(path)]
	pl.servers.RUnlock()
	if !ok {
		writeErrMsg(conn, "incorrect password")
		return
	} else if srvr.Addr != "tunnel" {
		writeErrMsg(conn, "not a tunnel")
		return
	} else if !srvr.tunnelConn.CompareAndSwap(TConn{}, TConn{conn: conn}) {
		writeErrMsg(conn, "tunnel already connected")
		return
	} else if _, err := conn.Write([]byte{tunnelConnectInitial}); err != nil {
		// TODO?
		srvr.tunnelConn.Store(TConn{})
		return
	}
	*closeConn = false
}

func (pl *ProxyListener) Close() error {
	return pl.closeWithErr(nil)
}

func (pl *ProxyListener) closeWithErr(err error) (closeEerr error) {
	closeEerr = pl.ln.Close()
	if pl.closed.Swap(true) {
		return
	}
	if err == nil {
		err = ErrClosed
	}
	pl.errVal.Store(utils.NewErrorValue(err))
	pl.closeHttp(false)
	pl.closeJtrpc(false)
	return
}

func (pl *ProxyListener) closeHttp(check bool) {
	pl.httpCh.Close()
	if check && pl.jtrpcCh.IsClosed() {
		pl.ln.Close()
	}
}

func (pl *ProxyListener) closeJtrpc(check bool) {
	pl.jtrpcCh.Close()
	if check && pl.httpCh.IsClosed() {
		pl.ln.Close()
	}
}

func (pl *ProxyListener) Error() error {
	return pl.errVal.Load().Error
}

type HTTPListener struct {
	pl *ProxyListener
}

func (jl *HTTPListener) Accept() (net.Conn, error) {
	conn, ok := jl.pl.httpCh.Recv()
	if !ok {
		err := jl.pl.Error()
		if err == nil {
			err = ErrClosed
		}
		return nil, err
	}
	return conn, nil
}

func (jl *HTTPListener) Close() error {
	jl.pl.closeJtrpc(true)
	return nil
}

func (jl *HTTPListener) Addr() net.Addr {
	return jl.pl.ln.Addr()
}

type JtRPCListener struct {
	pl *ProxyListener
}

func (jl *JtRPCListener) Accept() (net.Conn, error) {
	conn, ok := jl.pl.jtrpcCh.Recv()
	if !ok {
		err := jl.pl.Error()
		if err == nil {
			err = ErrClosed
		}
		return nil, err
	}
	return conn, nil
}

func (jl *JtRPCListener) Close() error {
	jl.pl.closeJtrpc(true)
	return nil
}

func (jl *JtRPCListener) Addr() net.Addr {
	return jl.pl.ln.Addr()
}

type Conn struct {
	net.Conn
	initial     []byte
	mtx         sync.Mutex
	readInitial atomic.Bool
}

func newConn(conn net.Conn, initial []byte) *Conn {
	return &Conn{Conn: conn, initial: initial}
}

func (c *Conn) clearInitial() {
	if c.readInitial.Swap(true) {
		return
	}
	c.mtx.Lock()
	c.initial = []byte{}
	c.mtx.Unlock()
}

func (c *Conn) Read(p []byte) (n int, err error) {
	if len(p) == 0 {
		return 0, nil
	}
	if !c.readInitial.Load() {
		c.mtx.Lock()
		n = copy(p, c.initial)
		if n != 0 {
			p, c.initial = p[n:], c.initial[n:]
			if len(c.initial) == 0 {
				//c.initial = nil
				c.initial = []byte{}
				c.readInitial.Store(true)
			}
		}
		c.mtx.Unlock()
		if len(p) == 0 {
			return
		}
	}
	ni := 0
	ni, err = c.Conn.Read(p)
	n += ni
	return
}

func deferClose(conn net.Conn, closeConn *bool) {
	if *closeConn {
		conn.Close()
	}
}

func writeErrMsg(conn net.Conn, msg string) error {
	bytes := binary.LittleEndian.AppendUint16(
		[]byte{errorInitial},
		uint16(len(msg)),
	)
	_, err := conn.Write(append(bytes, msg...))
	if err != nil {
		logger.Println(err)
	}
	return err
}
