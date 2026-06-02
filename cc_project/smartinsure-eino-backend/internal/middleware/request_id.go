package middleware

import (
	"context"
	"crypto/rand"
	"fmt"
	"log"
	"net/http"
	"time"
)

type contextKey string

const requestIDKey contextKey = "request_id"

func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := r.Header.Get("X-Request-Id")
		if requestID == "" {
			requestID = newRequestID()
		}

		ctx := context.WithValue(r.Context(), requestIDKey, requestID)
		w.Header().Set("X-Request-Id", requestID)

		start := time.Now()
		rw := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}
		next.ServeHTTP(rw, r.WithContext(ctx))

		log.Printf(
			"method=%s path=%s status=%d duration_ms=%.1f request_id=%s",
			r.Method,
			r.URL.Path,
			rw.statusCode,
			float64(time.Since(start).Microseconds())/1000,
			requestID,
		)
	})
}

func RequestIDFromContext(ctx context.Context) string {
	requestID, _ := ctx.Value(requestIDKey).(string)
	return requestID
}

func newRequestID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

type responseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (w *responseWriter) WriteHeader(statusCode int) {
	w.statusCode = statusCode
	w.ResponseWriter.WriteHeader(statusCode)
}

func (w *responseWriter) Flush() {
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (w *responseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}
