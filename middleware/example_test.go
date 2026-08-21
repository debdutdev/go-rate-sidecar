package middleware_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"time"

	ratelimiter "github.com/debdutdev/rate-limiter"
	"github.com/debdutdev/rate-limiter/algorithm"
	"github.com/debdutdev/rate-limiter/middleware"
	"github.com/debdutdev/rate-limiter/store"
)

func ExampleHTTP() {
	mem := store.NewMemory(time.Minute)
	defer mem.Close()

	limiter, _ := algorithm.NewTokenBucket(ratelimiter.Config{
		Rate:  10,
		Burst: 2,
	}, mem)
	defer limiter.Close()

	handler := middleware.HTTP(middleware.HTTPConfig{
		Limiter: limiter,
	})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("OK"))
	}))

	for i := 0; i < 3; i++ {
		req := httptest.NewRequest("GET", "/api/data", nil)
		req.RemoteAddr = "192.0.2.1:1234"
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		fmt.Printf("request %d: status=%d remaining=%s\n",
			i+1, rec.Code, rec.Header().Get("X-RateLimit-Remaining"))
	}
	// Output:
	// request 1: status=200 remaining=1
	// request 2: status=200 remaining=0
	// request 3: status=429 remaining=0
}
