package main

import (
	"log"
	"os"
)

var (
	logger = log.Default()
)

func main() {
	if len(os.Args) == 1 {
		logger.Fatal(`Missing "server" or "client" subcommand`)
	}
	switch os.Args[1] {
	case "server":
		RunServer(os.Args[2:])
	case "client":
		RunClient(os.Args[2:])
	default:
		logger.Fatalf(
			`Invalid subcommand %q, use either "server" or "client"`,
			os.Args[1],
		)
	}
}
