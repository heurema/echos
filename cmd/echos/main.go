// Command echos shares a running coding-agent session with a friend,
// end-to-end encrypted through an ephemeral zero-knowledge relay.
package main

import (
	"os"
)

func main() {
	os.Exit(Execute(os.Args[1:], os.Stdout, os.Stderr))
}
