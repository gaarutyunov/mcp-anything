// Package redis registers the "redis" session store provider.
// Tokens are AES-256-GCM encrypted. The Redis key TTL is set to the token expiry.
// Import this package with a blank import to make the provider available:
//
//	import _ "github.com/gaarutyunov/mcp-anything/pkg/session/redis"
package redis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"github.com/gaarutyunov/mcp-anything/pkg/config"
	"github.com/gaarutyunov/mcp-anything/pkg/session"
)

func init() {
	session.Register("redis", func(ctx context.Context, cfg *config.SessionStoreConfig) (session.Store, error) {
		return New(ctx, cfg.Redis)
	})
}

// Store is a Redis-backed session store with AES-256-GCM encrypted tokens.
// The Redis key TTL is set to the token's expiry time so expired tokens are
// automatically evicted from the store.
type Store struct {
	client *goredis.Client
	encKey []byte
}

// tokenData is the JSON structure stored (encrypted) in Redis.
type tokenData struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	Expiry       time.Time `json:"expiry"`
	TokenType    string    `json:"token_type"`
}

// New creates a Redis session store and verifies connectivity.
func New(ctx context.Context, cfg config.RedisSessionConfig) (*Store, error) {
	encKey, err := session.ParseKey(cfg.EncryptionKey)
	if err != nil {
		return nil, fmt.Errorf("redis session store encryption key: %w", err)
	}

	client := goredis.NewClient(&goredis.Options{
		Addr:     cfg.Addr,
		Password: os.ExpandEnv(cfg.Password),
	})

	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("connecting to redis: %w", err)
	}

	return &Store{client: client, encKey: encKey}, nil
}

func redisKey(userSubject, upstreamName string) string {
	return "mcp_session:" + userSubject + ":" + upstreamName
}

// Save encrypts and stores the token. The Redis TTL is set to token.Expiry if non-zero.
func (s *Store) Save(ctx context.Context, userSubject, upstreamName string, token *config.OAuthToken) error {
	data := tokenData{
		AccessToken:  token.AccessToken,
		RefreshToken: token.RefreshToken,
		Expiry:       token.Expiry,
		TokenType:    token.TokenType,
	}
	plaintext, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("marshalling token: %w", err)
	}
	encrypted, err := session.Encrypt(s.encKey, plaintext)
	if err != nil {
		return fmt.Errorf("encrypting token: %w", err)
	}

	var ttl time.Duration
	if !token.Expiry.IsZero() {
		ttl = time.Until(token.Expiry)
		if ttl < 0 {
			ttl = 0
		}
	}

	k := redisKey(userSubject, upstreamName)
	if err := s.client.Set(ctx, k, encrypted, ttl).Err(); err != nil {
		return fmt.Errorf("saving session to redis: %w", err)
	}
	return nil
}

// Load retrieves and decrypts the token. Returns nil, nil if not found.
func (s *Store) Load(ctx context.Context, userSubject, upstreamName string) (*config.OAuthToken, error) {
	val, err := s.client.Get(ctx, redisKey(userSubject, upstreamName)).Bytes()
	if err != nil {
		if errors.Is(err, goredis.Nil) {
			return nil, nil
		}
		return nil, fmt.Errorf("loading session from redis: %w", err)
	}

	plaintext, err := session.Decrypt(s.encKey, val)
	if err != nil {
		return nil, fmt.Errorf("decrypting token: %w", err)
	}
	var data tokenData
	if err := json.Unmarshal(plaintext, &data); err != nil {
		return nil, fmt.Errorf("unmarshalling token: %w", err)
	}
	return &config.OAuthToken{
		AccessToken:  data.AccessToken,
		RefreshToken: data.RefreshToken,
		Expiry:       data.Expiry,
		TokenType:    data.TokenType,
	}, nil
}

// Delete removes the token for the given user and upstream.
func (s *Store) Delete(ctx context.Context, userSubject, upstreamName string) error {
	if err := s.client.Del(ctx, redisKey(userSubject, upstreamName)).Err(); err != nil {
		return fmt.Errorf("deleting session from redis: %w", err)
	}
	return nil
}
