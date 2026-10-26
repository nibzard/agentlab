// Package guestidentity defines the versioned, unambiguous recovery proof.
package guestidentity

import (
	"crypto/ed25519"
	"encoding/base64"
	"fmt"
	"regexp"
)

var identifier = regexp.MustCompile(`^[a-f0-9]{32,64}$`)

func ValidID(s string) bool { return identifier.MatchString(s) }
func Message(vmid int, incarnation, challenge, nonce, operation, request string) []byte {
	return []byte(fmt.Sprintf("agentlab-guest-recovery-v1\n%d\n%s\n%s\n%s\n%s\n%s\n", vmid, incarnation, challenge, nonce, operation, request))
}
func Verify(public string, message []byte, signature string) bool {
	key, e := base64.StdEncoding.DecodeString(public)
	if e != nil || len(key) != ed25519.PublicKeySize {
		return false
	}
	sig, e := base64.StdEncoding.DecodeString(signature)
	return e == nil && ed25519.Verify(key, message, sig)
}
