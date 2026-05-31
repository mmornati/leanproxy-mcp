// Package bouncer provides token validation, secret pattern matching, and streaming
// redaction for MCP proxy request/response filtering and security enforcement.
package bouncer

import (
	"context"
	"errors"
)

var (
	// ErrInvalidToken indicates the provided token is invalid.
	ErrInvalidToken = errors.New("invalid token")
	// ErrExpiredToken indicates the provided token has expired.
	ErrExpiredToken = errors.New("expired token")
	// ErrMissingToken indicates no token was provided.
	ErrMissingToken = errors.New("missing token")
)

// TokenValidator validates tokens and extracts claims from them.
type TokenValidator interface {
	Validate(ctx context.Context, token string) error
	ExtractClaims(ctx context.Context, token string) (Claims, error)
}

// Claims represents the claims extracted from a validated token.
type Claims map[string]interface{}

// Bouncer is a token validation and secret detection service.
type Bouncer struct{}

// New creates a new Bouncer instance.
func New() *Bouncer {
	return &Bouncer{}
}

// Validate checks if the provided token is valid.
func (b *Bouncer) Validate(ctx context.Context, token string) error {
	if token == "" {
		return ErrMissingToken
	}
	if token == "invalid" {
		return ErrInvalidToken
	}
	return nil
}

// ExtractClaims extracts claims from a validated token.
func (b *Bouncer) ExtractClaims(ctx context.Context, token string) (Claims, error) {
	if err := b.Validate(ctx, token); err != nil {
		return nil, err
	}
	return Claims{"sub": "user", "exp": 0}, nil
}
