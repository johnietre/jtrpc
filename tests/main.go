package main

import (
	"fmt"
	"log"

	jtrpc "github.com/johnietre/jtrpc/go"
)

func main() {
  addr := "127.0.0.1:8080"
	srvr := jtrpc.NewServer(addr)
	srvr.HandleFunc("/yes", yesHandler)
	srvr.HandleFunc("/no", noHandler)
	srvr.HandleFunc("/echo", echoHandler)
	srvr.HandleStreamFunc("/stream", streamHandler)
  log.Print("Running on ", addr)
	panic(srvr.Run())
}

func yesHandler(req *jtrpc.Request, resp *jtrpc.Response) {
	fmt.Printf("Headers: %+v\n", req.Headers.Map())
	fmt.Println("Body:", req.Body.String())
}

func noHandler(req *jtrpc.Request, resp *jtrpc.Response) {
	resp.StatusCode = jtrpc.StatusBadRequest
	resp.SetBodyString("no good dog")
}

func echoHandler(req *jtrpc.Request, resp *jtrpc.Response) {
  for k, v := range req.Headers.Map() {
    resp.Headers[k] = v
  }
  resp.SetBodyReader(req.Body, int64(req.Body.Len()))
}

func streamHandler(stream *jtrpc.Stream) {
  defer stream.Close()
  for {
    msg, err := stream.Recv()
    if err != nil {
      log.Println(err)
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
