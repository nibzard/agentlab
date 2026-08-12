package auth

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

const (
	// tokenPrefix distinguishes agentlab tokens from generic bearer tokens.
	tokenPrefix = "agentlab."

	// tokenType is the type header value for agentlab tokens.
	tokenType = "agentlab-token"

	// defaultTokenTTL is the default token lifetime if no TTL is specified.
	defaultTokenTTL = 1 * time.Hour
)

var (
	// ErrInvalidTokenFormat indicates the token string is malformed.
	ErrInvalidTokenFormat = errors.New("invalid token format")

	// ErrTokenExpired indicates the token has expired.
	ErrTokenExpired = errors.New("token expired")

	// ErrTokenNotYetValid indicates the token is not yet valid (nbf check).
	ErrTokenNotYetValid = errors.New("token not yet valid")

	// ErrInvalidSignature indicates the token signature does not match.
	ErrInvalidSignature = errors.New("invalid token signature")

	// ErrEmptyCommands indicates a token was requested with no allowed commands.
	ErrEmptyCommands = errors.New("at least one command is required")
)

// TokenHeader is the JWT-like header for agentlab tokens.
type TokenHeader struct {
	Algorithm string `json:"alg"` // SSH key type: "ssh-ed25519", "ssh-rsa", etc.
	Type      string `json:"typ"` // Always "agentlab-token"
}

// TokenClaims represents the permissions and identity embedded in an API token.
type TokenClaims struct {
	// Issuer is the SSH key fingerprint that signed this token.
	Issuer string `json:"iss"`

	// Subject is an optional human-readable label for the token.
	Subject string `json:"sub,omitempty"`

	// Commands lists the CLI commands this token is allowed to execute.
	// Each entry is a command prefix like "sandbox.list", "sandbox.show", "job.run".
	// A single "*" means all commands are allowed.
	Commands []string `json:"cmds"`

	// Scope limits the token to specific sandboxes.
	// Each entry is "sandbox:<vmid>". Empty means all sandboxes.
	Scope []string `json:"scope,omitempty"`

	// ExpiresAt is the Unix timestamp when the token expires.
	ExpiresAt int64 `json:"exp"`

	// NotBefore is the Unix timestamp when the token becomes valid.
	NotBefore int64 `json:"nbf,omitempty"`

	// TokenID is a unique identifier for revocation support.
	TokenID string `json:"jti,omitempty"`
}

// Token is a parsed and verified API token.
type Token struct {
	Header  TokenHeader
	Claims  TokenClaims
	Raw     string // The original encoded token string
	Signer  ssh.PublicKey
}

// TokenCreateRequest holds the parameters for creating a new token.
type TokenCreateRequest struct {
	// Commands is the list of allowed command prefixes (required).
	// Use ["*"] for all commands.
	Commands []string

	// Scope limits the token to specific sandboxes (optional).
	// Format: "sandbox:<vmid>"
	Scope []string

	// TTL is the token lifetime. Defaults to 1 hour if zero.
	TTL time.Duration

	// Subject is an optional human-readable label.
	Subject string
}

// CreateToken signs a new API token using an SSH private key.
//
// The token format is: agentlab.<base64url(header)>.<base64url(payload)>.<base64url(signature)>
// where the signature is created by the SSH key over the "header.payload" data.
func CreateToken(signer ssh.Signer, req TokenCreateRequest) (string, error) {
	if signer == nil {
		return "", errors.New("signer is required")
	}
	if len(req.Commands) == 0 {
		return "", ErrEmptyCommands
	}

	ttl := req.TTL
	if ttl == 0 {
		ttl = defaultTokenTTL
	}

	now := time.Now()
	pubKey := signer.PublicKey()
	tokenID := generateTokenID()

	header := TokenHeader{
		Algorithm: pubKey.Type(),
		Type:      tokenType,
	}

	claims := TokenClaims{
		Issuer:    FingerprintForPublicKey(pubKey),
		Subject:   req.Subject,
		Commands:  req.Commands,
		Scope:     req.Scope,
		ExpiresAt: now.Add(ttl).Unix(),
		NotBefore: now.Unix(),
		TokenID:   tokenID,
	}

	headerB64, err := encodePart(header)
	if err != nil {
		return "", fmt.Errorf("encode header: %w", err)
	}
	claimsB64, err := encodePart(claims)
	if err != nil {
		return "", fmt.Errorf("encode claims: %w", err)
	}

	signingInput := headerB64 + "." + claimsB64

	// Sign using the SSH key. The Sign method on ssh.Signer produces an
	// ssh.Signature that includes the algorithm and the raw signature bytes.
	sig, err := signer.Sign(nil, []byte(signingInput))
	if err != nil {
		return "", fmt.Errorf("sign token: %w", err)
	}

	sigBytes, err := encodeSignature(sig)
	if err != nil {
		return "", fmt.Errorf("encode signature: %w", err)
	}

	return tokenPrefix + signingInput + "." + sigBytes, nil
}

// ParseToken parses and verifies an agentlab API token string.
//
// It returns the parsed Token if the signature is valid and the token is
// within its validity period. The caller should additionally check scope
// and command permissions.
func ParseToken(tokenStr string, keyStore *KeyStore) (*Token, error) {
	if !strings.HasPrefix(tokenStr, tokenPrefix) {
		return nil, ErrInvalidTokenFormat
	}
	tokenStr = strings.TrimPrefix(tokenStr, tokenPrefix)

	parts := strings.SplitN(tokenStr, ".", 3)
	if len(parts) != 3 {
		return nil, ErrInvalidTokenFormat
	}

	var header TokenHeader
	if err := decodePart(parts[0], &header); err != nil {
		return nil, fmt.Errorf("%w: invalid header", ErrInvalidTokenFormat)
	}
	if header.Type != tokenType {
		return nil, fmt.Errorf("%w: unsupported token type %q", ErrInvalidTokenFormat, header.Type)
	}

	var claims TokenClaims
	if err := decodePart(parts[1], &claims); err != nil {
		return nil, fmt.Errorf("%w: invalid claims", ErrInvalidTokenFormat)
	}

	// Look up the signing key by issuer fingerprint.
	if keyStore == nil {
		return nil, errors.New("key store is required for verification")
	}
	identity := keyStore.Lookup(claims.Issuer)
	if identity == nil {
		return nil, fmt.Errorf("%w: unknown issuer %s", ErrInvalidSignature, claims.Issuer)
	}

	// Verify signature.
	signingInput := parts[0] + "." + parts[1]
	sig, err := decodeSignature(parts[2], header.Algorithm)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidSignature, err)
	}
	if err := identity.PublicKey.Verify([]byte(signingInput), sig); err != nil {
		return nil, ErrInvalidSignature
	}

	// Check time validity.
	now := time.Now().Unix()
	if claims.ExpiresAt > 0 && now > claims.ExpiresAt {
		return nil, ErrTokenExpired
	}
	if claims.NotBefore > 0 && now < claims.NotBefore {
		return nil, ErrTokenNotYetValid
	}

	return &Token{
		Header:  header,
		Claims:  claims,
		Raw:     tokenPrefix + tokenStr,
		Signer:  identity.PublicKey,
	}, nil
}

// IsCommandAllowed checks whether the token allows a specific command.
//
// Matching is exact or dot-boundary only: "sandbox" matches "sandbox" and
// "sandbox.list", but NOT "sandboxsecret"; "sandbox.list" matches "sandbox.list"
// but not "sandbox.listsecret". This avoids the raw-prefix ambiguity where an
// allowed "sandbox.list" would also grant "sandbox.listprivate". "*" matches
// everything.
func (t *Token) IsCommandAllowed(command string) bool {
	if command == "" {
		return false
	}
	if len(t.Claims.Commands) == 0 {
		return false
	}
	for _, allowed := range t.Claims.Commands {
		if allowed == "*" {
			return true
		}
		if allowed == command {
			return true
		}
		// Dot-boundary namespace: "sandbox" matches "sandbox.list".
		if strings.HasPrefix(command, allowed+".") {
			return true
		}
	}
	return false
}

// IsSandboxAllowed checks whether the token allows access to a specific sandbox.
// If the token has no scope restrictions, all sandboxes are allowed.
func (t *Token) IsSandboxAllowed(vmid int) bool {
	if len(t.Claims.Scope) == 0 {
		return true
	}
	target := fmt.Sprintf("sandbox:%d", vmid)
	for _, s := range t.Claims.Scope {
		if s == target || s == "*" {
			return true
		}
	}
	return false
}

// IsFullAccess reports whether the token grants unrestricted command and sandbox
// access: its Commands claim contains "*" and its Scope is empty. Such a token
// is unconstrained by per-route authorization.
func (t *Token) IsFullAccess() bool {
	if len(t.Claims.Scope) > 0 {
		return false
	}
	for _, c := range t.Claims.Commands {
		if c == "*" {
			return true
		}
	}
	return false
}

// IsExpired returns true if the token has expired.
func (t *Token) IsExpired() bool {
	if t.Claims.ExpiresAt <= 0 {
		return false
	}
	return time.Now().Unix() > t.Claims.ExpiresAt
}

// --- encoding helpers ---

func encodePart(v any) (string, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

func decodePart(s string, v any) error {
	data, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}

// encodeSignature encodes an ssh.Signature into a compact base64url string.
// Format: base64url(ssh.Marshal(sig))
func encodeSignature(sig *ssh.Signature) (string, error) {
	data := ssh.Marshal(sig)
	return base64.RawURLEncoding.EncodeToString(data), nil
}

// decodeSignature reconstructs an ssh.Signature from its encoded form.
func decodeSignature(s string, expectedAlg string) (*ssh.Signature, error) {
	data, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return nil, err
	}
	var sig ssh.Signature
	if err := ssh.Unmarshal(data, &sig); err != nil {
		return nil, err
	}
	return &sig, nil
}

func generateTokenID() string {
	var buf [16]byte
	_, _ = rand.Read(buf[:])
	return base64.RawURLEncoding.EncodeToString(buf[:])
}

// ParseTokenUnverified parses a token without verifying the signature.
// This is useful for inspecting token claims (e.g., in token list commands).
// The caller MUST NOT use the result for authorization decisions.
func ParseTokenUnverified(tokenStr string) (*Token, error) {
	if !strings.HasPrefix(tokenStr, tokenPrefix) {
		return nil, ErrInvalidTokenFormat
	}
	tokenStr = strings.TrimPrefix(tokenStr, tokenPrefix)

	parts := strings.SplitN(tokenStr, ".", 3)
	if len(parts) != 3 {
		return nil, ErrInvalidTokenFormat
	}

	var header TokenHeader
	if err := decodePart(parts[0], &header); err != nil {
		return nil, fmt.Errorf("%w: invalid header", ErrInvalidTokenFormat)
	}

	var claims TokenClaims
	if err := decodePart(parts[1], &claims); err != nil {
		return nil, fmt.Errorf("%w: invalid claims", ErrInvalidTokenFormat)
	}

	return &Token{
		Header: header,
		Claims: claims,
		Raw:    tokenPrefix + tokenStr,
	}, nil
}
