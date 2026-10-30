// Package hostidentity derives an AgentLab identity without exposing machine-id.
package hostidentity

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
)

// RequestHeader binds a remote API request to the verified server identity.
const RequestHeader = "X-AgentLab-Server-Identity"

func FromMachineID(raw string) (string, error) {
	id := strings.TrimSpace(raw)
	if len(id) != 32 || strings.Trim(id, "0123456789abcdef") != "" {
		return "", fmt.Errorf("invalid server machine identity")
	}
	hash := sha256.Sum256([]byte("agentlab-server-identity-v1:" + id))
	return "server_" + hex.EncodeToString(hash[:]), nil
}

func Local() (string, error) {
	data, err := os.ReadFile("/etc/machine-id")
	if err != nil {
		return "", err
	}
	return FromMachineID(string(data))
}
