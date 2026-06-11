package productdetail

import (
	"fmt"
	"strings"

	"smartinsure-eino-backend/internal/rag/chunker"
)

func FormatSourceExcerptChunks(input DetailInput, startIndex int, opts Options, base map[string]any) ([]FormattedChunk, error) {
	size := opts.SourceChunkSize
	if size <= 0 {
		size = chunker.DefaultChunkSize
	}
	overlap := opts.SourceChunkOverlap
	if overlap <= 0 {
		overlap = chunker.DefaultOverlap
	}
	splitter, err := chunker.New(size, overlap)
	if err != nil {
		return nil, err
	}

	out := make([]FormattedChunk, 0)
	for sourceIndex, source := range input.Sources {
		if strings.TrimSpace(source.CleanedText) == "" {
			continue
		}
		sourceChunks := splitter.Split(source.CleanedText)
		for _, chunk := range sourceChunks {
			metadata := cloneMetadata(base)
			metadata["chunk_type"] = string(ChunkTypeSourceExcerpt)
			metadata["chunk_index"] = startIndex + len(out)
			metadata["source_url"] = fallbackString(source.SourceURL, canonicalURL(input))
			metadata["content_hash"] = source.ContentHash
			metadata["origin_source_type"] = source.OriginSourceType
			metadata["source_format"] = source.SourceFormat
			metadata["source_index"] = sourceIndex
			metadata["source_chunk_index"] = chunk.Index
			metadata["start_offset"] = chunk.Start
			metadata["end_offset"] = chunk.End
			if !source.FetchedAt.IsZero() {
				metadata["fetched_at"] = source.FetchedAt.UTC().Format(timeFormat)
			}

			content := fmt.Sprintf("产品名称：%s\n平台：%s\n原文片段：\n%s\n来源链接：%s",
				productName(input),
				platform(input),
				strings.TrimSpace(chunk.Text),
				fallbackString(source.SourceURL, canonicalURL(input)),
			)
			out = append(out, FormattedChunk{
				ChunkIndex: startIndex + len(out),
				ChunkType:  ChunkTypeSourceExcerpt,
				Content:    content,
				Metadata:   metadata,
			})
		}
	}
	return out, nil
}
