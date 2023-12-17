package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/johnietre/jtrpc/go"
	utils "github.com/johnietre/utils/go"
)

func RunClient(args []string) {
	logger.SetFlags(0)

	fs := flag.NewFlagSet("server", flag.ExitOnError)
	addr := fs.String("addr", os.Getenv("JTRPC_PROXY_ADDR"), "Address of proxy")
	fs.StringVar(
		&config.Password, "p", "",
		"Password for proxy (not passed uses JTRPC_PROXY_PASSWORD environment variable value)",
	)
	get := fs.Bool("get", false, "Get list of servers on proxy")
	newPath := fs.String(
		"new", "",
		"Path to JSON file of servers to add to proxy's servers",
	)
	var delPaths ArrayValue
	fs.Var(
		&delPaths, "del",
		"Server path to delete (can be specified multiple times",
	)
	replacePath := fs.String(
		"replace", "",
		"Path to JSON file of servers to replace proxy's servers with",
	)
	fs.Parse(args)

	passed := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "p" {
			passed = true
		}
	})
	if !passed {
		config.Password = os.Getenv("JTRPC_PROXY_PASSWORD")
	}

	client, err := jtrpc.Dial(*addr)
	if err != nil {
		logger.Printf("Error connecting to proxy: %v", err)
	}
	defer client.Close()

	if *replacePath != "" {
		if *newPath != "" || len(delPaths) != 0 {
			client.Close()
			logger.Fatal("Cannot replace as well as add new and/or delete servers")
		}
		if err := sendReplaceServers(client, *replacePath); err != nil {
			logger.Printf("Error replacing: %v", err)
		}
	}

	if len(delPaths) != 0 {
		if err := sendDelServers(client, delPaths); err != nil {
			logger.Printf("Error adding server(s): %v", err)
		}
	}
	if *newPath != "" {
		if err := sendNewServers(client, *newPath); err != nil {
			logger.Printf("Error deleting path(s): %v", err)
		}
	}
	if *get {
		if err := getServers(client); err != nil {
			logger.Printf("Error getting servers: %v", err)
		}
	}
}

func newClientReq(path string, body []byte) *jtrpc.Request {
	req := jtrpc.NewRequest(context.Background(), path)
	req.Headers.Set("password", config.Password)
	if len(body) != 0 {
		req.SetBodyBytes(body)
	}
	return req
}

func getServers(client *jtrpc.Client) error {
	req := newClientReq("/admin/servers", nil)
	respChan, err := client.Send(req)
	if err != nil {
		return fmt.Errorf("error sending request: %v", err)
	}
	resp, ok := <-respChan.Chan()
	if !ok {
		return fmt.Errorf("request canceled")
	}
	if code := resp.StatusCode; code != jtrpc.StatusOK {
		body, err := resp.BodyString()
		if err != nil {
			return fmt.Errorf(
				"received error response (code %d); error reading body: %v",
				code, err,
			)
		}
		return fmt.Errorf("received error response (code %d): %s", code, body)
	}
	body, err := resp.BodyBytes()
	if err != nil {
		return fmt.Errorf("error reading response body: %v", err)
	}
	buf := bytes.NewBuffer(nil)
	if err := json.Indent(buf, body, "", "  "); err != nil {
		return fmt.Errorf(
			"error formatting response body: %v\nRaw body:\n%s",
			err, body,
		)
	}
	fmt.Println(buf.String())
	return nil
}

func sendNewServers(client *jtrpc.Client, path string) error {
	bytes, err := getServersBytes(path)
	if err != nil {
		return fmt.Errorf("error parsing servers: %v", err)
	}
	req := newClientReq("/admin/servers/new", bytes)
	respChan, err := client.Send(req)
	if err != nil {
		return fmt.Errorf("error sending request: %v", err)
	}
	resp, ok := <-respChan.Chan()
	if !ok {
		return fmt.Errorf("request canceled")
	}
	if code := resp.StatusCode; code == jtrpc.StatusPartialError {
		body, err := resp.BodyString()
		if err != nil {
			return fmt.Errorf("received partial error; error reading body: %v", err)
		}
		logger.Printf("Partial error with operation: %s", body)
		return nil
	} else if code != jtrpc.StatusOK {
		body, err := resp.BodyString()
		if err != nil {
			return fmt.Errorf(
				"received error response (code %d); error reading body: %v",
				code, err,
			)
		}
		return fmt.Errorf("received error response (code %d): %s", code, body)
	}
	return nil
}

func sendDelServers(client *jtrpc.Client, paths []string) error {
	bytes, err := json.Marshal(paths)
	if err != nil {
		return fmt.Errorf("error marshaling paths: %v", err)
	}
	req := newClientReq("/admin/servers/del", bytes)
	respChan, err := client.Send(req)
	if err != nil {
		return fmt.Errorf("error sending request: %v", err)
	}
	resp, ok := <-respChan.Chan()
	if !ok {
		return fmt.Errorf("request canceled")
	}
	if code := resp.StatusCode; code == jtrpc.StatusPartialError {
		body, err := resp.BodyString()
		if err != nil {
			return fmt.Errorf("received partial error; error reading body: %v", err)
		}
		logger.Printf("Partial error with operation: %s", body)
		return nil
	} else if code != jtrpc.StatusOK {
		body, err := resp.BodyString()
		if err != nil {
			return fmt.Errorf(
				"received error response (code %d); error reading body: %v",
				code, err,
			)
		}
		return fmt.Errorf("received error response (code %d): %s", code, body)
	}
	return nil
}

func sendReplaceServers(client *jtrpc.Client, path string) error {
	bytes, err := getServersBytes(path)
	if err != nil {
		return fmt.Errorf("error parsing servers: %v", err)
	}
	req := newClientReq("/admin/servers/replace", bytes)
	respChan, err := client.Send(req)
	if err != nil {
		return fmt.Errorf("error sending request: %v", err)
	}
	resp, ok := <-respChan.Chan()
	if !ok {
		return fmt.Errorf("request canceled")
	}
	if code := resp.StatusCode; code == jtrpc.StatusPartialError {
		body, err := resp.BodyString()
		if err != nil {
			return fmt.Errorf("received partial error; error reading body: %v", err)
		}
		logger.Printf("Partial error with operation: %s", body)
		return nil
	} else if code != jtrpc.StatusOK {
		body, err := resp.BodyString()
		if err != nil {
			return fmt.Errorf(
				"received error response (code %d); error reading body: %v",
				code, err,
			)
		}
		return fmt.Errorf("received error response (code %d): %s", code, body)
	}
	return nil
}

func getServersBytes(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	servers := make(map[string]*Server)
	// Just to make sure the JSON is valid
	if err := json.NewDecoder(f).Decode(&servers); err != nil {
		return nil, err
	}
	return json.Marshal(servers)
}

type ArrayValue []string

func (v ArrayValue) String() string {
	return strings.Join(
		utils.MapSlice(v, func(s string) string { return fmt.Sprintf("%q", s) }),
		",",
	)
}

func (v *ArrayValue) Set(s string) error {
	*v = append(*v, s)
	return nil
}
