// Package protocol defines shared discovery contracts for AgentLab clients.
package protocol

// Capabilities advertises implemented server behavior. Clients must not infer
// support for a feature that is missing or false.
type Capabilities struct {
	Version            string          `json:"version"`
	ProtocolVersion    int             `json:"protocol_version"`
	APISchemaVersion   int             `json:"api_schema_version"`
	EventSchemaVersion int             `json:"event_schema_version"`
	ServerIdentity     string          `json:"server_identity,omitempty"`
	Features           map[string]bool `json:"features"`
	Authorization      Authorization   `json:"authorization"`
}

type Authorization struct {
	Method    string   `json:"method"`
	Issuer    string   `json:"issuer,omitempty"`
	Subject   string   `json:"subject,omitempty"`
	Commands  []string `json:"commands"`
	Scope     []string `json:"scope"`
	ExpiresAt int64    `json:"expires_at,omitempty"`
}

// Compatible returns whether this client understands the advertised API.
func (c Capabilities) Compatible() bool { return c.ProtocolVersion == 1 && c.APISchemaVersion == 1 }
