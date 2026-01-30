package daemon

import (
	"net"
	"testing"
)

func mustParseCIDR(t *testing.T, value string) *net.IPNet {
	t.Helper()
	_, subnet, err := net.ParseCIDR(value)
	if err != nil {
		t.Fatalf("parse cidr %s: %v", value, err)
	}
	return subnet
}
