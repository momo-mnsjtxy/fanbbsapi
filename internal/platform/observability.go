package platform

import (
	"encoding/json"
	"log"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
)

// SecurityHeaders applies browser-safe defaults without assuming a deployment
// origin or enabling browser credentials.
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'; base-uri 'none'")
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

type limitBucket struct {
	windowStart time.Time
	lastSeen    time.Time
	count       int
}

// RateLimiter is a bounded, process-local fixed-window limiter. It is meant to
// reduce accidental abuse in the local slice, not replace a production edge.
type RateLimiter struct {
	mu      sync.Mutex
	buckets map[string]limitBucket
	maxKeys int
	limit   int
	window  time.Duration
	now     func() time.Time
}

func NewRateLimiter(maxKeys, limit int, window time.Duration) *RateLimiter {
	return &RateLimiter{buckets: map[string]limitBucket{}, maxKeys: maxKeys, limit: limit, window: window, now: time.Now}
}

func (l *RateLimiter) Allow(key string) (bool, int) {
	key = strings.TrimSpace(key)
	if key == "" {
		key = "anonymous"
	}
	now := l.now().UTC()
	l.mu.Lock()
	defer l.mu.Unlock()
	for candidate, bucket := range l.buckets {
		if now.Sub(bucket.windowStart) >= l.window {
			delete(l.buckets, candidate)
		}
	}
	_, existing := l.buckets[key]
	if !existing && len(l.buckets) >= l.maxKeys {
		var oldestKey string
		var oldest time.Time
		for candidate, bucket := range l.buckets {
			if oldestKey == "" || bucket.lastSeen.Before(oldest) {
				oldestKey, oldest = candidate, bucket.lastSeen
			}
		}
		delete(l.buckets, oldestKey)
	}
	bucket, ok := l.buckets[key]
	if !ok {
		bucket = limitBucket{windowStart: now}
	}
	bucket.lastSeen = now
	if bucket.count >= l.limit {
		retry := int((l.window - now.Sub(bucket.windowStart)).Seconds())
		if retry < 1 {
			retry = 1
		}
		l.buckets[key] = bucket
		return false, retry
	}
	bucket.count++
	l.buckets[key] = bucket
	return true, 0
}

func CheckRateLimit(limiter *RateLimiter, key string) error {
	allowed, retry := limiter.Allow(key)
	if allowed {
		return nil
	}
	return &AppError{Status: http.StatusTooManyRequests, Code: "rate_limit_exceeded", Message: "请求过于频繁，请稍后重试", RetryAfter: retry}
}

func RemoteHost(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil && host != "" {
		return host
	}
	return r.RemoteAddr
}

type metricKey struct {
	Method string
	Route  string
	Status int
}

type Metrics struct {
	mu       sync.Mutex
	started  time.Time
	requests map[metricKey]uint64
	duration map[metricKey]uint64
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *statusWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusWriter) Write(body []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(body)
}

type MetricRow struct {
	Method     string `json:"method"`
	Route      string `json:"route"`
	Status     int    `json:"status"`
	Count      uint64 `json:"count"`
	DurationMS uint64 `json:"duration_ms"`
}

type MetricsSnapshot struct {
	UptimeSeconds int64       `json:"uptime_seconds"`
	Requests      []MetricRow `json:"requests"`
}

func NewMetrics() *Metrics {
	return &Metrics{started: time.Now().UTC(), requests: map[metricKey]uint64{}, duration: map[metricKey]uint64{}}
}

func (m *Metrics) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		wrapped := &statusWriter{ResponseWriter: w}
		next.ServeHTTP(wrapped, r)
		status := wrapped.status
		if status == 0 {
			status = http.StatusOK
		}
		route := chi.RouteContext(r.Context()).RoutePattern()
		if route == "" {
			route = "unmatched"
		}
		key := metricKey{Method: r.Method, Route: route, Status: status}
		duration := time.Since(started)
		m.mu.Lock()
		m.requests[key]++
		m.duration[key] += uint64(duration.Milliseconds())
		m.mu.Unlock()
		record, _ := json.Marshal(map[string]any{
			"event": "http_request", "request_id": RequestID(r.Context()), "method": r.Method,
			"route": route, "status": status, "duration_ms": duration.Milliseconds(),
		})
		log.Print(string(record))
	})
}

func (m *Metrics) Snapshot() MetricsSnapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	rows := make([]MetricRow, 0, len(m.requests))
	for key, count := range m.requests {
		rows = append(rows, MetricRow{Method: key.Method, Route: key.Route, Status: key.Status, Count: count, DurationMS: m.duration[key]})
	}
	sort.Slice(rows, func(i, j int) bool {
		left := rows[i].Method + rows[i].Route + strconv.Itoa(rows[i].Status)
		right := rows[j].Method + rows[j].Route + strconv.Itoa(rows[j].Status)
		return left < right
	})
	return MetricsSnapshot{UptimeSeconds: int64(time.Since(m.started).Seconds()), Requests: rows}
}
