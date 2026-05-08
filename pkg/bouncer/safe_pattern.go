package bouncer

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
)

var (
	ErrDangerousPattern = errors.New("dangerous regex pattern detected")
)

var dangerousNestedQuantifier = regexp.MustCompile(`\(\.[+\*]\)\+`)
var dangerousNestedQuantifierStar = regexp.MustCompile(`\(\.[+\*]\)\*`)
var dangerousQuantifiedQuantifier = regexp.MustCompile(`\(\w+[+\*]\)\*`)
var dangerousCharClassQuantifier = regexp.MustCompile(`\(\[[^\]]+\]\+\)\+`)
var dangerousAlternation = regexp.MustCompile(`\(\w+\|\w+\)\*`)
var dangerousDoubleQuantifier = regexp.MustCompile(`\(\w\+\)\+`)

var validationCache struct {
	sync.RWMutex
	patterns map[string]error
}

func init() {
	validationCache.patterns = make(map[string]error)
}

func ValidatePattern(pattern string) error {
	if pattern == "" {
		return nil
	}

	validationCache.RLock()
	cached, ok := validationCache.patterns[pattern]
	validationCache.RUnlock()
	if ok {
		return cached
	}

	var err error
	if dangerousNestedQuantifier.MatchString(pattern) {
		err = fmt.Errorf("%w: nested quantifier (.+)+ or (.*)+", ErrDangerousPattern)
	} else if dangerousNestedQuantifierStar.MatchString(pattern) {
		err = fmt.Errorf("%w: nested quantifier with star (.+)* or (.*)*", ErrDangerousPattern)
	} else if dangerousQuantifiedQuantifier.MatchString(pattern) {
		err = fmt.Errorf("%w: nested quantifier (a+)* or (ab)*", ErrDangerousPattern)
	} else if dangerousCharClassQuantifier.MatchString(pattern) {
		err = fmt.Errorf("%w: nested quantifier ([a-z]+)+", ErrDangerousPattern)
	} else if dangerousAlternation.MatchString(pattern) {
		err = fmt.Errorf("%w: overlapping alternation (a|b)*", ErrDangerousPattern)
	} else if dangerousDoubleQuantifier.MatchString(pattern) {
		err = fmt.Errorf("%w: double quantifier (a+)++", ErrDangerousPattern)
	}

	validationCache.Lock()
	validationCache.patterns[pattern] = err
	validationCache.Unlock()

	return err
}

func SafeCompile(pattern string) (*regexp.Regexp, error) {
	if err := ValidatePattern(pattern); err != nil {
		return nil, err
	}
	return regexp.Compile(pattern)
}

func SafeCompileMust(pattern string) *regexp.Regexp {
	re, err := SafeCompile(pattern)
	if err != nil {
		panic("safe compile failed: " + err.Error())
	}
	return re
}

func IsPatternSafe(pattern string) bool {
	return ValidatePattern(pattern) == nil
}

func FindDangerousPatterns(patterns []string) (unsafe []string, err error) {
	for _, p := range patterns {
		if err := ValidatePattern(p); err != nil {
			unsafe = append(unsafe, fmt.Sprintf("%s: %v", p, err))
		}
	}
	return unsafe, nil
}

func StripComments(pattern string) string {
	result := strings.Builder{}
	inCharClass := false
	i := 0
	for i < len(pattern) {
		if pattern[i] == '\\' && i+1 < len(pattern) {
			result.WriteByte(pattern[i])
			result.WriteByte(pattern[i+1])
			i += 2
			continue
		}
		if pattern[i] == '[' && !inCharClass {
			inCharClass = true
			result.WriteByte(pattern[i])
			i++
			continue
		}
		if pattern[i] == ']' && inCharClass {
			inCharClass = false
			result.WriteByte(pattern[i])
			i++
			continue
		}
		if pattern[i] == '#' && !inCharClass {
			for i < len(pattern) && pattern[i] != '\n' {
				i++
			}
			continue
		}
		result.WriteByte(pattern[i])
		i++
	}
	return result.String()
}
