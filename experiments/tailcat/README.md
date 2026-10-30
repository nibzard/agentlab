# Optional Tailcat prototype

This isolated module evaluates Tailcat v0.7.0. It is not part of normal builds,
installation, or release archives. The daemon's `tailcat` capability remains false.
The prototype uses Go 1.27.1. The root module remains on Go 1.26.10.

Run its explicit check from the repository root:

```sh
make tailcat-prototype-check
```

The check can download the pinned Go toolchain and module dependencies. It starts
its own loopback relay and STUN service. It does not use public relays or configure
a user's server. The policy permits one fixed loopback TCP API, preserves the
caller's bearer token, and pins the expected server identity. It rejects other
HTTP paths and does not enable a shell, trusted Unix socket, or subnet forwarding.

This is an incomplete experiment. The current package provides forwarding policy
and an explicit-peer virtual listener. Its private identity store preserves node and preshared keys across reopening.
It rejects mismatched roles, unsafe permissions, symlinks, and invalid records.
Public status formatting omits secrets. It does not provide a supervised helper
command, secure pairing, rotation, process restart recovery, or the
required transport comparison. These remain required before an adoption decision.
A local relay check does not prove public relay, NAT traversal, or native Mac
behavior. Check the main experience plan and the research notes for open gates.
