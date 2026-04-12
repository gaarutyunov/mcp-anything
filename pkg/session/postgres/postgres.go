// Package postgres registers the "postgres" session store provider.
// Tokens are AES-256-GCM encrypted before storage.
// Import this package with a blank import to make the provider available:
//
//	import _ "github.com/gaarutyunov/mcp-anything/pkg/session/postgres"
package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/gaarutyunov/mcp-anything/pkg/config"
	"github.com/gaarutyunov/mcp-anything/pkg/session"
)

func init() {
	session.Register("postgres", func(ctx context.Context, cfg *config.SessionStoreConfig) (session.Store, error) {
		return New(ctx, cfg.Postgres)
	})
}

// Store is a PostgreSQL-backed session store with AES-256-GCM encrypted tokens.
type Store struct {
	pool   *pgxpool.Pool
	encKey []byte
}

// tokenRow is the JSON structure stored (encrypted) in the database.
type tokenRow struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	Expiry       time.Time `json:"expiry"`
	TokenType    string    `json:"token_type"`
}

const createTableSQL = `
CREATE TABLE IF NOT EXISTS mcp_sessions (
    user_subject   TEXT        NOT NULL,
    upstream_name  TEXT        NOT NULL,
    encrypted_data BYTEA       NOT NULL,
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_subject, upstream_name)
)`

// New creates a PostgreSQL session store, creating the mcp_sessions table if absent.
func New(ctx context.Context, cfg config.PostgresSessionConfig) (*Store, error) {
	encKey, err := session.ParseKey(cfg.EncryptionKey)
	if err != nil {
		return nil, fmt.Errorf("postgres session store encryption key: %w", err)
	}

	pool, err := pgxpool.New(ctx, os.ExpandEnv(cfg.DSN))
	if err != nil {
		return nil, fmt.Errorf("connecting to postgres: %w", err)
	}

	if _, execErr := pool.Exec(ctx, createTableSQL); execErr != nil {
		pool.Close()
		return nil, fmt.Errorf("creating mcp_sessions table: %w", execErr)
	}

	return &Store{pool: pool, encKey: encKey}, nil
}

// Save encrypts and stores the token for the given user and upstream.
func (s *Store) Save(ctx context.Context, userSubject, upstreamName string, token *config.OAuthToken) error {
	row := tokenRow{
		AccessToken:  token.AccessToken,
		RefreshToken: token.RefreshToken,
		Expiry:       token.Expiry,
		TokenType:    token.TokenType,
	}
	plaintext, err := json.Marshal(row)
	if err != nil {
		return fmt.Errorf("marshalling token: %w", err)
	}
	encrypted, err := session.Encrypt(s.encKey, plaintext)
	if err != nil {
		return fmt.Errorf("encrypting token: %w", err)
	}
	_, err = s.pool.Exec(ctx,
		`INSERT INTO mcp_sessions (user_subject, upstream_name, encrypted_data, updated_at)
		 VALUES ($1, $2, $3, now())
		 ON CONFLICT (user_subject, upstream_name)
		 DO UPDATE SET encrypted_data = $3, updated_at = now()`,
		userSubject, upstreamName, encrypted,
	)
	if err != nil {
		return fmt.Errorf("saving session to postgres: %w", err)
	}
	return nil
}

// Load retrieves and decrypts the token for the given user and upstream.
// Returns nil, nil if not found.
func (s *Store) Load(ctx context.Context, userSubject, upstreamName string) (*config.OAuthToken, error) {
	var encrypted []byte
	err := s.pool.QueryRow(ctx,
		`SELECT encrypted_data FROM mcp_sessions WHERE user_subject = $1 AND upstream_name = $2`,
		userSubject, upstreamName,
	).Scan(&encrypted)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("loading session from postgres: %w", err)
	}

	plaintext, err := session.Decrypt(s.encKey, encrypted)
	if err != nil {
		return nil, fmt.Errorf("decrypting token: %w", err)
	}
	var row tokenRow
	if err := json.Unmarshal(plaintext, &row); err != nil {
		return nil, fmt.Errorf("unmarshalling token: %w", err)
	}
	return &config.OAuthToken{
		AccessToken:  row.AccessToken,
		RefreshToken: row.RefreshToken,
		Expiry:       row.Expiry,
		TokenType:    row.TokenType,
	}, nil
}

// Delete removes the token for the given user and upstream.
func (s *Store) Delete(ctx context.Context, userSubject, upstreamName string) error {
	_, err := s.pool.Exec(ctx,
		`DELETE FROM mcp_sessions WHERE user_subject = $1 AND upstream_name = $2`,
		userSubject, upstreamName,
	)
	if err != nil {
		return fmt.Errorf("deleting session from postgres: %w", err)
	}
	return nil
}
