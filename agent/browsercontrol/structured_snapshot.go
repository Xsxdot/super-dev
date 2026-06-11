// Package browsercontrol builds AI-oriented page snapshots without arbitrary evaluate.
//
// 职责：
//   - 将页面 DOM 中常见可交互元素转换为稳定 selector 摘要
//   - 对 token/password/cookie/localStorage 等敏感文本做脱敏
//
// 边界：
//   - 不返回 input value
//   - 不读取 cookie、localStorage 或 response body
package browsercontrol

import (
	"regexp"
	"strings"
)

const defaultSnapshotMaxElements = 100
const maxSnapshotElements = 200

var (
	sensitiveSnapshotAssignment = regexp.MustCompile(`(?i)(^|[\s"'[{(,;.])(?:[a-z0-9_-]*token|password|passwd|cookie|localstorage)\s*[:=]\s*\S+`)
	sensitiveBearerToken        = regexp.MustCompile(`(?i)\bbearer\s+[a-z0-9._~+/=-]{8,}`)
)

type snapshotElementInput struct {
	Tag        string
	Role       string
	Text       string
	AriaLabel  string
	DataTest   string
	DataTestID string
	ID         string
	NameAttr   string
	Type       string
	Visible    bool
	Enabled    bool
	Bounds     *SnapshotBounds
}

func redactSnapshotText(text string) string {
	trimmed := strings.TrimSpace(text)
	if sensitiveSnapshotAssignment.MatchString(trimmed) || sensitiveBearerToken.MatchString(trimmed) {
		return "[redacted]"
	}
	return trimmed
}

func normalizeSnapshotMaxElements(value int) int {
	if value <= 0 {
		return defaultSnapshotMaxElements
	}
	if value > maxSnapshotElements {
		return maxSnapshotElements
	}
	return value
}

func buildSnapshotElement(input snapshotElementInput) SnapshotElement {
	role := strings.TrimSpace(input.Role)
	tag := normalizeSnapshotTag(input.Tag)
	if role == "" {
		role = roleFromSnapshotTag(tag)
	}
	name := redactSnapshotText(snapshotElementName(input))
	return SnapshotElement{
		Role:     role,
		Name:     name,
		Selector: buildElementSelector(input),
		Visible:  input.Visible,
		Enabled:  input.Enabled,
		Bounds:   input.Bounds,
	}
}

func buildElementSelector(el snapshotElementInput) string {
	tag := normalizeSnapshotTag(el.Tag)
	if value, ok := snapshotSelectorValue(el.DataTest); ok {
		return `[data-test="` + cssString(value) + `"]`
	}
	if value, ok := snapshotSelectorValue(el.DataTestID); ok {
		return `[data-testid="` + cssString(value) + `"]`
	}
	if value, ok := snapshotSelectorValue(el.AriaLabel); ok {
		return selectorTag(tag) + `[aria-label="` + cssString(value) + `"]`
	}
	if value, ok := snapshotSelectorValue(el.NameAttr); ok {
		return selectorTag(tag) + `[name="` + cssString(value) + `"]`
	}
	if value, ok := snapshotSelectorValue(el.ID); ok {
		return `#` + cssIdentifier(value)
	}
	if role, ok := snapshotSelectorValue(el.Role); ok {
		return selectorTag(tag) + `[role="` + cssString(role) + `"]`
	}
	return selectorTag(tag)
}

func snapshotSelectorValue(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if value == "" || redactSnapshotText(value) == "[redacted]" {
		return "", false
	}
	return value, true
}

func snapshotElementName(input snapshotElementInput) string {
	if strings.EqualFold(strings.TrimSpace(input.Type), "password") {
		return "[redacted]"
	}
	for _, value := range []string{input.AriaLabel, input.Text, input.NameAttr, input.ID} {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func normalizeSnapshotTag(tag string) string {
	tag = strings.ToLower(strings.TrimSpace(tag))
	if tag == "" {
		return "element"
	}
	return tag
}

func roleFromSnapshotTag(tag string) string {
	switch tag {
	case "a":
		return "link"
	case "button":
		return "button"
	case "input", "textarea":
		return "textbox"
	case "select":
		return "combobox"
	default:
		return tag
	}
}

func selectorTag(tag string) string {
	if tag == "" || tag == "element" {
		return "*"
	}
	return tag
}

func cssString(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	return strings.ReplaceAll(value, `"`, `\"`)
}

func cssIdentifier(value string) string {
	var out strings.Builder
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			out.WriteRune(r)
			continue
		}
		out.WriteByte('\\')
		out.WriteRune(r)
	}
	return out.String()
}
