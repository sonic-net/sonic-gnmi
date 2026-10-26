package main

import (
	"fmt"
	"os"

	"github.com/sonic-net/sonic-gnmi/internal/imageexport"
)

func main() {
	os.Exit(run(os.Args, 3))
}

func run(args []string, socket int) int {
	if len(args) != 5 {
		fmt.Fprintln(os.Stderr, "usage: sonic-image-worker HOST_ROOT OPERATION PATH TOKEN")
		return 2
	}
	return imageexport.ServeWorker(socket, args[1], args[2], args[3], args[4])
}
