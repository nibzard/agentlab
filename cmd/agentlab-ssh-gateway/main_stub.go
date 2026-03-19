//go:build !sshgateway

package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "agentlab-ssh-gateway requires the 'sshgateway' build tag")
	os.Exit(1)
}
