package transport

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"time"

	"golang.org/x/net/proxy"

	"github.com/gaarutyunov/mcp-anything/internal/config"
)

// Builder constructs configured http.Transport instances per upstream.
type Builder struct{}

// NewBuilder returns a new Builder.
func NewBuilder() *Builder {
	return &Builder{}
}

// Build creates an *http.Transport from a TransportConfig.
// Zero values for numeric/duration fields use production-ready defaults.
func (b *Builder) Build(cfg config.TransportConfig) (*http.Transport, error) {
	if cfg.MaxIdleConns == 0 {
		cfg.MaxIdleConns = 100
	}
	if cfg.MaxIdleConnsPerHost == 0 {
		cfg.MaxIdleConnsPerHost = 10
	}
	if cfg.IdleConnTimeout == 0 {
		cfg.IdleConnTimeout = 90 * time.Second
	}
	if cfg.DialTimeout == 0 {
		cfg.DialTimeout = 30 * time.Second
	}
	if cfg.DialKeepalive == 0 {
		cfg.DialKeepalive = 30 * time.Second
	}

	tlsCfg, err := BuildTLSConfig(cfg.TLS)
	if err != nil {
		return nil, fmt.Errorf("tls config: %w", err)
	}

	dialer := &net.Dialer{
		Timeout:   cfg.DialTimeout,
		KeepAlive: cfg.DialKeepalive,
	}

	t := &http.Transport{
		TLSClientConfig:       tlsCfg,
		MaxIdleConns:          cfg.MaxIdleConns,
		MaxIdleConnsPerHost:   cfg.MaxIdleConnsPerHost,
		IdleConnTimeout:       cfg.IdleConnTimeout,
		ResponseHeaderTimeout: cfg.ResponseHeaderTimeout,
		ForceAttemptHTTP2:     cfg.ForceHTTP2,
		DialContext:           dialer.DialContext,
	}

	if cfg.ProxyURL != "" {
		if err := applyProxy(t, cfg.ProxyURL, dialer); err != nil {
			return nil, fmt.Errorf("proxy: %w", err)
		}
	}

	return t, nil
}

// applyProxy configures t to dial through the given proxy URL.
// Supports http://, https:// (HTTP CONNECT), socks5://, and socks5h:// schemes.
func applyProxy(t *http.Transport, rawURL string, dialer *net.Dialer) error {
	proxyURL, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("parsing proxy URL %q: %w", rawURL, err)
	}

	switch proxyURL.Scheme {
	case "socks5", "socks5h":
		var auth *proxy.Auth
		if proxyURL.User != nil {
			pw, _ := proxyURL.User.Password()
			auth = &proxy.Auth{
				User:     proxyURL.User.Username(),
				Password: pw,
			}
		}
		s5, err := proxy.SOCKS5("tcp", proxyURL.Host, auth, &netDialerAdapter{dialer})
		if err != nil {
			return fmt.Errorf("creating SOCKS5 proxy dialer: %w", err)
		}
		if cd, ok := s5.(proxy.ContextDialer); ok {
			t.DialContext = cd.DialContext
		} else {
			t.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
				return s5.Dial(network, addr)
			}
		}
	default:
		t.Proxy = http.ProxyURL(proxyURL)
	}
	return nil
}

// netDialerAdapter adapts *net.Dialer to the proxy.Dialer interface.
type netDialerAdapter struct {
	d *net.Dialer
}

func (a *netDialerAdapter) Dial(network, addr string) (net.Conn, error) {
	return a.d.Dial(network, addr)
}
