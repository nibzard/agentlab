package proxy

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"strings"
	"sync"
)

const (
	agentlabHostsMarker = "# agentlab-proxy-begin"
	agentlabHostsEnd    = "# agentlab-proxy-end"
)

// DNSResolver manages DNS entries for sandbox subdomains.
//
// It writes entries to a hosts-style file, allowing resolution of
// sandbox subdomains (e.g., mybox.agentlab.local) to their IPs.
// The managed section is delimited by markers so it can be updated
// atomically without affecting other entries.
type DNSResolver struct {
	FilePath string
	Logger   *log.Logger
	mu       sync.Mutex
}

// NewDNSResolver creates a resolver that writes to the given file.
func NewDNSResolver(filePath string, logger *log.Logger) *DNSResolver {
	if strings.TrimSpace(filePath) == "" {
		filePath = "/etc/hosts"
	}
	if logger == nil {
		logger = log.Default()
	}
	return &DNSResolver{
		FilePath: filePath,
		Logger:   logger,
	}
}

// dnsEntry represents a single hosts file entry.
type dnsEntry struct {
	IP      string
	Domain  string
}

// AddEntry adds or updates a DNS entry for the given domain.
//
// It writes to the managed section of the hosts file. The IP is typically
// the host machine's IP on the agent subnet.
func (d *DNSResolver) AddEntry(ctx context.Context, ip, domain string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	entries, err := d.readExisting()
	if err != nil {
		return fmt.Errorf("read hosts: %w", err)
	}

	// Update or add entry
	found := false
	for i, e := range entries {
		if e.Domain == domain {
			entries[i].IP = ip
			found = true
			break
		}
	}
	if !found {
		entries = append(entries, dnsEntry{IP: ip, Domain: domain})
	}

	if err := d.writeEntries(entries); err != nil {
		return fmt.Errorf("write hosts: %w", err)
	}

	d.Logger.Printf("proxy: dns %s -> %s", domain, ip)
	return nil
}

// RemoveEntry removes a DNS entry for the given domain.
func (d *DNSResolver) RemoveEntry(ctx context.Context, domain string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	entries, err := d.readExisting()
	if err != nil {
		return fmt.Errorf("read hosts: %w", err)
	}

	filtered := make([]dnsEntry, 0, len(entries))
	for _, e := range entries {
		if e.Domain != domain {
			filtered = append(filtered, e)
		}
	}

	if err := d.writeEntries(filtered); err != nil {
		return fmt.Errorf("write hosts: %w", err)
	}

	d.Logger.Printf("proxy: dns removed %s", domain)
	return nil
}

// readExisting reads the current agentlab-managed entries from the hosts file.
func (d *DNSResolver) readExisting() ([]dnsEntry, error) {
	data, err := os.ReadFile(d.FilePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var entries []dnsEntry
	inSection := false

	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == agentlabHostsMarker {
			inSection = true
			continue
		}
		if line == agentlabHostsEnd {
			inSection = false
			continue
		}
		if !inSection {
			continue
		}
		if strings.HasPrefix(line, "#") || line == "" {
			continue
		}

		fields := strings.Fields(line)
		if len(fields) >= 2 {
			ip := net.ParseIP(fields[0])
			if ip != nil {
				entries = append(entries, dnsEntry{
					IP:     fields[0],
					Domain: fields[1],
				})
			}
		}
	}

	return entries, scanner.Err()
}

// writeEntries rewrites the hosts file, preserving non-agentlab content
// and updating the managed section with the provided entries.
func (d *DNSResolver) writeEntries(entries []dnsEntry) error {
	data, err := os.ReadFile(d.FilePath)
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	var before, after strings.Builder
	inSection := false
	pastSection := false

	if data != nil {
		scanner := bufio.NewScanner(bytes.NewReader(data))
		for scanner.Scan() {
			line := scanner.Text()
			trimmed := strings.TrimSpace(line)

			if trimmed == agentlabHostsMarker {
				inSection = true
				continue
			}
			if trimmed == agentlabHostsEnd {
				inSection = false
				pastSection = true
				continue
			}

			if inSection {
				continue
			}

			if pastSection {
				after.WriteString(line)
				after.WriteByte('\n')
			} else {
				before.WriteString(line)
				before.WriteByte('\n')
			}
		}
	}

	var buf bytes.Buffer
	buf.WriteString(before.String())

	// Write managed section
	buf.WriteString(agentlabHostsMarker + "\n")
	for _, e := range entries {
		buf.WriteString(fmt.Sprintf("%s %s\n", e.IP, e.Domain))
	}
	buf.WriteString(agentlabHostsEnd + "\n")

	buf.WriteString(after.String())

	return os.WriteFile(d.FilePath, buf.Bytes(), 0o644)
}
