package tailcatexperiment

import (
	"context"
	"errors"
	"net"
	"net/netip"

	"github.com/tailscale/tailcat"
	"tailscale.com/tailcfg"
	"tailscale.com/types/key"
)

// Listen starts only a virtual API port. It enables neither subnet forwarding,
// shell access, nor proxy callbacks. An empty peer allowlist is rejected.
func Listen(ctx context.Context, identity key.NodePrivate, preshared tailcat.PresharedKey, allowed []key.NodePublic, region *tailcfg.DERPRegion) (*tailcat.Server, net.Listener, error) {
	if identity.IsZero() || preshared.IsZero() || len(allowed) == 0 || region == nil {
		return nil, nil, errors.New("provide a persistent identity, preshared key, explicit peers, and relay region")
	}
	for _, peer := range allowed {
		if peer.IsZero() {
			return nil, nil, errors.New("provide nonzero approved peer identities")
		}
	}
	server := &tailcat.Server{Key: identity, PresharedKey: preshared, AllowedClients: append([]key.NodePublic(nil), allowed...), Region: region, AllowProxy: func(netip.AddrPort) bool { return false }, Logf: func(string, ...any) {}}
	listener, err := server.Listen(ctx, "tcp", ":8843")
	if err != nil {
		server.Close()
		return nil, nil, errors.New("the selected Tailcat relay could not start")
	}
	return server, listener, nil
}
