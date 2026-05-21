package logparse

import (
	"regexp"
	"strings"
)

// levelFieldRe matches structured log fields such as level=error or level="warning".
var levelFieldRe = regexp.MustCompile(`(?i)\blevel\s*=\s*"?([a-z]+)"?`)

// bracketLevelRe matches [ERROR], [WARN], etc.
var bracketLevelRe = regexp.MustCompile(`\[(ERROR|WARN|WARNING|INFO|DEBUG|TRACE|FATAL|CRITICAL)\]`)

// DetectLevel infers a log level string (ERROR, WARN, INFO, DEBUG) from a raw line.
//
// Priority:
//  1. Structured level= field (logrus, zap text, etc.)
//  2. Bracketed level markers ([ERROR], [WARN], ...)
//  3. Keyword scan (error > warn > debug > default info)
func DetectLevel(line string) string {
	if lv := levelFromField(line); lv != "" {
		return lv
	}
	if lv := levelFromBrackets(line); lv != "" {
		return lv
	}
	return levelFromKeywords(line)
}

func levelFromField(line string) string {
	m := levelFieldRe.FindStringSubmatch(line)
	if len(m) < 2 {
		return ""
	}
	return normalizeLevel(m[1])
}

func levelFromBrackets(line string) string {
	upper := strings.ToUpper(line)
	m := bracketLevelRe.FindStringSubmatch(upper)
	if len(m) < 2 {
		return ""
	}
	return normalizeLevel(m[1])
}

func levelFromKeywords(line string) string {
	upper := strings.ToUpper(line)

	if strings.Contains(upper, "ERROR") ||
		strings.Contains(upper, "FATAL") ||
		strings.Contains(upper, "CRITICAL") ||
		strings.Contains(upper, "PANIC") {
		return "ERROR"
	}
	if strings.Contains(upper, "WARN") || strings.Contains(upper, "WARNING") {
		return "WARN"
	}
	if strings.Contains(upper, "DEBUG") || strings.Contains(upper, "TRACE") {
		return "DEBUG"
	}
	return "INFO"
}

func normalizeLevel(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "error", "err", "fatal", "critical", "panic":
		return "ERROR"
	case "warn", "warning":
		return "WARN"
	case "debug", "trace":
		return "DEBUG"
	case "info":
		return "INFO"
	default:
		return ""
	}
}
