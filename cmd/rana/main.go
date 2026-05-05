package main

import (
	"fmt"
	"os"

	"ranalang/repl"
	"ranalang/runner"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "repl":
		repl.Start(os.Stdin, os.Stdout, os.Stderr)
	case "run":
		if len(os.Args) != 3 {
			fmt.Fprintln(os.Stderr, "error: rana run expects one file")
			usage()
			os.Exit(1)
		}
		os.Exit(runner.RunFile(os.Args[2], os.Stdout, os.Stderr))
	default:
		fmt.Fprintf(os.Stderr, "error: unknown command %q\n", os.Args[1])
		usage()
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage:")
	fmt.Fprintln(os.Stderr, "  rana repl")
	fmt.Fprintln(os.Stderr, "  rana run path/to/file.rana")
}
