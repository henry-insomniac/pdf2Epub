package httpapi

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

type rateBucket struct {
	tokens float64
	last   time.Time
}

type requestLimiter struct {
	mu       sync.Mutex
	perSec   float64
	burst    float64
	buckets  map[string]rateBucket
	now      func() time.Time
	lastTrim time.Time
}

func newRequestLimiter(perSec float64, burst int) *requestLimiter {
	return &requestLimiter{
		perSec:  perSec,
		burst:   float64(burst),
		buckets: make(map[string]rateBucket),
		now:     time.Now,
	}
}

func (l *requestLimiter) Allow(key string) bool {
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.lastTrim.IsZero() || now.Sub(l.lastTrim) > 10*time.Minute {
		for candidate, bucket := range l.buckets {
			if now.Sub(bucket.last) > 10*time.Minute {
				delete(l.buckets, candidate)
			}
		}
		l.lastTrim = now
	}
	bucket, ok := l.buckets[key]
	if !ok {
		bucket = rateBucket{tokens: l.burst, last: now}
	}
	bucket.tokens = min(l.burst, bucket.tokens+now.Sub(bucket.last).Seconds()*l.perSec)
	bucket.last = now
	if bucket.tokens < 1 {
		l.buckets[key] = bucket
		return false
	}
	bucket.tokens--
	l.buckets[key] = bucket
	return true
}

func (a *API) limitRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			next.ServeHTTP(w, r)
			return
		}
		if !a.limiter.Allow(clientIP(r, a.trustProxyHeaders)) {
			w.Header().Set("Retry-After", "1")
			writeError(w, http.StatusTooManyRequests, "request.rate_limited", "请求过于频繁，请稍后再试。")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func clientIP(r *http.Request, trustProxyHeaders bool) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	peer := net.ParseIP(strings.TrimSpace(host))
	if trustProxyHeaders || (peer != nil && peer.IsLoopback()) {
		for _, header := range []string{"CF-Connecting-IP", "X-Real-IP"} {
			candidate := net.ParseIP(strings.TrimSpace(r.Header.Get(header)))
			if candidate != nil {
				return candidate.String()
			}
		}
	}
	if peer == nil {
		return "unknown"
	}
	return peer.String()
}
