package registry

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

const (
	// TrustUnverified is the level assigned to a score of 0: no signal
	// LeanProxy can independently verify was present at all. It is
	// distinct from TrustLow (some signal was present but weak) so the
	// display can tell "we checked and it looks risky" apart from "we have
	// nothing to check".
	TrustUnverified = "unverified"
	TrustLow        = "low"
	TrustMedium     = "medium"
	TrustHigh       = "high"

	trustWarningThreshold = 40
	highTrustThreshold    = 70

	scoreMax               = 100
	scoreRecent            = 30
	scoreWithinQuarter     = 20
	scoreWithinYear        = 10
	scoreUnknownDate       = 0
	scoreNoOpenIssues      = 30
	scoreFewOpenIssues     = 25
	scoreSomeOpenIssues    = 15
	scoreManyOpenIssues    = 5
	scorePopularDownloads  = 25
	scoreWellUsedDownloads = 20
	scoreModerateDownloads = 15
	scoreFewDownloads      = 10
	scoreSomeDownloads     = 5
	scoreNamespaceVerified = 25
	scoreHasLicense        = 15

	futureDateTolerance = 24 * time.Hour
)

// CalculateTrustScore computes a trust score from 0-100 using only signals
// LeanProxy itself can observe or independently verify:
//
//   - registry namespace verification status (entry.NamespaceVerified) —
//     the registry API confirmed the publisher owns the namespace (DNS or
//     GitHub verification for the official MCP Registry);
//   - presence of a license (entry.License);
//   - recency of the last release;
//   - open issue count (as a maintenance signal);
//   - download count.
//
// A feed-provided trust_score (entry.TrustScore) is never used: whoever
// authors the feed does not get to grade their own homework (issue #313).
// An entry with no verifiable signal at all scores 0, labeled "unverified"
// (see TrustLevel), never a high score by default.
func CalculateTrustScore(entry RegistryFeedEntry) int {
	score := 0
	if entry.NamespaceVerified {
		score += scoreNamespaceVerified
	}
	if entry.License != "" {
		score += scoreHasLicense
	}
	if entry.LastRelease != "" {
		score += releaseRecencyScore(entry.LastRelease)
	}
	if entry.OpenIssues > 0 {
		score += issueScore(entry.OpenIssues)
	}
	if entry.Downloads > 0 {
		score += downloadScore(entry.Downloads)
	}

	if score > scoreMax {
		score = scoreMax
	}
	if score < 0 {
		score = 0
	}
	return score
}

// TrustSignal is one individually-displayable input to the trust score.
type TrustSignal struct {
	Name    string
	Present bool
	Detail  string
}

// TrustSignals returns the individual signals behind entry's trust score, in
// a fixed display order, so callers can show "why" a score is what it is
// instead of only the number (issue #313 acceptance criterion: "Display the
// individual signals, not just a number").
func TrustSignals(entry RegistryFeedEntry) []TrustSignal {
	return []TrustSignal{
		{Name: "namespace verified", Present: entry.NamespaceVerified, Detail: FormatString("")},
		{Name: "license", Present: entry.License != "", Detail: FormatString(entry.License)},
		{Name: "last release", Present: entry.LastRelease != "", Detail: FormatLastRelease(entry.LastRelease)},
		{Name: "open issues", Present: entry.OpenIssues > 0, Detail: FormatInt(entry.OpenIssues)},
		{Name: "downloads", Present: entry.Downloads > 0, Detail: FormatInt(entry.Downloads)},
	}
}

func releaseRecencyScore(lastRelease string) int {
	parsed := tryParseDate(lastRelease)
	if parsed == nil {
		return scoreUnknownDate
	}
	delta := time.Since(*parsed)
	if delta < 0 {
		if -delta <= futureDateTolerance {
			return scoreRecent
		}
		return scoreUnknownDate
	}
	days := int(delta.Hours() / 24)
	switch {
	case days <= 30:
		return scoreRecent
	case days <= 90:
		return scoreWithinQuarter
	case days <= 365:
		return scoreWithinYear
	default:
		return scoreUnknownDate
	}
}

func tryParseDate(s string) *time.Time {
	formats := []string{
		time.RFC3339,
		"2006-01-02",
		"2006-01-02T15:04:05",
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
	case openIssues <= 0:
		return scoreNoOpenIssues
	case openIssues < 5:
		return scoreFewOpenIssues
	case openIssues < 20:
		return scoreSomeOpenIssues
	case openIssues < 100:
		return scoreManyOpenIssues
	default:
		return 0
	}
}

func downloadScore(downloads int) int {
	switch {
	case downloads <= 0:
		return 0
	case downloads >= 100000:
		return scorePopularDownloads
	case downloads >= 10000:
		return scoreWellUsedDownloads
	case downloads >= 1000:
		return scoreModerateDownloads
	case downloads >= 100:
		return scoreFewDownloads
	default:
		return scoreSomeDownloads
	}
}

func TrustLevel(score int) string {
	switch {
	case score <= 0:
		return TrustUnverified
	case score >= highTrustThreshold:
		return TrustHigh
	case score >= trustWarningThreshold:
		return TrustMedium
	default:
		return TrustLow
	}
}

// FormatString renders v as-is when non-empty, otherwise "-" to signal
// unavailable data. Used for columns where empty is the natural absence.
func FormatString(v string) string {
	if v == "" {
		return "-"
	}
	return v
}

// FormatLastRelease renders a registry last-release string. Empty strings
// and unparseable dates are both rendered as "-" so the search column does
// not conflate "missing" with "garbage". Parseable dates are returned
// verbatim so consumers can apply their own locale formatting later.
func FormatLastRelease(v string) string {
	if v == "" {
		return "-"
	}
	if tryParseDate(v) == nil {
		return "-"
	}
	return v
}

// FormatInt renders v as a decimal string, substituting "-" only when v
// is negative. Zero is a legitimate signal (e.g. zero open issues) and is
// preserved.
func FormatInt(v int) string {
	if v < 0 {
		return "-"
	}
	return strconv.Itoa(v)
}

// FormatInt64 mirrors FormatInt for int64.
func FormatInt64(v int64) string {
	if v < 0 {
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
	b.WriteString("[WARN] Server ")
	b.WriteString(sid)
	b.WriteString(" has a low trust score (")
	b.WriteString(strconv.Itoa(score))
	b.WriteString("/100).\n")
	b.WriteString("  This could indicate an abandoned or untrusted package.\n")
	b.WriteString("  To install anyway, re-run with: --i-understand-the-risks")
	return b.String()
}
