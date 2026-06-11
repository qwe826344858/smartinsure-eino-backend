package productdetail

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"smartinsure-eino-backend/internal/schema"
)

func TestFormatGeneratesSummaryDutyTagAndSourceChunks(t *testing.T) {
	input := sampleDetailInput()
	doc, err := Format(input, Options{
		IndexedAt:          time.Date(2026, 6, 10, 10, 0, 0, 0, time.UTC),
		SourceChunkSize:    80,
		SourceChunkOverlap: 10,
	})
	if err != nil {
		t.Fatalf("Format error = %v", err)
	}
	if doc.Namespace != DefaultNamespace || doc.SourceType != DefaultSourceType {
		t.Fatalf("namespace/source_type = %q/%q", doc.Namespace, doc.SourceType)
	}
	if doc.SourceURL != "productdetail://huize:104006:108504" {
		t.Fatalf("SourceURL = %q", doc.SourceURL)
	}

	counts := map[ChunkType]int{}
	for _, chunk := range doc.Chunks {
		counts[chunk.ChunkType]++
		if chunk.Metadata["chunk_index"] != chunk.ChunkIndex {
			t.Fatalf("chunk metadata index = %#v, want %d", chunk.Metadata["chunk_index"], chunk.ChunkIndex)
		}
	}
	if counts[ChunkTypeSummary] != 1 {
		t.Fatalf("summary count = %d, want 1", counts[ChunkTypeSummary])
	}
	if counts[ChunkTypeDuty] != len(input.Detail.Duties) {
		t.Fatalf("duty count = %d, want %d", counts[ChunkTypeDuty], len(input.Detail.Duties))
	}
	if counts[ChunkTypeTag] == 0 || counts[ChunkTypeTag] > 6 {
		t.Fatalf("tag count = %d, want 1..6", counts[ChunkTypeTag])
	}
	if counts[ChunkTypeSourceExcerpt] == 0 {
		t.Fatal("source excerpt chunks were not generated")
	}

	wantTags := []string{"医疗险", "高端", "个人", "外购药", "质子重离子"}
	gotTags := doc.Metadata["tags"].([]string)
	for _, want := range wantTags {
		if !containsString(gotTags, want) {
			t.Fatalf("tags = %#v, missing %q", gotTags, want)
		}
	}
	if !strings.Contains(doc.CleanedText, "保障责任：住院期间外购药品及外购医疗器械费用医疗保险金") {
		t.Fatalf("CleanedText missing duty chunk:\n%s", doc.CleanedText)
	}
	tags := InferTags(input)
	if len(tags.RelatedDuties["高端"]) == 0 || len(tags.RelatedDuties["医疗险"]) == 0 {
		t.Fatalf("product-level tags should carry related duties: %#v", tags.RelatedDuties)
	}
}

func TestInferTagsDoesNotOverClassifyCriticalIllness(t *testing.T) {
	input := DetailInput{
		ProductName:  "复星联合星相守2号长期医疗保险（个人版）",
		CanonicalURL: "https://example.com/product",
		Detail: schema.ProductDetail{
			ProductName: "复星联合星相守2号长期医疗保险（个人版）",
			ProductURL:  "https://example.com/product",
			Duties: []schema.DutyItem{{
				Name:        "重大疾病住院医疗保险金",
				Coverage:    "详见条款",
				Description: "重大疾病住院治疗费用",
			}},
			MatchRate: 0.91,
		},
	}
	tags := InferTags(input)
	if containsString(tags.InsuranceTags, "重疾险") {
		t.Fatalf("insurance tags over-classified as critical illness: %#v", tags.InsuranceTags)
	}
	if !containsString(tags.DutyTags, "重大疾病") {
		t.Fatalf("duty tags missing 重大疾病: %#v", tags.DutyTags)
	}
}

func TestFormatSourceExcerptChunksUsesChunkerMetadata(t *testing.T) {
	input := sampleDetailInput()
	chunks, err := FormatSourceExcerptChunks(input, 10, Options{
		SourceChunkSize:    30,
		SourceChunkOverlap: 5,
	}, map[string]any{
		"namespace":    DefaultNamespace,
		"product_key":  input.ProductKey,
		"product_name": input.ProductName,
	})
	if err != nil {
		t.Fatalf("FormatSourceExcerptChunks error = %v", err)
	}
	if len(chunks) < 2 {
		t.Fatalf("len(chunks) = %d, want at least 2", len(chunks))
	}
	if chunks[0].ChunkIndex != 10 || chunks[0].Metadata["source_chunk_index"] != 0 {
		t.Fatalf("unexpected first source chunk: %#v", chunks[0])
	}
	if chunks[0].Metadata["content_hash"] != "cleaned-source-hash" ||
		chunks[0].Metadata["origin_source_type"] != "web_page" ||
		chunks[0].Metadata["source_format"] != "html" {
		t.Fatalf("unexpected source metadata: %#v", chunks[0].Metadata)
	}
}

func TestEligibleForRAG(t *testing.T) {
	now := time.Date(2026, 6, 10, 10, 0, 0, 0, time.UTC)
	valid := sampleDetailInput()
	expiresAt := now.Add(time.Hour)
	valid.ExpiresAt = &expiresAt

	if ok, reason := Eligible(valid, 0.6, now); !ok {
		t.Fatalf("Eligible valid = false, reason=%s", reason)
	}
	invalid := valid
	invalid.Detail.MatchRate = 0.3
	if ok, reason := Eligible(invalid, 0.6, now); ok || reason == "" {
		t.Fatalf("Eligible low match = %v, reason=%q", ok, reason)
	}
	invalid = valid
	invalid.Status = "disabled"
	if ok, reason := Eligible(invalid, 0.6, now); ok || reason == "" {
		t.Fatalf("Eligible disabled = %v, reason=%q", ok, reason)
	}
	invalid = valid
	expiredAt := now.Add(-time.Second)
	invalid.ExpiresAt = &expiredAt
	if ok, reason := Eligible(invalid, 0.6, now); ok || reason == "" {
		t.Fatalf("Eligible expired = %v, reason=%q", ok, reason)
	}
}

func TestMetadataDoesNotContainDetailJSONOrUserFields(t *testing.T) {
	doc, err := Format(sampleDetailInput(), Options{})
	if err != nil {
		t.Fatalf("Format error = %v", err)
	}
	raw, err := json.Marshal(doc.Metadata)
	if err != nil {
		t.Fatalf("metadata marshal error = %v", err)
	}
	metadata := string(raw)
	for _, forbidden := range []string{"detail_json", "session_id", "anonymous_id", "message"} {
		if strings.Contains(metadata, forbidden) {
			t.Fatalf("metadata contains forbidden key %q: %s", forbidden, metadata)
		}
	}
}

func sampleDetailInput() DetailInput {
	return DetailInput{
		ProductKey:        "huize:104006:108504",
		ProductName:       "复星联合星相守2号长期医疗保险（个人版）",
		Platform:          "huize",
		CanonicalURL:      "https://www.huize.com/apps/cps/index/product/detail?prodId=104006&planId=108504",
		NormalizedURLHash: "url-hash",
		SourceHash:        "cleaned-source-hash",
		PromptVersion:     "detail_extract_v1",
		ModelName:         "extract-model",
		Status:            "active",
		Detail: schema.ProductDetail{
			ProductName: "复星联合星相守2号长期医疗保险（个人版）",
			ProductURL:  "https://www.huize.com/apps/cps/index/product/detail?prodId=104006&planId=108504",
			Platform:    "huize",
			Duties: []schema.DutyItem{
				{
					Name:        "一般住院医疗保险金",
					Coverage:    "详见条款",
					Description: "一般疾病住院医疗费用",
				},
				{
					Name:        "住院期间外购药品及外购医疗器械费用医疗保险金",
					Coverage:    "详见条款",
					Description: "住院期间处方外购药品和器械",
				},
				{
					Name:        "重大疾病住院拓展特需医疗保险金",
					Coverage:    "详见条款",
					Description: "特需病房、国际部等高端医疗区域费用，含质子重离子相关治疗",
					IsOptional:  true,
				},
			},
			CNCharCount: 1800,
			MatchRate:   0.91,
		},
		Sources: []SourceSnapshot{{
			SourceURL:        "https://www.huize.com/apps/cps/index/product/detail?prodId=104006&planId=108504",
			OriginSourceType: "web_page",
			SourceFormat:     "html",
			CleanedText:      "产品名称：复星联合星相守2号长期医疗保险（个人版）\n\n保障责任：一般住院医疗保险金，住院期间外购药品及外购医疗器械费用医疗保险金，重大疾病住院拓展特需医疗保险金。\n\n特需病房、国际部、VIP部相关医疗费用以条款为准。",
			ContentHash:      "cleaned-source-hash",
			CNCharCount:      1800,
			FetchedAt:        time.Date(2026, 6, 10, 9, 30, 0, 0, time.UTC),
		}},
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
