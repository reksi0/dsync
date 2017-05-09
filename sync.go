package main

import (
	"os"
)

func main() {
	dir := os.Args[2]
	addressOrPort := os.Args[3]

	switch os.Args[1] {
	case "server":
		server(dir, addressOrPort)
	case "client":
		client(dir, addressOrPort)
	}
}
