package mobilebridge

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"time"
)

// The gateway's TLS identity is a self-signed, long-lived certificate. The
// phone does not trust a CA for it: the pairing code carries the SHA-256 of the
// certificate's public key (SPKI) and the phone accepts only a certificate with
// that key. Pinning the key rather than the certificate keeps the pin valid if
// the certificate is ever re-issued over the same key.
//
// Rotation: replacing the key changes the fingerprint, so every phone must
// re-pair. There is no in-band rotation; delete the two files and restart.
const (
	tlsCertFile = "gateway-cert.pem"
	tlsKeyFile  = "gateway-key.pem"
	tlsValidity = 20 * 365 * 24 * time.Hour
)

// GatewayTLS is the loaded identity.
type GatewayTLS struct {
	Cert tls.Certificate
	// Pin is the lowercase hex SHA-256 of the certificate's SubjectPublicKeyInfo.
	Pin string
	// CreatedAt is when the key pair was first generated; it anchors the
	// plain-http migration window.
	CreatedAt time.Time
}

// TLSDir is where the gateway key pair lives.
func TLSDir(dataDir string) string { return filepath.Join(dataDir, "mobile") }

// SPKIPin returns the pin for a certificate.
func SPKIPin(cert *x509.Certificate) string {
	sum := sha256.Sum256(cert.RawSubjectPublicKeyInfo)
	return hex.EncodeToString(sum[:])
}

// EnsureGatewayTLS loads the gateway key pair from dataDir/mobile, generating
// it on first use. The private key is written 0600 and never logged.
func EnsureGatewayTLS(dataDir string) (*GatewayTLS, error) {
	dir := TLSDir(dataDir)
	certPath, keyPath := filepath.Join(dir, tlsCertFile), filepath.Join(dir, tlsKeyFile)
	if g, err := loadGatewayTLS(certPath, keyPath); err == nil {
		return g, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("load gateway tls: %w", err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create %s: %w", dir, err)
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate key: %w", err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	if err != nil {
		return nil, fmt.Errorf("serial: %w", err)
	}
	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "Tallpine mobile gateway"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(tlsValidity),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, fmt.Errorf("create certificate: %w", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, fmt.Errorf("marshal key: %w", err)
	}
	if err := writeAtomic(keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		return nil, err
	}
	if err := writeAtomic(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		return nil, err
	}
	return loadGatewayTLS(certPath, keyPath)
}

func writeAtomic(path string, data []byte, mode os.FileMode) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return fmt.Errorf("write %s: %w", filepath.Base(path), err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("install %s: %w", filepath.Base(path), err)
	}
	return nil
}

func loadGatewayTLS(certPath, keyPath string) (*GatewayTLS, error) {
	pair, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return nil, err
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return nil, fmt.Errorf("parse certificate: %w", err)
	}
	pair.Leaf = leaf
	return &GatewayTLS{Cert: pair, Pin: SPKIPin(leaf), CreatedAt: leaf.NotBefore.Add(time.Hour)}, nil
}
