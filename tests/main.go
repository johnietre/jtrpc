package main

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"os"
	"strings"

	jtrpc "github.com/johnietre/jtrpc/go"
)

func main() {
	log.SetFlags(log.Lshortfile)
	client, err := jtrpc.Dial("127.0.0.1:8080")
	if err != nil {
		log.Fatal(err)
	}

	req := jtrpc.NewRequest(context.Background(), "/echo")
	req.SetBodyString("no and yes")
	req.Headers.Set("h1", "value1")
	req.Headers.Set("h2", "value2")
	req.Headers.Set("h3579", "value3579")
	resp := <-must(client.Send(req)).Chan()
	resp.Headers.Parse()
	fmt.Println("Response:", resp)
	fmt.Println("Body:", must(resp.BodyString()))
	fmt.Println()

	req = jtrpc.NewRequest(context.Background(), "/yes")
	req.SetBodyString("no and yes")
	resp = <-must(client.Send(req)).Chan()
	resp.Headers.Parse()
	fmt.Println("Response:", resp)
	fmt.Println("Body:", must(resp.BodyString()))
	fmt.Println()

	req = jtrpc.NewRequest(context.Background(), "/yes")
	resp = <-must(client.Send(req)).Chan()
	resp.Headers.Parse()
	fmt.Println("Response:", resp)
	fmt.Println("Body:", must(resp.BodyString()))
	fmt.Println()

	req = jtrpc.NewRequest(context.Background(), "/no")
	req.SetBodyString("no and yes")
	resp = <-must(client.Send(req)).Chan()
	resp.Headers.Parse()
	fmt.Println("Response:", resp)
	fmt.Println("Body:", must(resp.BodyString()))
	fmt.Println()

	req = jtrpc.NewRequest(context.Background(), "/stream")
	req.SetStream(true)
	req.Headers.Set("stream_header1", "value1")
	req.Headers.Set("stream_header2", "value2")
	req.Headers.Set("stream_header3579", "value3579")
	resp = <-must(client.Send(req)).Chan()
	resp.Headers.Parse()
	fmt.Println("Response:", resp)
	fmt.Println("Body:", must(resp.BodyString()))
	stream := resp.Stream
	if stream == nil {
		panic("got nil stream")
	}
	defer stream.Close()
	for {
		line := readline("Message: ")
		if line == "exit" {
			break
		}
		if err := stream.Send(jtrpc.NewMessage([]byte(line))); err != nil {
			log.Fatal(err)
		}
		msg := must(stream.Recv())
		fmt.Println("Received:", msg.BodyString())
	}
}

var stdinReader = bufio.NewReader(os.Stdin)

func readline(prompt ...string) string {
	if len(prompt) != 0 {
		fmt.Print(prompt[0])
	}
	return strings.TrimSpace(must(stdinReader.ReadString('\n')))
}

func must[T any](t T, err error) T {
	if err != nil {
		log.Fatal(err)
	}
	return t
}
