package registry

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

const (
	TrustLow    = "low"
	TrustMedium = "medium"
	TrustHigh   = "high"

	trustWarningThreshold = 40
)

func CalculateTrustScore(entry RegistryFeedEntry) int {
	if entry.TrustScore > 0 {
		return entry.TrustScore
	}

	hasData := entry.LastRelease != "" ||
		entry.OpenIssues > 0 ||
		entry.Downloads > 0 ||
		entry.Description != "" ||
		len(entry.Categories) > 0

	if !hasData && entry.TrustScore == 0 {
		return 100
	}

	score := 0

	if entry.LastRelease != "" {
		score += releaseRecencyScore(entry.LastRelease)
	}

	if entry.OpenIssues >= 0 {
		score += issueScore(entry.OpenIssues)
	}

	if entry.Downloads > 0 {
		score += downloadScore(entry.Downloads)
	}

	if entry.Description != "" {
		score += 5
	}
	if len(entry.Categories) > 0 {
		score += 5
	}
	if entry.Command != "" || entry.URL != "" {
		score += 5
	}

	if score > 100 {
		score = 100
	}
	return score
}

func releaseRecencyScore(lastRelease string) int {
	parsed := tryParseDate(lastRelease)
	if parsed == nil {
		return 5
	}
	days := int(time.Since(*parsed).Hours() / 24)
	if days < 0 {
		return 30
	}
	switch {
	case days <= 30:
		return 30
	case days <= 90:
		return 20
	case days <= 365:
		return 10
	default:
		return 5
	}
}

func tryParseDate(s string) *time.Time {
	formats := []string{
		time.RFC3339,
		"2006-01-02",
		"2006-01-02T15:04:05",
		"2006-01-02T15:04:05Z",
		"January 2, 2006",
		"Jan 2, 2006",
	}
	for _, f := range formats {
		t, err := time.Parse(f, s)
		if err == nil {
			return &t
		}
	}
	return nil
}

func issueScore(openIssues int) int {
	switch {
	case openIssues == 0:
		return 30
	case openIssues < 5:
		return 25
	case openIssues < 20:
		return 15
	case openIssues < 100:
		return 5
	default:
		return 0
	}
}

func downloadScore(downloads int) int {
	switch {
	case downloads >= 100000:
		return 25
	case downloads >= 10000:
		return 20
	case downloads >= 1000:
		return 15
	case downloads >= 100:
		return 10
	case downloads > 0:
		return 5
	default:
		return 0
	}
}

func TrustLevel(score int) string {
	switch {
	case score >= 70:
		return TrustHigh
	case score >= trustWarningThreshold:
		return TrustMedium
	default:
		return TrustLow
	}
}

func FormatString(v string) string {
	if v == "" {
		return "-"
	}
	return v
}

func FormatInt(v int) string {
	if v <= 0 {
		return "-"
	}
	return strconv.Itoa(v)
}

func FormatInt64(v int64) string {
	if v <= 0 {
		return "-"
	}
	return strconv.FormatInt(v, 10)
}

func FormatTrustLabel(score int) string {
	return fmt.Sprintf("%d (%s)", score, TrustLevel(score))
}

func IsLowTrust(score int) bool {
	return score < trustWarningThreshold
}

func FormatWarning(sid string, score int) string {
	var b strings.Builder
	b.WriteString("⚠  WARNING: Server ")
	b.WriteString(sid)
	b.WriteString(" has a low trust score (")
	b.WriteString(strconv.Itoa(score))
	b.WriteString("/100).\n")
	b.WriteString("  This could indicate an abandoned or untrusted package.\n")
	b.WriteString("  To install anyway, re-run with: --i-understand-the-risks")
	return b.String()
}
