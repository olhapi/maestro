package main

import (
	"os"
)

var version = "dev"

func main() {
	os.Exit(execute(os.Args[1:], os.Stdout, os.Stderr))
}
