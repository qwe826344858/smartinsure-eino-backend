package stream

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWriterWritesNamedJSONEvent(t *testing.T) {
	recorder := httptest.NewRecorder()
	writer, ok := NewWriter(recorder)
	if !ok {
		t.Fatal("httptest recorder should support flushing")
	}

	err := writer.Write(Event{Name: "delta", Data: map[string]string{"text": "hello"}})
	if err != nil {
		t.Fatalf("write event: %v", err)
	}

	body := recorder.Body.String()
	if !strings.Contains(body, "event: delta\n") {
		t.Fatalf("missing event name: %q", body)
	}
	if !strings.Contains(body, `data: {"text":"hello"}`) {
		t.Fatalf("missing JSON data: %q", body)
	}
	if got := recorder.Header().Get("Content-Type"); !strings.Contains(got, "text/event-stream") {
		t.Fatalf("unexpected content type: %q", got)
	}
}
