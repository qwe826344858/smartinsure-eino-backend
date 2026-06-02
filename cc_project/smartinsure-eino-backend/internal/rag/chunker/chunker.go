package chunker

import (
	"errors"
	"strings"
	"unicode/utf8"
)

const (
	DefaultChunkSize = 1200
	DefaultOverlap   = 200
)

type Chunk struct {
	Index int    `json:"index"`
	Start int    `json:"start"`
	End   int    `json:"end"`
	Text  string `json:"text"`
}

type Chunker struct {
	chunkSize int
	overlap   int
}

type paragraph struct {
	text  string
	start int
	end   int
}

func New(chunkSize, overlap int) (*Chunker, error) {
	if chunkSize <= 0 {
		return nil, errors.New("chunk_size must be greater than 0")
	}
	if overlap < 0 {
		return nil, errors.New("overlap must not be negative")
	}
	if overlap >= chunkSize {
		return nil, errors.New("overlap must be smaller than chunk_size")
	}
	return &Chunker{chunkSize: chunkSize, overlap: overlap}, nil
}

func Default() *Chunker {
	chunker, _ := New(DefaultChunkSize, DefaultOverlap)
	return chunker
}

func (c *Chunker) ChunkSize() int {
	if c == nil {
		return DefaultChunkSize
	}
	return c.chunkSize
}

func (c *Chunker) Overlap() int {
	if c == nil {
		return DefaultOverlap
	}
	return c.overlap
}

func (c *Chunker) Split(text string) []Chunk {
	if c == nil {
		c = Default()
	}

	_, paragraphs := normalize(text)
	if len(paragraphs) == 0 {
		return nil
	}

	chunks := make([]Chunk, 0, len(paragraphs))
	bufferText := ""
	bufferStart := 0
	bufferEnd := 0

	flush := func() {
		if strings.TrimSpace(bufferText) == "" {
			return
		}
		chunks = append(chunks, Chunk{
			Index: len(chunks),
			Start: bufferStart,
			End:   bufferEnd,
			Text:  bufferText,
		})
		bufferText = ""
		bufferStart = 0
		bufferEnd = 0
	}

	for _, para := range paragraphs {
		if runeLen(para.text) > c.chunkSize {
			flush()
			chunks = append(chunks, c.splitLongParagraph(para, len(chunks))...)
			continue
		}

		if bufferText == "" {
			bufferText = para.text
			bufferStart = para.start
			bufferEnd = para.end
			continue
		}

		candidate := bufferText + "\n\n" + para.text
		if runeLen(candidate) <= c.chunkSize {
			bufferText = candidate
			bufferEnd = para.end
			continue
		}

		flush()
		bufferText = para.text
		bufferStart = para.start
		bufferEnd = para.end
	}
	flush()

	return chunks
}

func normalize(text string) (string, []paragraph) {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")

	lines := strings.Split(text, "\n")
	var builder strings.Builder
	paragraphs := make([]paragraph, 0, len(lines))
	runePos := 0

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if builder.Len() > 0 {
			builder.WriteString("\n\n")
			runePos += 2
		}

		start := runePos
		builder.WriteString(line)
		runePos += runeLen(line)
		paragraphs = append(paragraphs, paragraph{text: line, start: start, end: runePos})
	}

	return builder.String(), paragraphs
}

func (c *Chunker) splitLongParagraph(para paragraph, startIndex int) []Chunk {
	runes := []rune(para.text)
	chunks := make([]Chunk, 0, len(runes)/c.chunkSize+1)
	start := 0

	for start < len(runes) {
		end := start + c.chunkSize
		if end > len(runes) {
			end = len(runes)
		}

		chunks = append(chunks, Chunk{
			Index: startIndex + len(chunks),
			Start: para.start + start,
			End:   para.start + end,
			Text:  string(runes[start:end]),
		})

		if end >= len(runes) {
			break
		}
		nextStart := end - c.overlap
		if nextStart <= start {
			nextStart = start + 1
		}
		start = nextStart
	}

	return chunks
}

func runeLen(text string) int {
	return utf8.RuneCountInString(text)
}
