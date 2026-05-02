package bouncer

import (
	"fmt"
	"regexp"
)

func CompilePatterns(configs []PatternConfig) ([]*regexp.Regexp, error) {
	var patterns []*regexp.Regexp
	for _, c := range configs {
		re, err := regexp.Compile(c.Pattern)
		if err != nil {
			return nil, fmt.Errorf("invalid pattern %q: %w", c.Name, err)
		}
		patterns = append(patterns, re)
	}
	return patterns, nil
}