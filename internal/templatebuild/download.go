package templatebuild

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

func publicIP(ip net.IP) bool {
	for _, cidr := range []string{"100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4", "2001:db8::/32", "64:ff9b::/96", "64:ff9b:1::/48"} {
		_, network, _ := net.ParseCIDR(cidr)
		if network.Contains(ip) {
			return false
		}
	}
	return ip.IsGlobalUnicast() && !ip.IsPrivate() && !ip.IsLoopback() && !ip.IsLinkLocalUnicast() && !ip.IsLinkLocalMulticast() && !ip.IsUnspecified()
}

// Pin the validated DNS result to the connection, including on every redirect.
// A proxy from the daemon's environment must never bypass this address policy.
func ImageClient(hosts []string) *http.Client {
	tr := &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}, ResponseHeaderTimeout: 30 * time.Second}
	tr.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, e := net.SplitHostPort(address)
		if e != nil {
			return nil, e
		}
		ips, e := net.DefaultResolver.LookupIPAddr(ctx, host)
		if e != nil {
			return nil, e
		}
		if len(ips) == 0 {
			return nil, fmt.Errorf("image host has no addresses")
		}
		for _, ip := range ips {
			if !publicIP(ip.IP) {
				return nil, fmt.Errorf("image host resolves to a prohibited address")
			}
		}
		d := net.Dialer{Timeout: 30 * time.Second}
		return d.DialContext(ctx, network, net.JoinHostPort(ips[0].IP.String(), port))
	}
	return &http.Client{Transport: imageTransport{tr, hosts}, CheckRedirect: func(r *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return fmt.Errorf("too many image redirects")
		}
		return CheckURL(r.URL.String(), hosts)
	}}
}
func fetch(ctx context.Context, c *http.Client, raw string, w io.Writer, max int64) error {
	req, e := http.NewRequestWithContext(ctx, "GET", raw, nil)
	if e != nil {
		return e
	}
	resp, e := c.Do(req)
	if e != nil {
		return e
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("image server returned HTTP %d", resp.StatusCode)
	}
	n, e := io.Copy(w, io.LimitReader(resp.Body, max+1))
	if e != nil {
		return e
	}
	if n > max {
		return fmt.Errorf("image response exceeds size limit")
	}
	return nil
}
func ResolveChecksum(ctx context.Context, c *http.Client) (string, error) {
	var b strings.Builder
	if e := fetch(ctx, c, "https://cloud-images.ubuntu.com/noble/current/SHA256SUMS", &b, 4<<20); e != nil {
		return "", e
	}
	u, _ := url.Parse(DefaultImage)
	filename := path.Base(u.Path)
	for _, line := range strings.Split(b.String(), "\n") {
		f := strings.Fields(line)
		if len(f) == 2 && strings.TrimPrefix(f[1], "*") == filename && shaRE.MatchString(strings.ToLower(f[0])) {
			return strings.ToLower(f[0]), nil
		}
	}
	return "", fmt.Errorf("published checksum does not contain exact image filename")
}
func verifyFile(file, digest string) error {
	f, e := os.Open(file)
	if e != nil {
		return e
	}
	defer f.Close()
	info, e := f.Stat()
	if e != nil {
		return e
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("image cache entry is not a regular file")
	}
	h := sha256.New()
	if _, e = io.Copy(h, f); e != nil {
		return e
	}
	if hex.EncodeToString(h.Sum(nil)) != digest {
		return fmt.Errorf("template_checksum_mismatch")
	}
	return nil
}

// DownloadImage publishes a cache entry only after successful checksum verification.
// A digest lock serializes writers; the worker unit owns the lock lifetime.
func DownloadImage(ctx context.Context, p Plan, cache string, hosts []string) (string, string, error) {
	c := ImageClient(hosts)
	raw, digest := p.ImageURL, p.ImageSHA256
	var e error
	if raw == "" {
		raw = DefaultImage
		digest, e = ResolveChecksum(ctx, c)
		if e != nil {
			return "", "", e
		}
	}
	if e = CheckURL(raw, hosts); e != nil {
		return "", "", e
	}
	if !shaRE.MatchString(digest) {
		return "", "", fmt.Errorf("invalid digest")
	}
	if e = os.MkdirAll(cache, 0700); e != nil {
		return "", digest, e
	}
	file := filepath.Join(cache, digest+".img")
	unlock, e := lockCache(ctx, filepath.Join(cache, digest+".lock"))
	if e != nil {
		return "", digest, e
	}
	defer unlock()
	if info, e := os.Lstat(file); e == nil {
		if !info.Mode().IsRegular() {
			return "", digest, fmt.Errorf("unsafe cache entry")
		}
		if e = verifyFile(file, digest); e == nil {
			return file, digest, nil
		}
	}
	f, e := os.CreateTemp(cache, ".download-")
	if e != nil {
		return "", digest, e
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if e = fetch(ctx, c, raw, f, 20<<30); e != nil {
		return "", digest, e
	}
	if e = f.Sync(); e != nil {
		return "", digest, e
	}
	if e = verifyFile(f.Name(), digest); e != nil {
		return "", digest, e
	}
	if e = os.Rename(f.Name(), file); e != nil {
		return "", digest, e
	}
	return file, digest, nil
}

type imageTransport struct {
	base  http.RoundTripper
	hosts []string
}

func (t imageTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if e := CheckURL(r.URL.String(), t.hosts); e != nil {
		return nil, e
	}
	return t.base.RoundTrip(r)
}
