package chunker

import (
	"strings"
	"testing"
)

func TestNewValidatesOptions(t *testing.T) {
	tests := []struct {
		name      string
		size      int
		overlap   int
		wantError bool
	}{
		{name: "ok", size: 10, overlap: 2},
		{name: "zero size", size: 0, overlap: 0, wantError: true},
		{name: "negative overlap", size: 10, overlap: -1, wantError: true},
		{name: "equal overlap", size: 10, overlap: 10, wantError: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := New(tt.size, tt.overlap)
			if (err != nil) != tt.wantError {
				t.Fatalf("New() error=%v wantError=%v", err, tt.wantError)
			}
		})
	}
}

func TestSplitPrefersParagraphs(t *testing.T) {
	chunker, err := New(12, 2)
	if err != nil {
		t.Fatal(err)
	}

	chunks := chunker.Split(" 第一段文字 \n\n第二段\n\n第三段很长")
	if len(chunks) != 2 {
		t.Fatalf("len(chunks)=%d", len(chunks))
	}
	if chunks[0].Text != "第一段文字\n\n第二段" {
		t.Fatalf("unexpected first chunk: %q", chunks[0].Text)
	}
	if chunks[0].Index != 0 || chunks[0].Start != 0 || chunks[0].End != 10 {
		t.Fatalf("unexpected first chunk offsets: %+v", chunks[0])
	}
	if chunks[1].Text != "第三段很长" || chunks[1].Index != 1 || chunks[1].Start != 12 || chunks[1].End != 17 {
		t.Fatalf("unexpected second chunk: %+v", chunks[1])
	}
}

func TestSplitLongParagraphWithOverlap(t *testing.T) {
	chunker, err := New(5, 2)
	if err != nil {
		t.Fatal(err)
	}

	chunks := chunker.Split("一二三四五六七八九十")
	if len(chunks) != 3 {
		t.Fatalf("len(chunks)=%d", len(chunks))
	}
	got := []string{chunks[0].Text, chunks[1].Text, chunks[2].Text}
	want := []string{"一二三四五", "四五六七八", "七八九十"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("chunk %d text=%q want=%q", i, got[i], want[i])
		}
	}
	if chunks[1].Start != 3 || chunks[1].End != 8 {
		t.Fatalf("unexpected overlap offsets: %+v", chunks[1])
	}
}

func TestSplitNormalizesBlankLines(t *testing.T) {
	chunker, err := New(20, 2)
	if err != nil {
		t.Fatal(err)
	}

	chunks := chunker.Split("\r\n  A  \n\n\n B \r\n")
	if len(chunks) != 1 {
		t.Fatalf("len(chunks)=%d", len(chunks))
	}
	if chunks[0].Text != "A\n\nB" {
		t.Fatalf("unexpected normalized text: %q", chunks[0].Text)
	}
	if strings.Contains(chunks[0].Text, "\r") {
		t.Fatalf("text still contains CR: %q", chunks[0].Text)
	}
}
