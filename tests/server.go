package main

import (
	"context"
	"flag"
	"fmt"
	"log"

	jtrpc "github.com/johnietre/jtrpc/go"
)

func main() {
	addr := "127.0.0.1:8080"
	flag.StringVar(&addr, "addr", "127.0.0.1:8080", "Address to run on")
	flag.Parse()

	srvr := jtrpc.NewServer(addr)
	srvr.GlobalMiddleware(func(next jtrpc.Handler) jtrpc.Handler {
		return jtrpc.HandlerFunc(func(req *jtrpc.Request, resp *jtrpc.Response) {
			fmt.Println("========GLOBAL MIDDLEWARE========")
			next.Handle(req, resp)
			fmt.Println("========END GLOBAL MIDDLEWARE========")
		})
	})
	srvr.Middleware(func(next jtrpc.Handler) jtrpc.Handler {
		return jtrpc.HandlerFunc(func(req *jtrpc.Request, resp *jtrpc.Response) {
			fmt.Println("========START MIDDLEWARE========")
			fmt.Println("PATH:", req.Path)
			fmt.Println("HEADERS:", req.Headers.Parse())
			fmt.Println("========START HANDLER========")
			req = req.SetContext(
				context.WithValue(req.Context(), "another key", "another value"),
			)
			req = req.WithContext(
				context.WithValue(req.Context(), "some key", "some value"),
			)
			next.Handle(req, resp)
			fmt.Println("========END HANDLER========")
			fmt.Println("========END MIDDLEWARE========")
		})
	})
	srvr.HandleFunc("/yes", yesHandler)
	srvr.HandleFunc("/no", noHandler)
	srvr.HandleFunc(
		"/echo",
		echoHandler,
		func(next jtrpc.Handler) jtrpc.Handler {
			return jtrpc.HandlerFunc(func(req *jtrpc.Request, resp *jtrpc.Response) {
				fmt.Println("========ECHOING========")
				next.Handle(req, resp)
				fmt.Println("========DONE ECHOING========")
			})
		},
	)
	srvr.StreamMiddleware(func(next jtrpc.Handler) jtrpc.Handler {
		return jtrpc.HandlerFunc(func(req *jtrpc.Request, resp *jtrpc.Response) {
			fmt.Println("========STREAM MIDDLEWARE========")
			next.Handle(req, resp)
			fmt.Println("========END STREAM MIDDLEWARE========")
		})
	})
	srvr.HandleStreamFunc(
		"/stream",
		streamHandler,
		func(next jtrpc.Handler) jtrpc.Handler {
			return jtrpc.HandlerFunc(func(req *jtrpc.Request, resp *jtrpc.Response) {
				fmt.Println("STREAM:", req.Path)
				next.Handle(req, resp)
			})
		},
	)
	log.Print("Running on ", addr)
	panic(srvr.Run())
}

func yesHandler(req *jtrpc.Request, resp *jtrpc.Response) {
	fmt.Printf("Headers: %+v\n", req.Headers.Parse())
	fmt.Println("Body:", req.Body.String())
}

func noHandler(req *jtrpc.Request, resp *jtrpc.Response) {
	resp.StatusCode = jtrpc.StatusBadRequest
	resp.SetBodyString("no good dog")
}

func echoHandler(req *jtrpc.Request, resp *jtrpc.Response) {
	resp.Headers = req.Headers
	resp.SetBodyReader(req.Body, int64(req.Body.Len()))
}

func streamHandler(stream *jtrpc.Stream) {
	defer stream.Close()
	fmt.Println("Context Value:", stream.Request().Context().Value("some key"))
	fmt.Println("Context Value:", stream.Request().Context().Value("another key"))
	for {
		msg, err := stream.Recv()
		if err != nil {
			if sce := jtrpc.GetStreamClosedError(err); sce != nil {
				log.Println("Stream closed error:", err)
			} else {
				log.Println("Other error:", err)
			}
			break
		}
		fmt.Println("Incoming message Body:", msg.BodyString())
		newMsg := jtrpc.Message{}
		newMsg.SetBodyBytes(reverse(msg.BodyBytes()))
		fmt.Println("Outgoing message Body:", newMsg.BodyString())
		if err := stream.Send(newMsg); err != nil {
			log.Println(err)
			break
		}
	}
	fmt.Println("Closed")
}

func reverse(b []byte) []byte {
	for i := range b[:len(b)/2] {
		b[i], b[len(b)-i-1] = b[len(b)-i-1], b[i]
	}
	return b
}
