package httpx

import (
	"bufio"
	"errors"
	"log"
	"net"
	"net/http"
	"time"
)

// statusRecorder captures the response status code for logging.
type statusRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (s *statusRecorder) WriteHeader(code int) {
	if !s.wroteHeader {
		s.status = code
		s.wroteHeader = true
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if !s.wroteHeader {
		s.status = http.StatusOK
		s.wroteHeader = true
	}
	return s.ResponseWriter.Write(b)
}

func (s *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if hj, ok := s.ResponseWriter.(http.Hijacker); ok {
		return hj.Hijack()
	}
	return nil, nil, errors.New("underlying ResponseWriter does not support hijack")
}

// LogFunc receives one access-log line's fields. Injectable for testing.
type LogFunc func(method, path string, status int, dur time.Duration)

// RequestLoggerWith wraps a handler, invoking log for each request.
func RequestLoggerWith(logFn LogFunc) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rec, r)
			logFn(r.Method, r.URL.Path, rec.status, time.Since(start))
		})
	}
}

// RequestLogger logs each request to the standard logger.
func RequestLogger(next http.Handler) http.Handler {
	return RequestLoggerWith(func(method, path string, status int, dur time.Duration) {
		log.Printf("%s %s %d %s", method, path, status, dur)
	})(next)
}
