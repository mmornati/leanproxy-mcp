package socket

import (
	"context"
	"encoding/json"
	"fmt"
)

type Handler struct {
	tokenResolver TokenResolver
	proxyStatus   ProxyStatusProvider
	configGetter  ConfigGetter
	configSetter  ConfigSetter
	shutdownFn    func()
}

type TokenResolver interface {
	Resolve(ctx context.Context, uri string) (interface{}, error)
}

type ProxyStatusProvider interface {
	Status(ctx context.Context) (interface{}, error)
}

type ConfigGetter interface {
	Get(ctx context.Context, key string) (interface{}, error)
}

type ConfigSetter interface {
	Set(ctx context.Context, key string, value interface{}) error
}

func NewHandler(
	tokenResolver TokenResolver,
	proxyStatus ProxyStatusProvider,
	configGetter ConfigGetter,
	configSetter ConfigSetter,
	shutdownFn func(),
) *Handler {
	return &Handler{
		tokenResolver: tokenResolver,
		proxyStatus:   proxyStatus,
		configGetter:  configGetter,
		configSetter:  configSetter,
		shutdownFn:    shutdownFn,
	}
}

func (h *Handler) RegisterMethods(server *Server) {
	server.RegisterMethod("token.resolve", h.handleTokenResolve)
	server.RegisterMethod("token.validate", h.handleTokenValidate)
	server.RegisterMethod("proxy.status", h.handleProxyStatus)
	server.RegisterMethod("proxy.restart", h.handleProxyRestart)
	server.RegisterMethod("config.get", h.handleConfigGet)
	server.RegisterMethod("config.set", h.handleConfigSet)
	server.RegisterMethod("shutdown", h.handleShutdown)
}

type tokenResolveParams struct {
	URI string `json:"uri"`
}

func (h *Handler) handleTokenResolve(ctx context.Context, params json.RawMessage) (interface{}, error) {
	if h.tokenResolver == nil {
		return nil, fmt.Errorf("token resolver not configured")
	}

	var p tokenResolveParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}

	return h.tokenResolver.Resolve(ctx, p.URI)
}

type tokenValidateParams struct {
	Token string `json:"token"`
	Policy string `json:"policy"`
}

func (h *Handler) handleTokenValidate(ctx context.Context, params json.RawMessage) (interface{}, error) {
	var p tokenValidateParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}

	return map[string]interface{}{
		"valid":   true,
		"token":   p.Token,
		"policy":  p.Policy,
	}, nil
}

func (h *Handler) handleProxyStatus(ctx context.Context, params json.RawMessage) (interface{}, error) {
	if h.proxyStatus == nil {
		return map[string]interface{}{
			"status": "unknown",
		}, nil
	}
	return h.proxyStatus.Status(ctx)
}

func (h *Handler) handleProxyRestart(ctx context.Context, params json.RawMessage) (interface{}, error) {
	return map[string]interface{}{
		"restarted": true,
	}, nil
}

type configGetParams struct {
	Key string `json:"key"`
}

func (h *Handler) handleConfigGet(ctx context.Context, params json.RawMessage) (interface{}, error) {
	if h.configGetter == nil {
		return nil, fmt.Errorf("config getter not configured")
	}

	var p configGetParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}

	return h.configGetter.Get(ctx, p.Key)
}

type configSetParams struct {
	Key   string      `json:"key"`
	Value interface{} `json:"value"`
}

func (h *Handler) handleConfigSet(ctx context.Context, params json.RawMessage) (interface{}, error) {
	if h.configSetter == nil {
		return nil, fmt.Errorf("config setter not configured")
	}

	var p configSetParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}

	if err := h.configSetter.Set(ctx, p.Key, p.Value); err != nil {
		return nil, err
	}

	return map[string]interface{}{
		"set": true,
	}, nil
}

func (h *Handler) handleShutdown(ctx context.Context, params json.RawMessage) (interface{}, error) {
	if h.shutdownFn != nil {
		go h.shutdownFn()
	}
	return map[string]interface{}{
		"shutting_down": true,
	}, nil
}