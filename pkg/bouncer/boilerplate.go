package bouncer

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"log/slog"
	"regexp"
	"strings"
)

type BoilerplateProcessor interface {
	Process(content []byte, language string) ([]byte, BoilerplateReport, error)
}

type BoilerplateReport struct {
	ImportsRedacted  int
	LicensesRedacted int
	OriginalSize     int
	ProcessedSize    int
	TokenSavings     int
}

type BoilerplatePruner struct {
	enabled        bool
	redactImports  bool
	redactLicenses bool
	customPatterns []PatternDef
	patterns       map[string][]*boilerplatePattern
}

type boilerplatePattern struct {
	name    string
	pattern *regexp.Regexp
}

var (
	goImportPattern = regexp.MustCompile(`(?m)^import\s+\([\s\S]*?^\)`)
	goSimpleImport  = regexp.MustCompile(`(?m)^import\s+"[^"]+"$`)
	pyImportPattern = regexp.MustCompile(`(?m)^(?:from\s+[\w.]+\s+import\s+[\w,*{}\[\] \t]+|import\s+[\w,*{}\[\] \t]+)$`)
	jsImportPattern = regexp.MustCompile(`(?m)^import\s+.*from\s+['"][^'"]+['"];?$`)

	copyrightPattern = regexp.MustCompile(`(?si)/\*.{0,500}?[Cc]opyright.{0,200}?\*/`)
	licensePattern   = regexp.MustCompile(`(?si)/(?:Apache|MIT|GPL)[- ]?[Ll]icense.{0,500}?\*/`)
	shebangPattern   = regexp.MustCompile(`(?m)^#!.*$`)
)

var languagePatterns = map[string][]*boilerplatePattern{
	"go": {
		{name: "IMPORTS", pattern: goImportPattern},
		{name: "IMPORTS", pattern: goSimpleImport},
	},
	"python": {
		{name: "IMPORTS", pattern: pyImportPattern},
	},
	"py": {
		{name: "IMPORTS", pattern: pyImportPattern},
	},
	"javascript": {
		{name: "IMPORTS", pattern: jsImportPattern},
	},
	"js": {
		{name: "IMPORTS", pattern: jsImportPattern},
	},
	"typescript": {
		{name: "IMPORTS", pattern: jsImportPattern},
	},
	"ts": {
		{name: "IMPORTS", pattern: jsImportPattern},
	},
}

func NewBoilerplatePruner(enabled, redactImports, redactLicenses bool, customPatterns []PatternDef) *BoilerplatePruner {
	bp := &BoilerplatePruner{
		enabled:        enabled,
		redactImports:  redactImports,
		redactLicenses: redactLicenses,
		customPatterns: customPatterns,
		patterns:       make(map[string][]*boilerplatePattern),
	}

	for lang, langPats := range languagePatterns {
		bp.patterns[lang] = langPats
	}

	for _, cp := range customPatterns {
		re, err := regexp.Compile(cp.Pattern)
		if err != nil {
			slog.Warn("invalid custom boilerplate pattern, skipping",
				"name", cp.Name,
				"pattern", cp.Pattern,
				"error", err)
			continue
		}
		bp.patterns["custom"] = append(bp.patterns["custom"], &boilerplatePattern{
			name:    cp.Name,
			pattern: re,
		})
	}

	return bp
}

func (p *BoilerplatePruner) Process(content []byte, language string) ([]byte, BoilerplateReport, error) {
	report := BoilerplateReport{
		OriginalSize: len(content),
	}

	if !p.enabled {
		report.ProcessedSize = report.OriginalSize
		return content, report, nil
	}

	result := content

	if p.redactLicenses {
		result = p.processLicenses(result, &report)
	}

	if p.redactImports {
		result = p.processImports(result, language, &report)
	}

	result = p.removeShebang(result)

	result = p.processCustomPatterns(result, &report)

	report.ProcessedSize = len(result)
	report.TokenSavings = (report.OriginalSize - report.ProcessedSize) * 4 / 3

	slog.Debug("boilerplate processing complete",
		"original_size", report.OriginalSize,
		"processed_size", report.ProcessedSize,
		"token_savings", report.TokenSavings)

	return result, report, nil
}

func (p *BoilerplatePruner) processImports(content []byte, language string, report *BoilerplateReport) []byte {
	langPatterns, ok := p.patterns[strings.ToLower(language)]
	if !ok {
		langPatterns = p.patterns["go"]
	}

	result := content
	for _, pat := range langPatterns {
		if pat.name != "IMPORTS" {
			continue
		}
		newResult := pat.pattern.ReplaceAllFunc(result, func(match []byte) []byte {
			lineCount := int64(bytes.Count(match, []byte{'\n'})) + 1
			return []byte(fmt.Sprintf("[IMPORTS_REDACTED:%d lines]", lineCount))
		})
		if !bytes.Equal(result, newResult) {
			report.ImportsRedacted++
		}
		result = newResult
	}

	return result
}

func (p *BoilerplatePruner) processLicenses(content []byte, report *BoilerplateReport) []byte {
	result := copyrightPattern.ReplaceAllFunc(content, func(match []byte) []byte {
		return []byte("[LICENSE_REDACTED]")
	})
	if !bytes.Equal(result, content) {
		report.LicensesRedacted++
	}

	content = result
	result = licensePattern.ReplaceAllFunc(content, func(match []byte) []byte {
		return []byte("[LICENSE_REDACTED]")
	})
	if !bytes.Equal(result, content) {
		report.LicensesRedacted++
	}

	return result
}

func (p *BoilerplatePruner) removeShebang(content []byte) []byte {
	return shebangPattern.ReplaceAllFunc(content, func(match []byte) []byte {
		return []byte{}
	})
}

func (p *BoilerplatePruner) processCustomPatterns(content []byte, report *BoilerplateReport) []byte {
	result := content
	for _, pat := range p.patterns["custom"] {
		result = pat.pattern.ReplaceAllFunc(result, func(match []byte) []byte {
			return []byte(fmt.Sprintf("[BOILERPLATE_REDACTED:%s]", pat.name))
		})
	}
	return result
}

func (p *BoilerplatePruner) ProcessStream(reader io.Reader, writer io.Writer, language string) (BoilerplateReport, error) {
	var report BoilerplateReport

	scanner := bufio.NewScanner(reader)
	var processed bytes.Buffer

	for scanner.Scan() {
		line := scanner.Bytes()
		processedLine, lineReport, _ := p.Process(line, language)
		report.ImportsRedacted += lineReport.ImportsRedacted
		report.LicensesRedacted += lineReport.LicensesRedacted
		processed.Write(processedLine)
		processed.WriteByte('\n')
	}

	if err := scanner.Err(); err != nil {
		return report, fmt.Errorf("boilerplate stream processing: %w", err)
	}

	result := processed.Bytes()
	report.ProcessedSize = len(result)
	report.TokenSavings = (report.OriginalSize - report.ProcessedSize) * 4 / 3

	_, err := writer.Write(result)
	if err != nil {
		return report, fmt.Errorf("boilerplate stream write: %w", err)
	}

	return report, nil
}

func DetectLanguage(filename string) string {
	filename = strings.ToLower(filename)

	if strings.HasSuffix(filename, ".go") {
		return "go"
	}
	if strings.HasSuffix(filename, ".py") {
		return "python"
	}
	if strings.HasSuffix(filename, ".js") {
		return "javascript"
	}
	if strings.HasSuffix(filename, ".ts") {
		return "typescript"
	}
	if strings.HasSuffix(filename, ".jsx") {
		return "javascript"
	}
	if strings.HasSuffix(filename, ".tsx") {
		return "typescript"
	}

	return "unknown"
}
