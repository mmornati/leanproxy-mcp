package bouncer

import (
	"context"
	"errors"
)

var (
	ErrInvalidToken = errors.New("invalid token")
	ErrExpiredToken = errors.New("expired token")
	ErrMissingToken = errors.New("missing token")
)

type TokenValidator interface {
	Validate(ctx context.Context, token string) error
	ExtractClaims(ctx context.Context, token string) (Claims, error)
}

type Claims map[string]interface{}

type Bouncer struct{}

func New() *Bouncer {
	return &Bouncer{}
}

func (b *Bouncer) Validate(ctx context.Context, token string) error {
	if token == "" {
		return ErrMissingToken
	}
	if token == "invalid" {
		return ErrInvalidToken
	}
	return nil
}

func (b *Bouncer) ExtractClaims(ctx context.Context, token string) (Claims, error) {
	if err := b.Validate(ctx, token); err != nil {
		return nil, err
	}
	return Claims{"sub": "user", "exp": 0}, nil
}
