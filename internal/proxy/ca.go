package proxy

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"time"
)

const (
	caCertFile = "ca.crt"
	caKeyFile  = "ca.key"
)

// CA manages a built-in certificate authority for self-hosted TLS.
//
// The CA is used to issue certificates for .local domains and other
// private domains where Let's Encrypt cannot provision certificates.
// The CA certificate should be trusted by clients that need to access
// sandbox subdomains.
type CA struct {
	CertPath string
	KeyPath  string
	cert     *x509.Certificate
	key      *ecdsa.PrivateKey
}

// LoadOrGenerateCA loads an existing CA or generates a new one.
//
// If both the certificate and key files exist, they are loaded.
// Otherwise, a new CA is generated and written to disk.
func LoadOrGenerateCA(dir string) (*CA, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create CA directory: %w", err)
	}

	certPath := filepath.Join(dir, caCertFile)
	keyPath := filepath.Join(dir, caKeyFile)

	ca := &CA{
		CertPath: certPath,
		KeyPath:  keyPath,
	}

	// Try loading existing CA
	if _, err := os.Stat(certPath); err == nil {
		if _, err := os.Stat(keyPath); err == nil {
			if err := ca.load(); err == nil {
				return ca, nil
			}
		}
	}

	// Generate new CA
	if err := ca.generate(); err != nil {
		return nil, err
	}

	return ca, nil
}

// generate creates a new self-signed CA certificate and key.
func (ca *CA) generate() error {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return fmt.Errorf("generate CA key: %w", err)
	}

	serialNumber, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return fmt.Errorf("generate serial: %w", err)
	}

	template := &x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			Organization:       []string{"AgentLab"},
			CommonName:         "AgentLab CA",
			OrganizationalUnit: []string{"Self-hosted TLS"},
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(10 * 365 * 24 * time.Hour), // 10 years
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLen:            0,
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return fmt.Errorf("create CA certificate: %w", err)
	}

	cert, err := x509.ParseCertificate(certDER)
	if err != nil {
		return fmt.Errorf("parse CA certificate: %w", err)
	}

	// Write certificate
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	if err := os.WriteFile(ca.CertPath, certPEM, 0o644); err != nil {
		return fmt.Errorf("write CA certificate: %w", err)
	}

	// Write key
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return fmt.Errorf("marshal CA key: %w", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	if err := os.WriteFile(ca.KeyPath, keyPEM, 0o600); err != nil {
		return fmt.Errorf("write CA key: %w", err)
	}

	ca.cert = cert
	ca.key = key
	return nil
}

// load reads existing CA certificate and key from disk.
func (ca *CA) load() error {
	certPEM, err := os.ReadFile(ca.CertPath)
	if err != nil {
		return fmt.Errorf("read CA certificate: %w", err)
	}

	block, _ := pem.Decode(certPEM)
	if block == nil {
		return fmt.Errorf("decode CA certificate PEM")
	}

	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return fmt.Errorf("parse CA certificate: %w", err)
	}

	keyPEM, err := os.ReadFile(ca.KeyPath)
	if err != nil {
		return fmt.Errorf("read CA key: %w", err)
	}

	keyBlock, _ := pem.Decode(keyPEM)
	if keyBlock == nil {
		return fmt.Errorf("decode CA key PEM")
	}

	key, err := x509.ParseECPrivateKey(keyBlock.Bytes)
	if err != nil {
		return fmt.Errorf("parse CA key: %w", err)
	}

	ca.cert = cert
	ca.key = key
	return nil
}

// IssueCert creates a TLS certificate for the given domain(s).
//
// Returns the TLS certificate in PEM format (cert + key concatenated).
func (ca *CA) IssueCert(domain string) ([]byte, error) {
	if ca.cert == nil || ca.key == nil {
		return nil, fmt.Errorf("CA not initialized")
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate leaf key: %w", err)
	}

	serialNumber, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, fmt.Errorf("generate serial: %w", err)
	}

	template := &x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			Organization: []string{"AgentLab"},
			CommonName:   domain,
		},
		NotBefore:   time.Now(),
		NotAfter:    time.Now().Add(365 * 24 * time.Hour), // 1 year
		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:    []string{domain},
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, ca.cert, &key.PublicKey, ca.key)
	if err != nil {
		return nil, fmt.Errorf("issue certificate: %w", err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})

	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, fmt.Errorf("marshal leaf key: %w", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})

	return append(certPEM, keyPEM...), nil
}

// CACertPEM returns the CA certificate in PEM format.
func (ca *CA) CACertPEM() ([]byte, error) {
	if ca.cert == nil {
		return nil, fmt.Errorf("CA not initialized")
	}
	certPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: ca.cert.Raw,
	})
	return certPEM, nil
}
