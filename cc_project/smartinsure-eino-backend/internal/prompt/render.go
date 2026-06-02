package prompt

import (
	"bytes"
	"strings"
	"text/template"
)

var templateFuncs = template.FuncMap{
	"join": strings.Join,
}

func Render(source string, data any) (string, error) {
	tpl, err := template.New("prompt").Funcs(templateFuncs).Parse(source)
	if err != nil {
		return "", err
	}
	var out bytes.Buffer
	if err := tpl.Execute(&out, data); err != nil {
		return "", err
	}
	return out.String(), nil
}

func BuildIntentUserPrompt(message string) (string, error) {
	return BuildIntentUserPromptWithHistory(message, "")
}

func BuildIntentUserPromptWithHistory(message string, historyContext string) (string, error) {
	return Render(IntentTemplate, struct {
		Message        string
		HistoryContext string
	}{Message: message, HistoryContext: historyContext})
}

func BuildQueryUserPrompt(message, intent string) (string, error) {
	return Render(QueryTemplate, struct {
		Message string
		Intent  string
	}{Message: message, Intent: intent})
}
