// Package callbackmux implements the OAuth2 authorization callback handler.
// It handles GET /oauth/callback/{upstreamName}, verifies HMAC-signed state,
// exchanges the authorization code for tokens, and persists them in the session store.
// It also implements config.OAuthCallbackRegistrar so outbound auth providers can
// register their OAuth2 configuration.
package callbackmux

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/oauth2"

	"github.com/gaarutyunov/mcp-anything/pkg/config"
)

// upstreamEntry holds the OAuth2 config and HTTP client for one upstream.
type upstreamEntry struct {
	cfg        *oauth2.Config
	httpClient *http.Client
}

// Mux handles OAuth2 callback requests and registers upstream OAuth2 configurations.
// It implements config.OAuthCallbackRegistrar and the server.OAuthCallbackHandler interface.
type Mux struct {
	store     config.OAuthTokenStore
	hmacKey   []byte
	providers sync.Map // string → *upstreamEntry
}

// New creates a Mux with the given session store and HMAC key.
// store may be nil only during startup; HandleCallback will return 503 if store is nil.
func New(store config.OAuthTokenStore, hmacKey []byte) *Mux {
	return &Mux{store: store, hmacKey: hmacKey}
}

// RegisterProvider registers an upstream's OAuth2 configuration.
// Implements config.OAuthCallbackRegistrar.
func (m *Mux) RegisterProvider(upstreamName, authURL, tokenURL, clientID, clientSecret string, scopes []string, redirectURL string) {
	entry := &upstreamEntry{
		cfg: &oauth2.Config{
			ClientID:     clientID,
			ClientSecret: clientSecret,
			Endpoint: oauth2.Endpoint{
				AuthURL:  authURL,
				TokenURL: tokenURL,
			},
			RedirectURL: redirectURL,
			Scopes:      scopes,
		},
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
	m.providers.Store(upstreamName, entry)
}

// AuthURL generates the authorization URL with an HMAC-SHA256-signed state parameter.
// Implements config.OAuthCallbackRegistrar.
func (m *Mux) AuthURL(upstreamName, userSubject string) (string, error) {
	v, ok := m.providers.Load(upstreamName)
	if !ok {
		return "", fmt.Errorf("no OAuth2 provider registered for upstream %q", upstreamName)
	}
	entry := v.(*upstreamEntry)
	state, err := m.signedState(upstreamName, userSubject)
	if err != nil {
		return "", fmt.Errorf("generating state: %w", err)
	}
	return entry.cfg.AuthCodeURL(state, oauth2.AccessTypeOffline), nil
}

// HandleCallback handles GET /oauth/callback/{upstreamName}?code=...&state=...
// It verifies the HMAC state, exchanges the code, and saves the token to the session store.
func (m *Mux) HandleCallback(w http.ResponseWriter, r *http.Request, upstreamName string) {
	if m.store == nil {
		http.Error(w, "session store not configured", http.StatusServiceUnavailable)
		return
	}

	code := r.URL.Query().Get("code")
	state := r.URL.Query().Get("state")
	if code == "" || state == "" {
		http.Error(w, "missing code or state parameter", http.StatusBadRequest)
		return
	}

	// Verify and decode state.
	userSubject, err := m.verifyState(upstreamName, state)
	if err != nil {
		http.Error(w, "invalid state parameter: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Look up the provider config.
	v, ok := m.providers.Load(upstreamName)
	if !ok {
		http.Error(w, "unknown upstream: "+upstreamName, http.StatusNotFound)
		return
	}
	entry := v.(*upstreamEntry)

	// Exchange authorization code for tokens.
	httpCtx := context.WithValue(r.Context(), oauth2.HTTPClient, entry.httpClient)
	tok, err := entry.cfg.Exchange(httpCtx, code)
	if err != nil {
		http.Error(w, "code exchange failed: "+err.Error(), http.StatusBadGateway)
		return
	}

	// Persist token to the session store.
	oauthTok := &config.OAuthToken{
		AccessToken:  tok.AccessToken,
		RefreshToken: tok.RefreshToken,
		Expiry:       tok.Expiry,
		TokenType:    tok.TokenType,
	}
	if err := m.store.Save(r.Context(), userSubject, upstreamName, oauthTok); err != nil {
		http.Error(w, "failed to save token: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = fmt.Fprint(w, "Authorization successful! You can now close this window and retry your request.")
}

// statePayload is the JSON embedded in the OAuth state parameter.
type statePayload struct {
	Upstream string `json:"u"`
	Subject  string `json:"s"`
	Nonce    string `json:"n"`
}

// signedState builds an HMAC-SHA256-signed state string.
// Format: base64url(JSON).base64url(HMAC-SHA256)
func (m *Mux) signedState(upstreamName, userSubject string) (string, error) {
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("generating nonce: %w", err)
	}
	payload := statePayload{
		Upstream: upstreamName,
		Subject:  userSubject,
		Nonce:    base64.RawURLEncoding.EncodeToString(nonce),
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshalling state: %w", err)
	}
	encoded := base64.RawURLEncoding.EncodeToString(data)
	sig := m.computeHMAC([]byte(encoded))
	return encoded + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

// verifyState verifies the HMAC signature and returns the user subject.
func (m *Mux) verifyState(upstreamName, state string) (string, error) {
	parts := strings.SplitN(state, ".", 2)
	if len(parts) != 2 {
		return "", fmt.Errorf("malformed state parameter")
	}
	encoded, sigEncoded := parts[0], parts[1]

	sig, err := base64.RawURLEncoding.DecodeString(sigEncoded)
	if err != nil {
		return "", fmt.Errorf("decoding state signature: %w", err)
	}
	expected := m.computeHMAC([]byte(encoded))
	if !hmac.Equal(sig, expected) {
		return "", fmt.Errorf("invalid HMAC signature")
	}

	data, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return "", fmt.Errorf("decoding state payload: %w", err)
	}
	var payload statePayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return "", fmt.Errorf("parsing state payload: %w", err)
	}
	if payload.Upstream != upstreamName {
		return "", fmt.Errorf("state upstream %q does not match callback upstream %q", payload.Upstream, upstreamName)
	}
	return payload.Subject, nil
}

func (m *Mux) computeHMAC(data []byte) []byte {
	mac := hmac.New(sha256.New, m.hmacKey)
	mac.Write(data)
	return mac.Sum(nil)
}
