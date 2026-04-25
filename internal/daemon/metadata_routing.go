package daemon

import (
	"fmt"
	"log"
	"net"
	"os/exec"
	"strings"
)

const (
	// metadataIP is the cloud-standard link-local address for instance metadata.
	// See: AWS, GCP, Azure, and other cloud providers use this address.
	metadataIP = "169.254.169.254"

	// metadataPort is the standard port for the metadata endpoint.
	metadataPort = 80

	// metadataComment is the iptables rule comment used to identify agentlab rules.
	metadataComment = "agentlab-metadata-endpoint"
)

// MetadataRouting manages iptables DNAT rules that make the metadata endpoint
// accessible at 169.254.169.254:80 from inside sandboxes.
//
// The DNAT rule rewrites destinations from 169.254.169.254:80 to the daemon's
// bootstrap listener address, so sandboxes can query metadata using the
// cloud-standard IP without any per-sandbox configuration.
//
// This requires the daemon to run with sufficient privileges to execute iptables.
// If the daemon lacks privileges, the routing setup is skipped with a warning.
type MetadataRouting struct {
	// targetAddr is the daemon's bootstrap listener address (host:port).
	targetAddr string

	// active indicates whether the DNAT rule was successfully installed.
	active bool

	// runner executes iptables commands. Defaults to the real iptables runner.
	// Can be overridden in tests.
	runner iptablesRunner
}

// iptablesRunner executes an iptables command with the given arguments.
type iptablesRunner interface {
	Run(args ...string) ([]byte, error)
}

// realIPTablesRunner runs iptables via exec.Command.
type realIPTablesRunner struct{}

func (r *realIPTablesRunner) Run(args ...string) ([]byte, error) {
	cmd := exec.Command("iptables", args...)
	return cmd.CombinedOutput()
}

// NewMetadataRouting creates a new MetadataRouting instance targeting the
// given bootstrap listener address.
func NewMetadataRouting(bootstrapListen string) *MetadataRouting {
	return &MetadataRouting{
		targetAddr: bootstrapListen,
		runner:     &realIPTablesRunner{},
	}
}

// Setup installs the iptables DNAT rule to route 169.254.169.254:80 to the
// daemon's bootstrap listener.
//
// The rule is added to the NAT table's PREROUTING chain so that incoming
// packets from sandboxes destined for the metadata IP are rewritten to
// reach the daemon's actual listener.
//
// Returns an error if the iptables command fails (e.g., insufficient privileges).
func (mr *MetadataRouting) Setup() error {
	if mr == nil {
		return nil
	}
	host, port, err := net.SplitHostPort(mr.targetAddr)
	if err != nil {
		return fmt.Errorf("metadata routing: parse bootstrap addr %q: %w", mr.targetAddr, err)
	}
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	destination := fmt.Sprintf("%s:%s", host, port)

	// First, try to remove any existing rule (idempotent setup).
	_ = mr.deleteRule(destination)

	args := []string{
		"-t", "nat",
		"-A", "PREROUTING",
		"-d", metadataIP,
		"-p", "tcp",
		"--dport", fmt.Sprintf("%d", metadataPort),
		"-j", "DNAT",
		"--to-destination", destination,
		"-m", "comment",
		"--comment", metadataComment,
	}

	output, err := mr.runner.Run(args...)
	if err != nil {
		return fmt.Errorf("metadata routing: iptables DNAT: %w: %s", err, strings.TrimSpace(string(output)))
	}

	mr.active = true
	log.Printf("metadata routing: DNAT 169.254.169.254:%d → %s", metadataPort, destination)
	return nil
}

// Cleanup removes the iptables DNAT rule installed by Setup.
//
// This should be called during daemon shutdown to avoid leaving stale rules.
// Errors are logged but not returned, as cleanup is best-effort.
func (mr *MetadataRouting) Cleanup() {
	if mr == nil || !mr.active {
		return
	}

	host, port, err := net.SplitHostPort(mr.targetAddr)
	if err != nil {
		log.Printf("metadata routing: cleanup: parse addr: %v", err)
		return
	}
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	destination := fmt.Sprintf("%s:%s", host, port)

	if err := mr.deleteRule(destination); err != nil {
		log.Printf("metadata routing: cleanup: %v", err)
		return
	}
	mr.active = false
	log.Printf("metadata routing: removed DNAT rule for 169.254.169.254")
}

func (mr *MetadataRouting) deleteRule(destination string) error {
	args := []string{
		"-t", "nat",
		"-D", "PREROUTING",
		"-d", metadataIP,
		"-p", "tcp",
		"--dport", fmt.Sprintf("%d", metadataPort),
		"-j", "DNAT",
		"--to-destination", destination,
		"-m", "comment",
		"--comment", metadataComment,
	}
	output, err := mr.runner.Run(args...)
	if err != nil {
		return fmt.Errorf("iptables -D: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}
