package compliance

import "strings"

var ProhibitedPhrases = []string{
	"保证收益",
	"稳赚不赔",
	"100%赔付",
	"百分百赔付",
	"什么都保",
	"这款一定最好",
	"绝对最好",
	"肯定能赔",
	"必须买",
	"不买就亏",
	"限时",
	"抢购",
}

var ReplacementMap = map[string]string{
	"保证收益":   "预期收益（具体以合同为准）",
	"稳赚不赔":   "具有一定保障（具体以合同条款为准）",
	"100%赔付": "按合同约定赔付",
	"百分百赔付":  "按合同约定赔付",
	"什么都保":   "保障范围较广（具体以合同为准）",
	"这款一定最好": "这款产品具有一定优势",
	"绝对最好":   "这款产品具有一定优势",
	"肯定能赔":   "符合条款约定的情况下可理赔",
}

type Issue struct {
	Phrase      string `json:"phrase"`
	Replacement string `json:"replacement,omitempty"`
}

type Result struct {
	Compliant bool    `json:"compliant"`
	Issues    []Issue `json:"issues"`
}

type Validator struct {
	phrases      []string
	replacements map[string]string
}

var DefaultValidator = NewValidator()

func NewValidator() Validator {
	phrases := append([]string(nil), ProhibitedPhrases...)
	replacements := make(map[string]string, len(ReplacementMap))
	for phrase, replacement := range ReplacementMap {
		replacements[phrase] = replacement
	}
	return Validator{phrases: phrases, replacements: replacements}
}

func Issues(text string) []Issue {
	return DefaultValidator.Issues(text)
}

func Validate(text string) Result {
	return DefaultValidator.Validate(text)
}

func Sanitize(text string) string {
	return DefaultValidator.Sanitize(text)
}

func (v Validator) Issues(text string) []Issue {
	if text == "" {
		return []Issue{}
	}

	issues := make([]Issue, 0)
	for _, phrase := range v.phrasesOrDefault() {
		if phrase == "" || !strings.Contains(text, phrase) {
			continue
		}
		issue := Issue{Phrase: phrase}
		if replacement, ok := v.replacementFor(phrase); ok {
			issue.Replacement = replacement
		}
		issues = append(issues, issue)
	}
	return issues
}

func (v Validator) Validate(text string) Result {
	issues := v.Issues(text)
	return Result{
		Compliant: len(issues) == 0,
		Issues:    issues,
	}
}

func (v Validator) Sanitize(text string) string {
	result := text
	for _, phrase := range v.phrasesOrDefault() {
		if phrase == "" || !strings.Contains(result, phrase) {
			continue
		}
		replacement, _ := v.replacementFor(phrase)
		result = strings.ReplaceAll(result, phrase, replacement)
	}
	return result
}

func (v Validator) phrasesOrDefault() []string {
	if v.phrases == nil {
		return ProhibitedPhrases
	}
	return v.phrases
}

func (v Validator) replacementFor(phrase string) (string, bool) {
	if v.replacements == nil {
		replacement, ok := ReplacementMap[phrase]
		return replacement, ok
	}
	replacement, ok := v.replacements[phrase]
	return replacement, ok
}
