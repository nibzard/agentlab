package daemon

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

// A dedicated listener lets old HTTP guests keep working. Only newly provisioned
// descriptors receive this pinned certificate. Never fall back to HTTP.
func guestTLS(dir, listen string) (net.Listener, string, error) {
	host, _, e := net.SplitHostPort(listen)
	if e != nil {
		return nil, "", e
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		return nil, "", errors.New("guest recovery listener requires a concrete advertised host")
	}
	if e = os.MkdirAll(dir, 0700); e != nil {
		return nil, "", e
	}
	certPath, keyPath := filepath.Join(dir, "server.crt"), filepath.Join(dir, "server.key")
	certPEM, e := os.ReadFile(certPath)
	if errors.Is(e, os.ErrNotExist) {
		// Refuse to silently replace trust if only one half of the pair was lost.
		if _, e = os.Stat(keyPath); !errors.Is(e, os.ErrNotExist) {
			return nil, "", errors.New("guest TLS certificate missing; restore matching certificate and key")
		}
		key, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if e != nil {
			return nil, "", e
		}
		serial, e := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
		if e != nil {
			return nil, "", e
		}
		template := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: "AgentLab guest controller"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().AddDate(5, 0, 0), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
		if ip := net.ParseIP(host); ip != nil {
			template.IPAddresses = []net.IP{ip}
		} else {
			template.DNSNames = []string{host}
		}
		der, e := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
		if e != nil {
			return nil, "", e
		}
		raw, e := x509.MarshalPKCS8PrivateKey(key)
		if e != nil {
			return nil, "", e
		}
		if e = os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: raw}), 0600); e != nil {
			return nil, "", e
		}
		certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
		if e = os.WriteFile(certPath, certPEM, 0600); e != nil {
			return nil, "", e
		}
	} else if e != nil {
		return nil, "", e
	}
	pair, e := tls.LoadX509KeyPair(certPath, keyPath)
	if e != nil {
		return nil, "", e
	}
	certificate, e := x509.ParseCertificate(pair.Certificate[0])
	if e != nil {
		return nil, "", e
	}
	if e = certificate.VerifyHostname(host); e != nil {
		return nil, "", errors.New("guest TLS certificate does not match configured host; restore matching trust or reprovision guests")
	}
	if time.Now().Before(certificate.NotBefore) || time.Now().After(certificate.NotAfter) {
		return nil, "", errors.New("guest TLS certificate outside validity period; perform an explicit guest trust migration")
	}
	ln, e := tls.Listen("tcp", listen, &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{pair}})
	return ln, string(certPEM), e
}
