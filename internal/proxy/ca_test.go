package proxy

import (
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadOrGenerateCA_New(t *testing.T) {
	dir := t.TempDir()
	ca, err := LoadOrGenerateCA(dir)
	if err != nil {
		t.Fatalf("LoadOrGenerateCA: %v", err)
	}

	if ca.cert == nil {
		t.Fatal("CA certificate is nil")
	}
	if ca.key == nil {
		t.Fatal("CA key is nil")
	}

	// Check files exist
	if _, err := os.Stat(ca.CertPath); err != nil {
		t.Fatalf("cert file: %v", err)
	}
	if _, err := os.Stat(ca.KeyPath); err != nil {
		t.Fatalf("key file: %v", err)
	}

	// Verify cert is a CA
	if !ca.cert.IsCA {
		t.Error("certificate is not a CA")
	}
	if ca.cert.Subject.CommonName != "AgentLab CA" {
		t.Errorf("CA CN = %q, want 'AgentLab CA'", ca.cert.Subject.CommonName)
	}
}

func TestLoadOrGenerateCA_Existing(t *testing.T) {
	dir := t.TempDir()

	// Generate first
	ca1, err := LoadOrGenerateCA(dir)
	if err != nil {
		t.Fatalf("first LoadOrGenerateCA: %v", err)
	}

	// Load existing
	ca2, err := LoadOrGenerateCA(dir)
	if err != nil {
		t.Fatalf("second LoadOrGenerateCA: %v", err)
	}

	// Should have the same serial number
	if ca1.cert.SerialNumber.Cmp(ca2.cert.SerialNumber) != 0 {
		t.Error("loaded CA has different serial than generated")
	}
}

func TestIssueCert(t *testing.T) {
	dir := t.TempDir()
	ca, err := LoadOrGenerateCA(dir)
	if err != nil {
		t.Fatalf("LoadOrGenerateCA: %v", err)
	}

	certPEM, err := ca.IssueCert("mybox.agentlab.local")
	if err != nil {
		t.Fatalf("IssueCert: %v", err)
	}

	if len(certPEM) == 0 {
		t.Fatal("issued cert PEM is empty")
	}

	// Parse the certificate
	block, rest := pem.Decode(certPEM)
	if block == nil {
		t.Fatal("failed to decode cert PEM")
	}
	if block.Type != "CERTIFICATE" {
		t.Errorf("first PEM block type = %q, want CERTIFICATE", block.Type)
	}

	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("parse issued cert: %v", err)
	}

	// Verify domain
	found := false
	for _, name := range cert.DNSNames {
		if name == "mybox.agentlab.local" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("DNS names = %v, want mybox.agentlab.local", cert.DNSNames)
	}

	// Verify it was signed by our CA
	if err := cert.CheckSignatureFrom(ca.cert); err != nil {
		t.Fatalf("cert not signed by CA: %v", err)
	}

	// Should also have the key
	keyBlock, _ := pem.Decode(rest)
	if keyBlock == nil {
		t.Fatal("no key PEM block found")
	}
	if keyBlock.Type != "EC PRIVATE KEY" {
		t.Errorf("key PEM type = %q, want EC PRIVATE KEY", keyBlock.Type)
	}
}

func TestCACertPEM(t *testing.T) {
	dir := t.TempDir()
	ca, err := LoadOrGenerateCA(dir)
	if err != nil {
		t.Fatalf("LoadOrGenerateCA: %v", err)
	}

	pemData, err := ca.CACertPEM()
	if err != nil {
		t.Fatalf("CACertPEM: %v", err)
	}

	block, _ := pem.Decode(pemData)
	if block == nil {
		t.Fatal("failed to decode CA cert PEM")
	}

	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("parse CA cert: %v", err)
	}

	if !cert.IsCA {
		t.Error("CA cert is not a CA")
	}
}

func TestLoadOrGenerateCA_CreatesDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "ca")
	ca, err := LoadOrGenerateCA(dir)
	if err != nil {
		t.Fatalf("LoadOrGenerateCA with nested dir: %v", err)
	}
	if ca.cert == nil {
		t.Fatal("CA not initialized")
	}
}
