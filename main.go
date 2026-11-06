// Command fcm scans a Mac for junk that is provably safe to delete and, later,
// cleans it.
package main

import (
	"os"

	"github.com/thewindear/free-clean-my-macos/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:], os.Stdout, os.Stderr))
}
