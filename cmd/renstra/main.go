package main

import (
	"fmt"
	"os"

	"renstra/internal/config"
)

func main() {
	args := os.Args[1:]
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "pemakaian: renstra <chat|config> [argumen]")
		os.Exit(2)
	}
	switch args[0] {
	case "config":
		if len(args) > 1 && args[1] == "path" {
			fmt.Println(config.Home())
			return
		}
		fmt.Fprintln(os.Stderr, "pemakaian: renstra config path")
		os.Exit(2)
	case "chat":
		fmt.Fprintln(os.Stderr, "chat belum tersedia (dipasang di Task 9)")
		os.Exit(1)
	default:
		fmt.Fprintf(os.Stderr, "perintah tidak dikenal: %s\n", args[0])
		os.Exit(2)
	}
}
