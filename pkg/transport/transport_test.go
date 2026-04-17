package transport_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gaarutyunov/mcp-anything/pkg/config"
	pkgtransport "github.com/gaarutyunov/mcp-anything/pkg/transport"
)

func TestBuild_Defaults(t *testing.T) {
	t.Parallel()
	b := pkgtransport.NewBuilder()
	tr, err := b.Build(config.TransportSpec{})
	if err != nil {
		t.Fatalf("Build with empty config: %v", err)
	}
	if tr.MaxIdleConns != 100 {
		t.Errorf("MaxIdleConns: got %d, want 100", tr.MaxIdleConns)
	}
	if tr.MaxIdleConnsPerHost != 10 {
		t.Errorf("MaxIdleConnsPerHost: got %d, want 10", tr.MaxIdleConnsPerHost)
	}
	if tr.IdleConnTimeout != 90*time.Second {
		t.Errorf("IdleConnTimeout: got %v, want 90s", tr.IdleConnTimeout)
	}
}

func TestBuild_ExplicitPooling(t *testing.T) {
	t.Parallel()
	b := pkgtransport.NewBuilder()
	tr, err := b.Build(config.TransportSpec{
		MaxIdleConns:        200,
		MaxIdleConnsPerHost: 20,
		IdleConnTimeout:     60 * time.Second,
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if tr.MaxIdleConns != 200 {
		t.Errorf("MaxIdleConns: got %d, want 200", tr.MaxIdleConns)
	}
	if tr.MaxIdleConnsPerHost != 20 {
		t.Errorf("MaxIdleConnsPerHost: got %d, want 20", tr.MaxIdleConnsPerHost)
	}
	if tr.IdleConnTimeout != 60*time.Second {
		t.Errorf("IdleConnTimeout: got %v, want 60s", tr.IdleConnTimeout)
	}
}

func TestBuild_ForceHTTP2(t *testing.T) {
	t.Parallel()
	b := pkgtransport.NewBuilder()
	tr, err := b.Build(config.TransportSpec{ForceHTTP2: true})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if !tr.ForceAttemptHTTP2 {
		t.Error("ForceAttemptHTTP2 not set")
	}
}

func TestBuild_ResponseHeaderTimeout(t *testing.T) {
	t.Parallel()
	b := pkgtransport.NewBuilder()
	tr, err := b.Build(config.TransportSpec{ResponseHeaderTimeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if tr.ResponseHeaderTimeout != 5*time.Second {
		t.Errorf("ResponseHeaderTimeout: got %v, want 5s", tr.ResponseHeaderTimeout)
	}
}

func TestBuild_HTTPProxy(t *testing.T) {
	t.Parallel()
	b := pkgtransport.NewBuilder()
	tr, err := b.Build(config.TransportSpec{ProxyURL: "http://proxy.example.com:3128"})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if tr.Proxy == nil {
		t.Error("Proxy function not set for HTTP proxy URL")
	}
}

func TestBuild_InvalidProxyURL(t *testing.T) {
	t.Parallel()
	b := pkgtransport.NewBuilder()
	_, err := b.Build(config.TransportSpec{ProxyURL: "://invalid"})
	if err == nil {
		t.Error("expected error for invalid proxy URL, got nil")
	}
}

func TestBuild_InsecureSkipVerify(t *testing.T) {
	t.Parallel()
	b := pkgtransport.NewBuilder()
	tr, err := b.Build(config.TransportSpec{
		TLS: config.TLSSpec{InsecureSkipVerify: true},
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if tr.TLSClientConfig == nil || !tr.TLSClientConfig.InsecureSkipVerify {
		t.Error("InsecureSkipVerify not set on TLS config")
	}
}

func TestBuild_TLSVersions(t *testing.T) {
	t.Parallel()
	b := pkgtransport.NewBuilder()
	tr, err := b.Build(config.TransportSpec{
		TLS: config.TLSSpec{MinVersion: "1.2", MaxVersion: "1.3"},
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if tr.TLSClientConfig.MinVersion != tls.VersionTLS12 {
		t.Errorf("MinVersion: got %d, want %d", tr.TLSClientConfig.MinVersion, tls.VersionTLS12)
	}
	if tr.TLSClientConfig.MaxVersion != tls.VersionTLS13 {
		t.Errorf("MaxVersion: got %d, want %d", tr.TLSClientConfig.MaxVersion, tls.VersionTLS13)
	}
}

func TestBuild_InvalidTLSVersion(t *testing.T) {
	t.Parallel()
	b := pkgtransport.NewBuilder()
	_, err := b.Build(config.TransportSpec{
		TLS: config.TLSSpec{MinVersion: "99.9"},
	})
	if err == nil {
		t.Error("expected error for invalid TLS version, got nil")
	}
}

func TestBuild_SessionCache(t *testing.T) {
	t.Parallel()
	b := pkgtransport.NewBuilder()
	tr, err := b.Build(config.TransportSpec{
		TLS: config.TLSSpec{SessionCacheSize: 32},
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if tr.TLSClientConfig.ClientSessionCache == nil {
		t.Error("ClientSessionCache not set")
	}
}

func TestBuild_CustomRootCA(t *testing.T) {
	t.Parallel()
	// Generate a self-signed CA cert for testing.
	caKey, caCert, caCertPEM := generateCA(t)
	_ = caKey

	tmpDir := t.TempDir()
	caPath := filepath.Join(tmpDir, "ca.pem")
	if err := os.WriteFile(caPath, caCertPEM, 0o600); err != nil {
		t.Fatalf("write CA file: %v", err)
	}
	_ = caCert

	b := pkgtransport.NewBuilder()
	tr, err := b.Build(config.TransportSpec{
		TLS: config.TLSSpec{RootCAPath: caPath},
	})
	if err != nil {
		t.Fatalf("Build with custom CA: %v", err)
	}
	if tr.TLSClientConfig.RootCAs == nil {
		t.Error("RootCAs not set")
	}
}

func TestBuild_InvalidRootCA(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	caPath := filepath.Join(tmpDir, "ca.pem")
	if err := os.WriteFile(caPath, []byte("not-a-pem"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}

	b := pkgtransport.NewBuilder()
	_, err := b.Build(config.TransportSpec{
		TLS: config.TLSSpec{RootCAPath: caPath},
	})
	if err == nil {
		t.Error("expected error for invalid CA PEM, got nil")
	}
}

func TestBuild_MissingRootCAFile(t *testing.T) {
	t.Parallel()
	b := pkgtransport.NewBuilder()
	_, err := b.Build(config.TransportSpec{
		TLS: config.TLSSpec{RootCAPath: "/nonexistent/ca.pem"},
	})
	if err == nil {
		t.Error("expected error for missing CA file, got nil")
	}
}

func TestBuild_mTLS(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	certPath, keyPath := generateClientCert(t, tmpDir)

	b := pkgtransport.NewBuilder()
	tr, err := b.Build(config.TransportSpec{
		TLS: config.TLSSpec{
			ClientCertPath: certPath,
			ClientKeyPath:  keyPath,
		},
	})
	if err != nil {
		t.Fatalf("Build with mTLS: %v", err)
	}
	if len(tr.TLSClientConfig.Certificates) != 1 {
		t.Errorf("expected 1 client certificate, got %d", len(tr.TLSClientConfig.Certificates))
	}
}

func TestBuild_mTLS_MissingKey(t *testing.T) {
	t.Parallel()
	b := pkgtransport.NewBuilder()
	_, err := b.Build(config.TransportSpec{
		TLS: config.TLSSpec{
			ClientCertPath: "/some/cert.pem",
			// ClientKeyPath intentionally missing
		},
	})
	if err == nil {
		t.Error("expected error when only client_cert_path is set without client_key_path")
	}
}

func TestBuild_SNIOverride(t *testing.T) {
	t.Parallel()
	b := pkgtransport.NewBuilder()
	tr, err := b.Build(config.TransportSpec{
		TLS: config.TLSSpec{ServerName: "override.example.com"},
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if tr.TLSClientConfig.ServerName != "override.example.com" {
		t.Errorf("ServerName: got %q, want %q", tr.TLSClientConfig.ServerName, "override.example.com")
	}
}

func TestBuildServerTLSConfig_Basic(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	certPath, keyPath := generateServerCert(t, tmpDir)

	tlsCfg, err := pkgtransport.BuildServerTLSConfig(config.ServerTLSSpec{
		CertPath: certPath,
		KeyPath:  keyPath,
	})
	if err != nil {
		t.Fatalf("BuildServerTLSConfig: %v", err)
	}
	if len(tlsCfg.Certificates) != 1 {
		t.Errorf("expected 1 certificate, got %d", len(tlsCfg.Certificates))
	}
	if tlsCfg.ClientAuth != tls.NoClientCert {
		t.Errorf("ClientAuth: got %v, want NoClientCert", tlsCfg.ClientAuth)
	}
}

func TestBuildServerTLSConfig_RequireAndVerify(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	certPath, keyPath := generateServerCert(t, tmpDir)
	_, _, caPEM := generateCA(t)
	caPath := filepath.Join(tmpDir, "client-ca.pem")
	if err := os.WriteFile(caPath, caPEM, 0o600); err != nil {
		t.Fatalf("write CA file: %v", err)
	}

	tlsCfg, err := pkgtransport.BuildServerTLSConfig(config.ServerTLSSpec{
		CertPath:     certPath,
		KeyPath:      keyPath,
		ClientAuth:   "require_and_verify",
		ClientCAPath: caPath,
	})
	if err != nil {
		t.Fatalf("BuildServerTLSConfig: %v", err)
	}
	if tlsCfg.ClientAuth != tls.RequireAndVerifyClientCert {
		t.Errorf("ClientAuth: got %v, want RequireAndVerifyClientCert", tlsCfg.ClientAuth)
	}
	if tlsCfg.ClientCAs == nil {
		t.Error("ClientCAs not set")
	}
}

func TestBuildServerTLSConfig_InvalidClientAuth(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	certPath, keyPath := generateServerCert(t, tmpDir)

	_, err := pkgtransport.BuildServerTLSConfig(config.ServerTLSSpec{
		CertPath:   certPath,
		KeyPath:    keyPath,
		ClientAuth: "bogus",
	})
	if err == nil {
		t.Error("expected error for invalid client_auth mode")
	}
}

func TestBuildServerTLSConfig_MinVersion(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	certPath, keyPath := generateServerCert(t, tmpDir)

	tlsCfg, err := pkgtransport.BuildServerTLSConfig(config.ServerTLSSpec{
		CertPath:   certPath,
		KeyPath:    keyPath,
		MinVersion: "1.3",
	})
	if err != nil {
		t.Fatalf("BuildServerTLSConfig: %v", err)
	}
	if tlsCfg.MinVersion != tls.VersionTLS13 {
		t.Errorf("MinVersion: got %d, want TLS 1.3", tlsCfg.MinVersion)
	}
}

// generateCA generates a self-signed CA key and certificate for testing.
// Returns the key, the parsed cert, and the PEM-encoded cert bytes.
func generateCA(t *testing.T) (*ecdsa.PrivateKey, *x509.Certificate, []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate CA key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
	}
	certDER, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create CA cert: %v", err)
	}
	cert, err := x509.ParseCertificate(certDER)
	if err != nil {
		t.Fatalf("parse CA cert: %v", err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	return key, cert, pemBytes
}

// generateClientCert generates a client cert/key pair and writes PEM files to dir.
func generateClientCert(t *testing.T, dir string) (certPath, keyPath string) {
	t.Helper()
	return generateLeafCert(t, dir, "client-cert.pem", "client-key.pem", []net.IP{net.ParseIP("127.0.0.1")})
}

// generateServerCert generates a server cert/key pair and writes PEM files to dir.
func generateServerCert(t *testing.T, dir string) (certPath, keyPath string) {
	t.Helper()
	return generateLeafCert(t, dir, "server-cert.pem", "server-key.pem", []net.IP{net.ParseIP("127.0.0.1")})
}

func generateLeafCert(t *testing.T, dir, certFile, keyFile string, ips []net.IP) (certPath, keyPath string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		IPAddresses:  ips,
	}
	certDER, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create cert: %v", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}

	certPath = filepath.Join(dir, certFile)
	keyPath = filepath.Join(dir, keyFile)

	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER}), 0o600); err != nil {
		t.Fatalf("write cert: %v", err)
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}
	return certPath, keyPath
}
