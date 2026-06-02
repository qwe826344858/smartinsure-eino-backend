package htmlcleaner

import (
	"encoding/json"
	"fmt"
	"html"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	MaxCNChars  = 5000
	TailCNChars = 1000
)

var (
	embeddedJSONRE = regexp.MustCompile(`(?s)var\s+[A-Za-z_$][A-Za-z0-9_$]*\s*=\s*(\{.+?\})\s*;`)
	htmlCommentRE  = regexp.MustCompile(`(?s)<!--.*?-->`)
	htmlTagRE      = regexp.MustCompile(`(?s)<[^>]+>`)
	blankLinesRE   = regexp.MustCompile(`\n{3,}`)
	spaceRE        = regexp.MustCompile(`[ \t\f\v]+`)
	separatorTagRE = regexp.MustCompile(`(?i)<\s*/?\s*(br|p|div|li|tr|td|th|section|article|main|h[1-6]|ul|ol|table|tbody|thead|dl|dt|dd)\b[^>]*>`)
)

var containerRemoveTags = []string{
	"script", "style", "nav", "footer", "header", "iframe", "noscript", "svg",
}

var voidRemoveTags = []string{
	"img", "link", "meta",
}

// CleanHTML converts raw HTML into LLM-friendly text and returns the original
// Chinese character count before truncation.
func CleanHTML(rawHTML string) (string, int) {
	rawHTML = strings.TrimSpace(rawHTML)
	if rawHTML == "" {
		return "", 0
	}

	embeddedText := ExtractEmbeddedJSONText(rawHTML)
	visibleText := ExtractVisibleText(rawHTML)
	text := visibleText
	if embeddedText != "" {
		text = embeddedText + "\n\n" + visibleText
	}
	text = strings.TrimSpace(blankLinesRE.ReplaceAllString(text, "\n\n"))

	cnCount := CountChinese(text)
	return TruncateText(text, cnCount), cnCount
}

// Clean is a short alias kept for call sites that do not need Python parity in
// the function name.
func Clean(rawHTML string) (string, int) {
	return CleanHTML(rawHTML)
}

// ExtractVisibleText removes noisy tags and returns normalized page text.
func ExtractVisibleText(rawHTML string) string {
	text := htmlCommentRE.ReplaceAllString(rawHTML, "\n")
	for _, tag := range containerRemoveTags {
		text = removeContainerTag(text, tag)
	}
	for _, tag := range voidRemoveTags {
		text = removeVoidTag(text, tag)
	}
	text = separatorTagRE.ReplaceAllString(text, "\n")
	text = htmlTagRE.ReplaceAllString(text, "\n")
	text = html.UnescapeString(text)
	return normalizeLines(text)
}

// ExtractEmbeddedJSONText extracts Chinese strings from "var name = {...};"
// payloads before script tags are removed.
func ExtractEmbeddedJSONText(rawHTML string) string {
	var results []string
	for _, match := range embeddedJSONRE.FindAllStringSubmatch(rawHTML, -1) {
		if len(match) < 2 || CountChinese(match[1]) == 0 {
			continue
		}
		var data any
		if err := json.Unmarshal([]byte(match[1]), &data); err != nil {
			continue
		}
		results = append(results, extractChineseStrings(data, 0)...)
	}
	return strings.Join(results, "\n")
}

func removeContainerTag(text, tag string) string {
	pattern := fmt.Sprintf(`(?is)<\s*%s\b[^>]*>.*?<\s*/\s*%s\s*>`, regexp.QuoteMeta(tag), regexp.QuoteMeta(tag))
	re := regexp.MustCompile(pattern)
	return re.ReplaceAllString(text, "\n")
}

func removeVoidTag(text, tag string) string {
	pattern := fmt.Sprintf(`(?is)<\s*%s\b[^>]*(?:/)?\s*>`, regexp.QuoteMeta(tag))
	re := regexp.MustCompile(pattern)
	return re.ReplaceAllString(text, "\n")
}

func normalizeLines(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	lines := strings.Split(text, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(spaceRE.ReplaceAllString(line, " "))
		if line != "" {
			out = append(out, line)
		}
	}
	return strings.Join(out, "\n")
}

func extractChineseStrings(obj any, depth int) []string {
	if depth > 10 {
		return nil
	}
	switch value := obj.(type) {
	case string:
		if CountChinese(value) > 0 && utf8.RuneCountInString(value) < 500 {
			return []string{strings.TrimSpace(value)}
		}
	case []any:
		var out []string
		for _, item := range value {
			out = append(out, extractChineseStrings(item, depth+1)...)
		}
		return out
	case map[string]any:
		var out []string
		for _, item := range value {
			out = append(out, extractChineseStrings(item, depth+1)...)
		}
		return out
	}
	return nil
}

func CountChinese(text string) int {
	count := 0
	for _, r := range text {
		if isChinese(r) {
			count++
		}
	}
	return count
}

func TruncateText(text string, cnCount int) string {
	if cnCount <= MaxCNChars {
		return text
	}

	headEnd := findCNPosition(text, MaxCNChars)
	if cnCount <= MaxCNChars*2 {
		return text[:headEnd]
	}

	tailStart := findCNPositionReverse(text, TailCNChars)
	return text[:headEnd] + "\n\n...(中间内容省略)...\n\n" + text[tailStart:]
}

func findCNPosition(text string, n int) int {
	count := 0
	for i, r := range text {
		if !isChinese(r) {
			continue
		}
		count++
		if count >= n {
			return i + utf8.RuneLen(r)
		}
	}
	return len(text)
}

func findCNPositionReverse(text string, n int) int {
	count := 0
	for i := len(text); i > 0; {
		r, size := utf8.DecodeLastRuneInString(text[:i])
		i -= size
		if r == utf8.RuneError && size == 0 {
			break
		}
		if !isChinese(r) {
			continue
		}
		count++
		if count >= n {
			return i
		}
	}
	return 0
}

func isChinese(r rune) bool {
	return unicode.In(r, unicode.Han) && r >= '\u4e00' && r <= '\u9fff'
}
