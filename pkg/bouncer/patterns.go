package bouncer

import "regexp"

var BuiltInPatterns = []*regexp.Regexp{
	regexp.MustCompile(`AKIA[0-9A-Z]{16}`),
	regexp.MustCompile(`ghp_[A-Za-z0-9]{36}`),
	regexp.MustCompile(`github_pat_[A-Za-z0-9_]{22,}`),
	regexp.MustCompile(`sk_live_[A-Za-z0-9]{24}`),
	regexp.MustCompile(`pk_live_[A-Za-z0-9]{24}`),
	regexp.MustCompile(`(?i)(api[_-]?key)[_-]?[=]?[A-Za-z0-9]{16,}`),
	regexp.MustCompile(`(?i)bearer\s+[A-Za-z0-9\-_]+\.[A-Za-z0-9\-_]+\.[A-Za-z0-9\-_]+`),
}