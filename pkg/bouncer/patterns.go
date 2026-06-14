package bouncer

import (
	"fmt"
	"regexp"
)

func CompilePatterns(configs []PatternConfig) ([]*regexp.Regexp, error) {
	patterns := make([]*regexp.Regexp, 0, len(configs))
	for _, c := range configs {
		re, err := SafeCompile(c.Pattern)
		if err != nil {
			return nil, fmt.Errorf("invalid pattern %q: %w", c.Name, err)
		}
		patterns = append(patterns, re)
	}
	return patterns, nil
}
