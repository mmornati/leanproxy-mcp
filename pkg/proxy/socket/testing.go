package socket

import "context"

type mockTokenResolver struct {
	resolveFunc func(ctx context.Context, uri string) (interface{}, error)
}

func (m *mockTokenResolver) Resolve(ctx context.Context, uri string) (interface{}, error) {
	if m.resolveFunc != nil {
		return m.resolveFunc(ctx, uri)
	}
	return map[string]interface{}{
		"token": "mock-token-" + uri,
		"uri":   uri,
	}, nil
}