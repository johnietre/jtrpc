package main

import (
	"encoding/json"
	"flag"
	"log"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/johnietre/jtrpc/go"
)

var (
	config = &Config{}
)

func RunServer(args []string) {
	logger.SetFlags(log.Lshortfile)

	fs := flag.NewFlagSet("server", flag.ExitOnError)
	addr := fs.String("addr", os.Getenv("JTRPC_PROXY_ADDR"), "Address to run on")
	configPath := fs.String("config", "", "Path to config file")
	fs.Parse(args)

	if *configPath != "" {
		f, err := os.Open(*configPath)
		if err != nil {
			log.Fatal(err)
		}
		if err := json.NewDecoder(f).Decode(config); err != nil {
			log.Fatal(err)
		}
		f.Close()
	}
	config.init()

	passed := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "addr" {
			passed = true
		}
	})
	if !passed {
		*addr = config.Addr
	}

	mux := newMux(config.Servers)
	srvr := jtrpc.NewServer(*addr)
	srvr.Mux = mux

	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		logger.Fatal(err)
	}
	logger.Print("Running on ", *addr)
	pln := NewProxyListener(ln, time.Second*10, mux.servers)
	if config.TunnelAddr != "" {
		err := pln.tunnelTo(config.TunnelAddr, config.Path, config.TunnelPwd)
		if err != nil {
			logger.Fatalf("error connecting to tunnel: %v:", err)
		}
	}
	go pln.Run()

	doneCh := make(chan struct{})
	go func() {
		if err := http.Serve(pln.HTTP(), srvr); err != nil {
			logger.Println("error running HTTP:", err)
		}
		close(doneCh)
	}()
	if err := srvr.Serve(pln.JtRPC()); err != nil {
		logger.Println("error running JtRPC:", err)
	}
	_, _ = <-doneCh
}

type Config struct {
	Addr     string             `json:"addr"`
	Servers  map[string]*Server `json:"servers,omitempty"`
	Password string             `json:"password,omitempty"`

	Path       string `json:"path,omitempty"`
	TunnelAddr string `json:"tunnel,omitempty"`
	TunnelPwd  string `json:"tunnelPassword"`
}

func (c *Config) init() {
	if config.Servers == nil {
		config.Servers = make(map[string]*Server)
	}
	for path, srvr := range c.Servers {
		srvr.init()
		srvr.Path = path
	}
}
