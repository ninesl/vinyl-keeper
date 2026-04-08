package router

import (
	"log"
	"net/http"
	"time"
)

type responseWriter struct {
	http.ResponseWriter
	statusCode int
	wroteHdr   bool
}

func (rw *responseWriter) WriteHeader(code int) {
	if !rw.wroteHdr {
		rw.wroteHdr = true
		rw.statusCode = code
	}
	rw.ResponseWriter.WriteHeader(code)
}

func (rw *responseWriter) Write(b []byte) (int, error) {
	if !rw.wroteHdr {
		rw.wroteHdr = true
		rw.statusCode = http.StatusOK
	}
	return rw.ResponseWriter.Write(b)
}

func LoggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		wrapped := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}

		next.ServeHTTP(wrapped, r)

		duration := time.Since(start)
		log.Printf("[%s] %s %d %s", r.Method, r.URL.Path, wrapped.statusCode, duration)
	})
}

// htmxTriggerWriter injects an HX-Trigger header before the first write
// on any 2xx response, merging with any existing HX-Trigger value.
type htmxTriggerWriter struct {
	http.ResponseWriter
	event    string
	wroteHdr bool
}

func (tw *htmxTriggerWriter) WriteHeader(code int) {
	if !tw.wroteHdr {
		tw.wroteHdr = true
		if code >= 200 && code < 300 {
			tw.injectTrigger()
		}
	}
	tw.ResponseWriter.WriteHeader(code)
}

func (tw *htmxTriggerWriter) Write(b []byte) (int, error) {
	if !tw.wroteHdr {
		tw.wroteHdr = true
		tw.injectTrigger() // implicit 200
	}
	return tw.ResponseWriter.Write(b)
}

func (tw *htmxTriggerWriter) injectTrigger() {
	h := tw.ResponseWriter.Header()
	if existing := h.Get("HX-Trigger"); existing != "" {
		h.Set("HX-Trigger", existing+", "+tw.event)
	} else {
		h.Set("HX-Trigger", tw.event)
	}
}

// HTMXTrigger returns middleware that appends the given event name to the
// HX-Trigger response header on every 2xx response from the wrapped handler.
// Wrap any mutating endpoint with this to broadcast a client-side event.
func HTMXTrigger(event string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(&htmxTriggerWriter{ResponseWriter: w, event: event}, r)
		})
	}
}
