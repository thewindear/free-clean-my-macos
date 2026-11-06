// Command fcm finds junk a Scan Rule declared safe to delete, shows it as a
// list, and cleans what the user selects.
package main

import (
	"os"

	"github.com/thewindear/free-clean-my-macos/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:], os.Stdout, os.Stderr))
}
