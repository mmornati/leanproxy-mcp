package router

import (
	"context"
	"strings"
	"sync"

	"github.com/mmornati/leanproxy-mcp/pkg/registry"
)

type ServerEntry = registry.ServerEntry

type Router interface {
	Route(ctx context.Context, method string) (*ServerEntry, error)
	RouteBatch(ctx context.Context, methods []string) ([]*ServerEntry, []error)
}

type router struct {
	toolRegistry ToolRegistry
	serverReg    registry.Registry
	logger       interface{ Debug(msg string, args ...any) }
}

func NewRouter(toolRegistry ToolRegistry, serverReg registry.Registry, logger interface{ Debug(msg string, args ...any) }) Router {
	return &router{
		toolRegistry: toolRegistry,
		serverReg:    serverReg,
		logger:       logger,
	}
}

type RouteResult struct {
	Method   string
	ServerID string
	Server   *ServerEntry
	Error    error
}

func (r *router) Route(ctx context.Context, method string) (*ServerEntry, error) {
	if method == "" {
		return nil, NewRouterError(ErrCodeInternalError, "empty method name", ErrInvalidMethod)
	}

	if len(method) > 100 {
		return nil, NewRouterError(ErrCodeInternalError, "method name too long", ErrInvalidMethod)
	}

	namespace, toolName := parseMethod(method)

	if namespace == "" || toolName == "" {
		return nil, NewRouterError(ErrCodeInternalError, "invalid method format: "+method, ErrInvalidMethod)
	}

	serverIDs, err := r.toolRegistry.FindByNamespace(ctx, namespace)
	if err == nil && len(serverIDs) > 0 {
		server, err := r.serverReg.Get(ctx, serverIDs[0])
		if err == nil {
			r.logger.Debug("route: found by namespace", "method", method, "namespace", namespace, "server", server.ID)
			return server, nil
		}
	}

	fullToolName := method
	if !strings.Contains(method, ".") {
		fullToolName = namespace + "." + toolName
	}

	serverIDs, err = r.toolRegistry.FindByToolName(ctx, fullToolName)
	if err == nil && len(serverIDs) == 1 {
		server, err := r.serverReg.Get(ctx, serverIDs[0])
		if err == nil {
			r.logger.Debug("route: found by tool name fallback", "method", method, "server", server.ID)
			return server, nil
		}
	}

	if len(serverIDs) > 1 {
		return nil, NewRouterError(ErrCodeInvalidParams, "ambiguous tool: "+method, ErrAmbiguousTool)
	}

	return nil, NewRouterError(ErrCodeMethodNotFound, "tool not found: "+method, ErrToolNotFound)
}

func (r *router) RouteBatch(ctx context.Context, methods []string) ([]*ServerEntry, []error) {
	results := make([]*ServerEntry, len(methods))
	errors := make([]error, len(methods))

	resultC := make(chan *RouteResult, len(methods))

	var wg sync.WaitGroup

	for i, method := range methods {
		wg.Add(1)
		go func(idx int, m string) {
			defer wg.Done()
			server, err := r.Route(ctx, m)
			serverIDStr := ""
			if server != nil {
				serverIDStr = server.ID
			}
			resultC <- &RouteResult{
				Method:   m,
				ServerID: serverIDStr,
				Server:   server,
				Error:    err,
			}
		}(i, method)
	}

	go func() {
		wg.Wait()
		close(resultC)
	}()

	for result := range resultC {
		for i, m := range methods {
			if m == result.Method {
				results[i] = result.Server
				errors[i] = result.Error
				break
			}
		}
	}

	return results, errors
}

func parseMethod(method string) (namespace string, toolName string) {
	method = strings.TrimSpace(method)

	if strings.HasPrefix(method, ".") {
		return "", method[1:]
	}

	dotIdx := strings.Index(method, ".")
	if dotIdx == -1 {
		return method, method
	}

	if dotIdx == 0 {
		return "", method[1:]
	}

	namespace = method[:dotIdx]
	toolName = method[dotIdx+1:]

	return namespace, toolName
}
