package htmlcleaner

import (
	"strings"
	"testing"
)

func TestCleanHTMLRemovesNoiseTags(t *testing.T) {
	html := `<html><head><meta charset="utf-8"><link href="a.css"></head><body>
<header>网站标题</header><nav>首页导航</nav><p>保险产品详情</p>
<script>alert("广告")</script><style>.red{color:red}</style><footer>版权信息</footer>
<iframe>广告框</iframe><noscript>启用 JS</noscript><svg>图标</svg><img alt="照片">
</body></html>`

	text, _ := CleanHTML(html)
	for _, removed := range []string{"网站标题", "首页导航", "alert", "color", "版权信息", "广告框", "启用 JS", "图标", "照片"} {
		if strings.Contains(text, removed) {
			t.Fatalf("cleaned text contains removed content %q: %s", removed, text)
		}
	}
	if !strings.Contains(text, "保险产品详情") {
		t.Fatalf("cleaned text missing body content: %s", text)
	}
}

func TestCleanHTMLExtractsEmbeddedJSONChineseStrings(t *testing.T) {
	html := `<html><body>
<script>var staticData = {"name":"尊享医疗险","duties":[{"title":"一般医疗保险金","amount":"300万"},{"title":"English only"}]};</script>
<p>页面正文</p>
</body></html>`

	text, count := CleanHTML(html)
	for _, want := range []string{"尊享医疗险", "一般医疗保险金", "300万", "页面正文"} {
		if !strings.Contains(text, want) {
			t.Fatalf("cleaned text missing %q: %s", want, text)
		}
	}
	if count == 0 {
		t.Fatal("expected Chinese character count")
	}
}

func TestCleanHTMLCountsChineseAndHandlesEmpty(t *testing.T) {
	text, count := CleanHTML(`<p>Hello你好World世界Test测试</p>`)
	if count != 6 {
		t.Fatalf("count = %d, want 6", count)
	}
	if !strings.Contains(text, "你好World世界Test测试") {
		t.Fatalf("unexpected text: %s", text)
	}

	text, count = CleanHTML(" \n\t ")
	if text != "" || count != 0 {
		t.Fatalf("empty input = (%q, %d), want empty", text, count)
	}
}

func TestTruncateTextKeepsHeadAndTailForVeryLongChinese(t *testing.T) {
	text := strings.Repeat("头", 5500) + strings.Repeat("中", 3000) + strings.Repeat("尾", 3000)
	got := TruncateText(text, CountChinese(text))

	if !strings.Contains(got, "中间内容省略") {
		t.Fatalf("truncated text missing omission marker")
	}
	if !strings.Contains(got, "头") || !strings.Contains(got, "尾") {
		t.Fatalf("truncated text should keep head and tail")
	}
	if len(got) >= len(text) {
		t.Fatalf("truncated text length = %d, original = %d", len(got), len(text))
	}
}
