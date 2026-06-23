package cache

import (
	"encoding/json"
	"fmt"
	"log/slog"
)

type InjectStrategy string

const (
	StrategyOff        InjectStrategy = "off"
	StrategyAggressive InjectStrategy = "aggressive"
	StrategyBalanced   InjectStrategy = "balanced"
)

type BreakpointInjector struct {
	logger   *slog.Logger
	strategy InjectStrategy
}

type BreakpointInjectorOption func(*BreakpointInjector)

func WithInjectLogger(logger *slog.Logger) BreakpointInjectorOption {
	return func(inj *BreakpointInjector) {
		if logger != nil {
			inj.logger = logger
		}
	}
}

func WithStrategy(strategy InjectStrategy) BreakpointInjectorOption {
	return func(inj *BreakpointInjector) {
		inj.strategy = strategy
	}
}

func NewBreakpointInjector(opts ...BreakpointInjectorOption) *BreakpointInjector {
	inj := &BreakpointInjector{
		logger:   slog.Default(),
		strategy: StrategyAggressive,
	}
	for _, opt := range opts {
		opt(inj)
	}
	return inj
}

func (b *BreakpointInjector) Inject(body []byte) ([]byte, error) {
	if len(body) == 0 {
		return nil, fmt.Errorf("breakpoint injector: empty body")
	}

	if b.strategy == StrategyOff {
		return body, nil
	}

	var req map[string]interface{}
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, fmt.Errorf("breakpoint injector: unmarshal: %w", err)
	}

	if req == nil {
		return nil, fmt.Errorf("breakpoint injector: body is not a JSON object")
	}

	switch b.strategy {
	case StrategyAggressive:
		b.injectSystem(req)
		b.injectTools(req)
	case StrategyBalanced:
		b.injectBalanced(req)
	}

	result, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("breakpoint injector: marshal: %w", err)
	}
	return result, nil
}

func (b *BreakpointInjector) injectSystem(req map[string]interface{}) {
	systemRaw, ok := req["system"]
	if !ok {
		return
	}
	system, ok := systemRaw.([]interface{})
	if !ok || len(system) == 0 {
		return
	}
	last := system[len(system)-1].(map[string]interface{})
	if b.hasCacheControl(last) {
		b.logger.Debug("cache_control: user-supplied, skipping system")
		return
	}
	last["cache_control"] = map[string]interface{}{"type": "ephemeral"}
}

func (b *BreakpointInjector) injectTools(req map[string]interface{}) {
	toolsRaw, ok := req["tools"]
	if !ok {
		return
	}
	tools, ok := toolsRaw.([]interface{})
	if !ok || len(tools) == 0 {
		return
	}
	last := tools[len(tools)-1].(map[string]interface{})
	if b.hasCacheControl(last) {
		b.logger.Debug("cache_control: user-supplied, skipping tools")
		return
	}
	last["cache_control"] = map[string]interface{}{"type": "ephemeral"}
}

func (b *BreakpointInjector) injectBalanced(req map[string]interface{}) {
	systemSize := b.blockSize(req, "system")
	toolsSize := b.blockSize(req, "tools")

	if systemSize == 0 && toolsSize == 0 {
		return
	}
	if systemSize == 0 {
		b.injectTools(req)
		return
	}
	if toolsSize == 0 {
		b.injectSystem(req)
		return
	}

	systemRaw, _ := req["system"]
	system := systemRaw.([]interface{})
	lastSys := system[len(system)-1].(map[string]interface{})
	sysHasCC := b.hasCacheControl(lastSys)

	toolsRaw, _ := req["tools"]
	tools := toolsRaw.([]interface{})
	lastTool := tools[len(tools)-1].(map[string]interface{})
	toolsHasCC := b.hasCacheControl(lastTool)

	if sysHasCC && toolsHasCC {
		return
	}
	if sysHasCC {
		b.injectTools(req)
		return
	}
	if toolsHasCC {
		b.injectSystem(req)
		return
	}

	if systemSize >= toolsSize {
		lastSys["cache_control"] = map[string]interface{}{"type": "ephemeral"}
	} else {
		lastTool["cache_control"] = map[string]interface{}{"type": "ephemeral"}
	}
}

func (b *BreakpointInjector) blockSize(req map[string]interface{}, key string) int {
	raw, ok := req[key]
	if !ok {
		return 0
	}
	arr, ok := raw.([]interface{})
	if !ok || len(arr) == 0 {
		return 0
	}
	data, err := json.Marshal(arr)
	if err != nil {
		return 0
	}
	return len(data)
}

func (b *BreakpointInjector) hasCacheControl(item map[string]interface{}) bool {
	_, ok := item["cache_control"]
	return ok
}
