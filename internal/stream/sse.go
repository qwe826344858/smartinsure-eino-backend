package stream

import (
	"encoding/json"
	"fmt"
	"net/http"
)

type Event struct {
	Name string
	Data any
}

type Writer struct {
	w       http.ResponseWriter
	flusher http.Flusher
}

func NewWriter(w http.ResponseWriter) (*Writer, bool) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		return nil, false
	}
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	return &Writer{w: w, flusher: flusher}, true
}

func (w *Writer) Write(event Event) error {
	if event.Name == "" {
		event.Name = "delta"
	}
	payload, err := json.Marshal(event.Data)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w.w, "event: %s\ndata: %s\n\n", event.Name, payload); err != nil {
		return err
	}
	w.flusher.Flush()
	return nil
}
