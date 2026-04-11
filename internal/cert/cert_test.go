package cert

import (
	"crypto/x509"
	"os"
	"path/filepath"
	"testing"
)

func TestGenerateSelfSigned(t *testing.T) {
	deviceID := "test_device_12345"
	cert, err := GenerateSelfSigned(deviceID)
	if err != nil {
		t.Fatalf("GenerateSelfSigned failed: %v", err)
	}

	if len(cert.Certificate) == 0 {
		t.Fatal("generated cert has no certificate data")
	}

	x509Cert, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		t.Fatalf("failed to parse generated certificate: %v", err)
	}

	if x509Cert.Subject.CommonName != deviceID {
		t.Errorf("expected CN %q, got %q", deviceID, x509Cert.Subject.CommonName)
	}
}

func TestFingerprint(t *testing.T) {
	cert, err := GenerateSelfSigned("fingerprint_test_device")
	if err != nil {
		t.Fatal(err)
	}

	x509Cert, _ := x509.ParseCertificate(cert.Certificate[0])
	fp := Fingerprint(x509Cert)

	if len(fp) != 64 {
		t.Errorf("expected SHA256 hex fingerprint to be 64 chars, got %d", len(fp))
	}

	// Deterministic
	fp2 := Fingerprint(x509Cert)
	if fp != fp2 {
		t.Error("fingerprint is not deterministic")
	}
}

func TestLoadOrGenerate(t *testing.T) {
	dir := t.TempDir()
	certPath := filepath.Join(dir, "cert.pem")
	keyPath := filepath.Join(dir, "key.pem")
	deviceID := "load_or_generate_test"

	// First call should generate
	cert1, err := LoadOrGenerate(certPath, keyPath, deviceID)
	if err != nil {
		t.Fatalf("first LoadOrGenerate failed: %v", err)
	}

	if _, err := os.Stat(certPath); os.IsNotExist(err) {
		t.Error("cert file was not generated")
	}

	// Second call should load
	cert2, err := LoadOrGenerate(certPath, keyPath, deviceID)
	if err != nil {
		t.Fatalf("second LoadOrGenerate failed: %v", err)
	}

	x509Cert1, _ := x509.ParseCertificate(cert1.Certificate[0])
	x509Cert2, _ := x509.ParseCertificate(cert2.Certificate[0])

	if Fingerprint(x509Cert1) != Fingerprint(x509Cert2) {
		t.Error("loaded certificate mismatch with generated one")
	}
}
