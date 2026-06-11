package productdetail

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"smartinsure-eino-backend/internal/schema"
)

const timeFormat = time.RFC3339

func Format(input DetailInput, opts Options) (FormattedDocument, error) {
	if strings.TrimSpace(productName(input)) == "" {
		return FormattedDocument{}, errors.New("product name is empty")
	}
	if len(input.Detail.Duties) == 0 {
		return FormattedDocument{}, errors.New("duties must not be empty")
	}

	namespace := fallbackString(opts.Namespace, fallbackString(input.Namespace, DefaultNamespace))
	sourceType := fallbackString(opts.SourceType, DefaultSourceType)
	indexedAt := opts.IndexedAt
	if indexedAt.IsZero() {
		indexedAt = time.Now().UTC()
	}
	tags := InferTags(input)
	baseMetadata := commonMetadata(input, namespace, sourceType, tags, indexedAt)

	chunks := make([]FormattedChunk, 0, len(input.Detail.Duties)+1+len(tags.Tags)+len(input.Sources))
	summary := formatSummaryChunk(input, tags, baseMetadata)
	chunks = append(chunks, summary)
	for _, duty := range input.Detail.Duties {
		chunks = append(chunks, formatDutyChunk(input, duty, len(chunks), baseMetadata))
	}
	for _, tag := range limitedTags(tags.Tags, opts.MaxTagChunks) {
		chunks = append(chunks, formatTagChunk(input, tag, tags, len(chunks), baseMetadata))
	}
	sourceChunks, err := FormatSourceExcerptChunks(input, len(chunks), opts, baseMetadata)
	if err != nil {
		return FormattedDocument{}, err
	}
	chunks = append(chunks, sourceChunks...)

	return FormattedDocument{
		Namespace:   namespace,
		SourceType:  sourceType,
		SourceURL:   documentSourceURL(input),
		Title:       productName(input),
		CleanedText: formattedDocumentText(chunks),
		Metadata:    baseMetadata,
		Chunks:      chunks,
	}, nil
}

func Eligible(input DetailInput, minMatchRate float64, now time.Time) (bool, string) {
	if strings.TrimSpace(productName(input)) == "" {
		return false, "product_name is empty"
	}
	if strings.TrimSpace(input.ProductKey) == "" && strings.TrimSpace(canonicalURL(input)) == "" {
		return false, "product identity is empty"
	}
	if len(input.Detail.Duties) == 0 {
		return false, "duties is empty"
	}
	if minMatchRate > 0 && input.Detail.MatchRate < minMatchRate {
		return false, "match_rate below threshold"
	}
	if status := strings.TrimSpace(input.Status); status != "" && status != "active" {
		return false, "status is not active"
	}
	if input.ExpiresAt != nil {
		if now.IsZero() {
			now = time.Now().UTC()
		}
		if !input.ExpiresAt.After(now) {
			return false, "detail expired"
		}
	}
	return true, ""
}

func formatSummaryChunk(input DetailInput, tags TagSet, base map[string]any) FormattedChunk {
	metadata := cloneMetadata(base)
	metadata["chunk_type"] = string(ChunkTypeSummary)
	metadata["chunk_index"] = 0

	var b strings.Builder
	b.WriteString("产品名称：")
	b.WriteString(productName(input))
	b.WriteString("\n平台：")
	b.WriteString(platform(input))
	if input.ProductKey != "" {
		b.WriteString("\n商品唯一键：")
		b.WriteString(input.ProductKey)
	}
	if len(tags.Tags) > 0 {
		b.WriteString("\n产品标签：")
		b.WriteString(strings.Join(tags.Tags, "、"))
	}
	b.WriteString("\n来源链接：")
	b.WriteString(canonicalURL(input))
	b.WriteString("\n保障责任列表：")
	for _, duty := range input.Detail.Duties {
		b.WriteString("\n- ")
		if duty.IsOptional {
			b.WriteString("【可选】")
		} else {
			b.WriteString("【必选】")
		}
		b.WriteString(strings.TrimSpace(duty.Name))
		if strings.TrimSpace(duty.Coverage) != "" {
			b.WriteString("：")
			b.WriteString(strings.TrimSpace(duty.Coverage))
		}
	}
	return FormattedChunk{ChunkIndex: 0, ChunkType: ChunkTypeSummary, Content: b.String(), Metadata: metadata}
}

func formatDutyChunk(input DetailInput, duty schema.DutyItem, index int, base map[string]any) FormattedChunk {
	tags := dutyTagsForDuty(duty)
	metadata := cloneMetadata(base)
	metadata["chunk_type"] = string(ChunkTypeDuty)
	metadata["chunk_index"] = index
	metadata["duty_index"] = index - 1
	metadata["duty_name"] = strings.TrimSpace(duty.Name)
	metadata["coverage"] = strings.TrimSpace(duty.Coverage)
	metadata["is_optional"] = duty.IsOptional
	metadata["duty_tags"] = tags

	optional := "否"
	if duty.IsOptional {
		optional = "是"
	}
	content := fmt.Sprintf("产品名称：%s\n平台：%s\n产品标签：%s\n保障责任：%s\n保额/限额：%s\n是否可选：%s\n责任说明：%s\n来源链接：%s",
		productName(input),
		platform(input),
		strings.Join(asStringSlice(base["tags"]), "、"),
		strings.TrimSpace(duty.Name),
		fallbackString(duty.Coverage, "详见条款"),
		optional,
		strings.TrimSpace(duty.Description),
		canonicalURL(input),
	)
	return FormattedChunk{ChunkIndex: index, ChunkType: ChunkTypeDuty, Content: content, Metadata: metadata}
}

func formatTagChunk(input DetailInput, tag string, tags TagSet, index int, base map[string]any) FormattedChunk {
	category := tags.Category(tag)
	relatedDuties := tags.RelatedDuties[tag]
	metadata := cloneMetadata(base)
	metadata["chunk_type"] = string(ChunkTypeTag)
	metadata["chunk_index"] = index
	metadata["tag_name"] = tag
	metadata["tag_category"] = category
	metadata["related_duties"] = relatedDuties

	content := fmt.Sprintf("产品名称：%s\n平台：%s\n标签：%s\n标签分类：%s\n适用说明：%s\n相关保障责任：%s\n来源链接：%s",
		productName(input),
		platform(input),
		tag,
		category,
		tagReason(tag, category),
		strings.Join(relatedDuties, "、"),
		canonicalURL(input),
	)
	return FormattedChunk{ChunkIndex: index, ChunkType: ChunkTypeTag, Content: content, Metadata: metadata}
}

func commonMetadata(input DetailInput, namespace, sourceType string, tags TagSet, indexedAt time.Time) map[string]any {
	requiredCount, optionalCount := dutyCounts(input.Detail.Duties)
	metadata := map[string]any{
		"namespace":           namespace,
		"source_type":         sourceType,
		"product_key":         strings.TrimSpace(input.ProductKey),
		"product_name":        productName(input),
		"platform":            platform(input),
		"source_url":          canonicalURL(input),
		"canonical_url":       canonicalURL(input),
		"normalized_url_hash": strings.TrimSpace(input.NormalizedURLHash),
		"duty_count":          len(input.Detail.Duties),
		"required_duty_count": requiredCount,
		"optional_duty_count": optionalCount,
		"match_rate":          input.Detail.MatchRate,
		"prompt_version":      strings.TrimSpace(input.PromptVersion),
		"model_name":          strings.TrimSpace(input.ModelName),
		"source_hash":         strings.TrimSpace(input.SourceHash),
		"tags":                tags.Tags,
		"insurance_tags":      tags.InsuranceTags,
		"market_tags":         tags.MarketTags,
		"audience_tags":       tags.AudienceTags,
		"duty_tags":           tags.DutyTags,
		"quality_tags":        tags.QualityTags,
		"indexed_at":          indexedAt.UTC().Format(timeFormat),
	}
	if input.ExpiresAt != nil {
		metadata["expires_at"] = input.ExpiresAt.UTC().Format(timeFormat)
	}
	return metadata
}

func formattedDocumentText(chunks []FormattedChunk) string {
	parts := make([]string, 0, len(chunks))
	for _, chunk := range chunks {
		parts = append(parts, strings.TrimSpace(chunk.Content))
	}
	return strings.Join(parts, "\n\n")
}

func limitedTags(tags []string, max int) []string {
	if max <= 0 {
		max = 6
	}
	if len(tags) <= max {
		return tags
	}
	return tags[:max]
}

func dutyCounts(duties []schema.DutyItem) (required, optional int) {
	for _, duty := range duties {
		if duty.IsOptional {
			optional++
		} else {
			required++
		}
	}
	return required, optional
}

func productName(input DetailInput) string {
	return strings.TrimSpace(fallbackString(input.ProductName, input.Detail.ProductName))
}

func platform(input DetailInput) string {
	return strings.TrimSpace(fallbackString(input.Platform, input.Detail.Platform))
}

func canonicalURL(input DetailInput) string {
	return strings.TrimSpace(fallbackString(input.CanonicalURL, input.Detail.ProductURL))
}

func documentSourceURL(input DetailInput) string {
	if strings.TrimSpace(input.ProductKey) != "" {
		return "productdetail://" + strings.TrimSpace(input.ProductKey)
	}
	return canonicalURL(input)
}

func fallbackString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func cloneMetadata(in map[string]any) map[string]any {
	out := make(map[string]any, len(in)+4)
	for key, value := range in {
		out[key] = value
	}
	return out
}

func asStringSlice(value any) []string {
	switch typed := value.(type) {
	case []string:
		return typed
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if text, ok := item.(string); ok {
				out = append(out, text)
			}
		}
		return out
	default:
		return nil
	}
}

func tagReason(tag, category string) string {
	switch tag {
	case "医疗险":
		return "该产品名称或保障责任包含医疗、住院医疗、门急诊或药品费用相关内容。"
	case "重疾险":
		return "该产品名称明确包含重疾险、重大疾病保险或疾病保险相关表述。"
	case "意外险":
		return "该产品名称或责任包含意外、伤残、意外医疗或意外身故相关内容。"
	case "年金险":
		return "该产品名称包含年金或养老年金相关内容。"
	case "高端":
		return "该产品名称或保障责任包含高端、中高端、特需、国际部、VIP或私立医院相关内容。"
	case "性价比":
		return "该产品公开文本明确出现性价比、普惠、惠民或低保费相关表述。"
	case "家庭":
		return "该产品公开文本包含家庭、全家、家享或亲子相关表述。"
	case "个人":
		return "该产品公开文本包含个人或个人版相关表述。"
	case "儿童":
		return "该产品公开文本包含少儿、儿童、宝宝、学生或青少年相关表述。"
	case "老人":
		return "该产品公开文本包含老人、老年、中老年或银发相关表述。"
	case "企业":
		return "该产品公开文本包含企业、团体、员工或雇主相关表述。"
	default:
		if category == "duty_tags" {
			return "该产品的保障责任名称、保额或说明中包含该标签对应的保障关键词。"
		}
		return "该标签来自产品名称、保障责任或公开文本中的确定性关键词。"
	}
}
