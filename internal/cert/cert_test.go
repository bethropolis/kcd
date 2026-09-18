package cert

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
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

func TestPinnedFingerprint(t *testing.T) {
	if got := PinnedFingerprint(nil); got != "" {
		t.Errorf("expected empty fingerprint for nil cert, got %q", got)
	}

	c, err := GenerateSelfSigned("pinned_fp_test")
	if err != nil {
		t.Fatal(err)
	}
	x509Cert, _ := x509.ParseCertificate(c.Certificate[0])
	if got := PinnedFingerprint(x509Cert); got != Fingerprint(x509Cert) {
		t.Error("PinnedFingerprint mismatch with Fingerprint")
	}
}

func TestVerifySideChannelPeer(t *testing.T) {
	c, err := GenerateSelfSigned("verify_peer_test")
	if err != nil {
		t.Fatal(err)
	}
	x509Cert, _ := x509.ParseCertificate(c.Certificate[0])
	fp := Fingerprint(x509Cert)

	other, err := GenerateSelfSigned("verify_peer_other")
	if err != nil {
		t.Fatal(err)
	}
	otherCert, _ := x509.ParseCertificate(other.Certificate[0])

	state := tls.ConnectionState{PeerCertificates: []*x509.Certificate{x509Cert}}

	if err := VerifySideChannelPeer(state, fp); err != nil {
		t.Errorf("matching fingerprint rejected: %v", err)
	}
	if err := VerifySideChannelPeer(state, Fingerprint(otherCert)); err == nil {
		t.Error("mismatched fingerprint accepted")
	}
	if err := VerifySideChannelPeer(state, ""); err != nil {
		t.Errorf("empty expected fingerprint should skip verification: %v", err)
	}
	if err := VerifySideChannelPeer(tls.ConnectionState{}, fp); err == nil {
		t.Error("missing peer certificate accepted")
	}
}

func TestVerificationKeyMatchesReference(t *testing.T) {
	// Fixed public-key DERs with an independently computed reference code
	// (larger DER first + ASCII decimal timestamp, SHA-256, first 8 hex
	// chars uppercased — the algorithm every stock client implements).
	const (
		derA      = "30820122300d06092a864886f70d01010105000382010f003082010a0282010100d0c0cb8bcf06d5f3ca9ba5d529d112764d79a7d0eeef2fb7522e027982b10dc3d5253961153e6a707032c3272818fe4e699f2d4d9289806a81f7613eeba87092a39206d756ed5317f47c565314b6b24a62d9d009fb8d9af1fccd1f387bb9f6cb8a6840cad05791310e25df77042121f8969e63b60381d722fa01d6cfb27a0e9b8716010b0da6037dacf5e7eb9f3b4f366d4dee37d976c76975f68751ae4677ed3aac6b3b5c885af7a65740c604abc17daf4fb260a35fcac7d9eff525a8febafb6c4239f6bc2f5655caeb1b979dd363a513b51d698b6b93c5396bfc6a9968aac07733d895e94da23080759822af22e54f778cb0f237ba6f12148d54d3d73819cf0203010001"
		derB      = "30820122300d06092a864886f70d01010105000382010f003082010a0282010100f7a42d36379fb98d5032b639a4f4d9d3360797831c70de6317f96f5c6bcc3325dd129d5816f7bbdff608ba71ae5c2a2de1516075d1129e34a6a1fa7558aeecdeceb5202e48863fa3453fa508891f5d21ca75d42120b82579102ccb250e55a7184bd8c50848aef917022b42a3d95403155c66d8b29996fbdd37444733347cbc33138112b6d7d993a965b955026a90f9caaaeacb5e519bcd2cecf38b012962af5bbe67b6c5b5ff203364f58ce39da6acfce86a07228c18d0280944677388f211e68a10938afeb0204d4545b031800373a86a9a7c5989ecf81fb5b59f3a6d68a7a781882f488fd0dab498383b86ce7fd85fbdc79a7bb71b5f606b6b6b86bea1fd910203010001"
		timestamp = 1711234567
		want      = "698179EF"
		wantNoTS  = "D7B8B287"
	)
	mustPub := func(hexDER string) *x509.Certificate {
		t.Helper()
		raw, err := hex.DecodeString(hexDER)
		if err != nil {
			t.Fatalf("decode DER: %v", err)
		}
		pub, err := x509.ParsePKIXPublicKey(raw)
		if err != nil {
			t.Fatalf("parse public key: %v", err)
		}
		return &x509.Certificate{PublicKey: pub}
	}
	certA, certB := mustPub(derA), mustPub(derB)

	// Order-independent: both sides display the same code.
	if got := VerificationKey(certA, certB, timestamp); got != want {
		t.Errorf("VerificationKey(a, b) = %q, want %q", got, want)
	}
	if got := VerificationKey(certB, certA, timestamp); got != want {
		t.Errorf("VerificationKey(b, a) = %q, want %q", got, want)
	}
	// Non-positive timestamp hashes without it (pre-v8 peers).
	if got := VerificationKey(certA, certB, 0); got != wantNoTS {
		t.Errorf("VerificationKey no-timestamp = %q, want %q", got, wantNoTS)
	}
}
