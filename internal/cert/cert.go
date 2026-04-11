// Package cert handles TLS certificate generation and fingerprinting for KDE Connect.
// KDE Connect uses self-signed certificates with long validity periods.
// Identity is established by comparing the SHA256 fingerprint of the certificate.
// The certificate's Common Name (CN) MUST match the device ID.
package cert

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"time"
)

// GenerateSelfSigned creates a new 2048-bit RSA key and a self-signed X.509
// certificate valid for 10 years, which matches the KDE Connect reference implementation.
// The deviceID is used as the certificate's Common Name (CN), which KDE Connect
// verifies matches the deviceId in identity packets.
func GenerateSelfSigned(deviceID string) (*tls.Certificate, error) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, fmt.Errorf("cert: generate RSA key: %w", err)
	}

	// KDE Connect uses -1 year to +10 years validity
	notBefore := time.Now().Add(-365 * 24 * time.Hour)
	notAfter := notBefore.Add(11 * 365 * 24 * time.Hour)

	serialNumberLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serialNumber, err := rand.Int(rand.Reader, serialNumberLimit)
	if err != nil {
		return nil, fmt.Errorf("cert: generate serial number: %w", err)
	}

	template := x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			CommonName:         deviceID,
			Organization:       []string{"KDE"},
			OrganizationalUnit: []string{"KDE Connect"},
		},
		NotBefore:             notBefore,
		NotAfter:              notAfter,
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
		IsCA:                  true, // KDE Connect requires self-signed certs to act as their own CA
	}

	derBytes, err := x509.CreateCertificate(rand.Reader, &template, &template, &priv.PublicKey, priv)
	if err != nil {
		return nil, fmt.Errorf("cert: create certificate: %w", err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: derBytes})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(priv)})

	tlsCert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, fmt.Errorf("cert: load key pair: %w", err)
	}

	return &tlsCert, nil
}

// LoadOrGenerate tries to load a TLS certificate from the given paths.
// If the files do not exist, it generates a new self-signed certificate
// with the given deviceID as Common Name and writes it to disk.
func LoadOrGenerate(certFile, keyFile, deviceID string) (*tls.Certificate, error) {
	tlsCert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err == nil {
		return &tlsCert, nil
	}

	if !os.IsNotExist(err) {
		return nil, fmt.Errorf("cert: load existing %s: %w", certFile, err)
	}

	// Generate new
	newCert, err := GenerateSelfSigned(deviceID)
	if err != nil {
		return nil, err
	}

	// Need to write the raw bytes to disk
	certBlock := &pem.Block{Type: "CERTIFICATE", Bytes: newCert.Certificate[0]}
	keyBytes, err := x509.MarshalPKCS8PrivateKey(newCert.PrivateKey)
	if err != nil {
		return nil, fmt.Errorf("cert: marshal private key: %w", err)
	}
	keyBlock := &pem.Block{Type: "PRIVATE KEY", Bytes: keyBytes}

	// Create directory if needed
	certDir := certFile[:len(certFile)-len("/cert.pem")]
	if idx := len(certFile) - 1; idx > 0 {
		for i := idx; i >= 0; i-- {
			if certFile[i] == '/' {
				certDir = certFile[:i]
				break
			}
		}
	}
	if err := os.MkdirAll(certDir, 0700); err != nil {
		return nil, fmt.Errorf("cert: create dir: %w", err)
	}

	if err := os.WriteFile(certFile, pem.EncodeToMemory(certBlock), 0600); err != nil {
		return nil, fmt.Errorf("cert: write cert file: %w", err)
	}
	if err := os.WriteFile(keyFile, pem.EncodeToMemory(keyBlock), 0600); err != nil {
		return nil, fmt.Errorf("cert: write key file: %w", err)
	}

	return newCert, nil
}

// Fingerprint returns the SHA256 hex string of the given certificate.
// This is used for verifying device identity after pairing.
func Fingerprint(cert *x509.Certificate) string {
	hash := sha256.Sum256(cert.Raw)
	return hex.EncodeToString(hash[:])
}

// VerificationKey generates a verification fingerprint used for out-of-band pairing verification.
// It creates a SHA256 hash of the concatenated public keys to display a fingerprint.
func VerificationKey(localCert, remoteCert *x509.Certificate) string {
	var localKey, remoteKey []byte
	if localCert != nil && localCert.PublicKey != nil {
		localKey, _ = x509.MarshalPKIXPublicKey(localCert.PublicKey)
	}
	if remoteCert != nil && remoteCert.PublicKey != nil {
		remoteKey, _ = x509.MarshalPKIXPublicKey(remoteCert.PublicKey)
	}

	var combined []byte
	if string(localKey) < string(remoteKey) {
		combined = append(localKey, remoteKey...)
	} else {
		combined = append(remoteKey, localKey...)
	}

	hash := sha256.Sum256(combined)
	return hex.EncodeToString(hash[:])
}

// TLSConfig returns the standard tls.Config used for KDE Connect.
func TLSConfig(cert *tls.Certificate) *tls.Config {
	return &tls.Config{
		Certificates: []tls.Certificate{*cert},
		// We use self-signed certs and custom fingerprint verification.
		InsecureSkipVerify: true,
		// Require clients to present their cert for verification against our stored pairing list.
		ClientAuth: tls.RequireAnyClientCert,
	}
}
