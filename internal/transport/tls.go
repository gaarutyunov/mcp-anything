// Package transport constructs configured http.Transport and tls.Config instances.
package transport

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"

	"github.com/gaarutyunov/mcp-anything/internal/config"
)

// BuildTLSConfig constructs a *tls.Config for outbound (upstream) connections.
func BuildTLSConfig(cfg config.TLSConfig) (*tls.Config, error) {
	minVer := uint16(tls.VersionTLS12)
	if cfg.MinVersion != "" {
		v, err := parseTLSVersion(cfg.MinVersion)
		if err != nil {
			return nil, fmt.Errorf("min_version: %w", err)
		}
		minVer = v
	}

	var maxVer uint16
	if cfg.MaxVersion != "" {
		v, err := parseTLSVersion(cfg.MaxVersion)
		if err != nil {
			return nil, fmt.Errorf("max_version: %w", err)
		}
		maxVer = v
	}

	cacheSize := cfg.SessionCacheSize
	if cacheSize <= 0 {
		cacheSize = 64
	}

	tlsCfg := &tls.Config{ //nolint:gosec
		InsecureSkipVerify: cfg.InsecureSkipVerify,
		ServerName:         cfg.ServerName,
		MinVersion:         minVer,
		MaxVersion:         maxVer,
		ClientSessionCache: tls.NewLRUClientSessionCache(cacheSize),
	}

	if cfg.RootCAPath != "" {
		pool := x509.NewCertPool()
		pem, err := os.ReadFile(cfg.RootCAPath)
		if err != nil {
			return nil, fmt.Errorf("reading root CA %q: %w", cfg.RootCAPath, err)
		}
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("no valid certificate found in %q", cfg.RootCAPath)
		}
		tlsCfg.RootCAs = pool
	}

	if cfg.ClientCertPath != "" || cfg.ClientKeyPath != "" {
		if cfg.ClientCertPath == "" || cfg.ClientKeyPath == "" {
			return nil, fmt.Errorf("both client_cert_path and client_key_path must be set for mTLS")
		}
		cert, err := tls.LoadX509KeyPair(cfg.ClientCertPath, cfg.ClientKeyPath)
		if err != nil {
			return nil, fmt.Errorf("loading client certificate: %w", err)
		}
		tlsCfg.Certificates = []tls.Certificate{cert}
	}

	return tlsCfg, nil
}

// BuildServerTLSConfig constructs a *tls.Config for inbound TLS termination.
func BuildServerTLSConfig(cfg config.ServerTLSConfig) (*tls.Config, error) {
	cert, err := tls.LoadX509KeyPair(cfg.CertPath, cfg.KeyPath)
	if err != nil {
		return nil, fmt.Errorf("loading server certificate: %w", err)
	}

	minVer := uint16(tls.VersionTLS12)
	if cfg.MinVersion != "" {
		v, err := parseTLSVersion(cfg.MinVersion)
		if err != nil {
			return nil, fmt.Errorf("min_version: %w", err)
		}
		minVer = v
	}

	clientAuth, err := parseClientAuth(cfg.ClientAuth)
	if err != nil {
		return nil, fmt.Errorf("client_auth: %w", err)
	}

	tlsCfg := &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   minVer,
		ClientAuth:   clientAuth,
	}

	if cfg.ClientCAPath != "" {
		pool := x509.NewCertPool()
		pem, err := os.ReadFile(cfg.ClientCAPath)
		if err != nil {
			return nil, fmt.Errorf("reading client CA %q: %w", cfg.ClientCAPath, err)
		}
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("no valid certificate found in %q", cfg.ClientCAPath)
		}
		tlsCfg.ClientCAs = pool
	}

	return tlsCfg, nil
}

func parseTLSVersion(v string) (uint16, error) {
	switch v {
	case "1.0":
		return tls.VersionTLS10, nil
	case "1.1":
		return tls.VersionTLS11, nil
	case "1.2":
		return tls.VersionTLS12, nil
	case "1.3":
		return tls.VersionTLS13, nil
	default:
		return 0, fmt.Errorf("unsupported TLS version %q (use 1.0, 1.1, 1.2, or 1.3)", v)
	}
}

func parseClientAuth(s string) (tls.ClientAuthType, error) {
	switch s {
	case "", "none":
		return tls.NoClientCert, nil
	case "request":
		return tls.RequestClientCert, nil
	case "require_and_verify":
		return tls.RequireAndVerifyClientCert, nil
	default:
		return 0, fmt.Errorf("unsupported client_auth mode %q (use none, request, or require_and_verify)", s)
	}
}
