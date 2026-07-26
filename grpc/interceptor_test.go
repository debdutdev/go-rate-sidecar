package grpc

import (
	"context"
	"net"
	"testing"

	ratelimiter "github.com/12345debdut/rate-limiter"
	"github.com/12345debdut/rate-limiter/algorithm"
	"github.com/12345debdut/rate-limiter/store"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	pb "google.golang.org/grpc/health/grpc_health_v1"
)

const bufSize = 1024 * 1024

func newTestLimiter(t *testing.T, burst int64) ratelimiter.Limiter {
	t.Helper()
	mem := store.NewMemory(0)
	t.Cleanup(func() { mem.Close() })
	limiter, err := algorithm.NewTokenBucket(ratelimiter.Config{Rate: 10, Burst: burst}, mem)
	if err != nil {
		t.Fatalf("NewTokenBucket: %v", err)
	}
	return limiter
}

// healthServer implements the gRPC Health service for testing.
type healthServer struct {
	pb.UnimplementedHealthServer
}

func (s *healthServer) Check(_ context.Context, _ *pb.HealthCheckRequest) (*pb.HealthCheckResponse, error) {
	return &pb.HealthCheckResponse{Status: pb.HealthCheckResponse_SERVING}, nil
}

func (s *healthServer) Watch(req *pb.HealthCheckRequest, stream pb.Health_WatchServer) error {
	if err := stream.Send(&pb.HealthCheckResponse{Status: pb.HealthCheckResponse_SERVING}); err != nil {
		return err
	}
	// Block until client cancels.
	<-stream.Context().Done()
	return nil
}

func startServer(t *testing.T, opts ...grpc.ServerOption) (*grpc.Server, *bufconn.Listener) {
	t.Helper()
	lis := bufconn.Listen(bufSize)
	srv := grpc.NewServer(opts...)
	pb.RegisterHealthServer(srv, &healthServer{})
	go srv.Serve(lis)
	t.Cleanup(func() { srv.Stop() })
	return srv, lis
}

func dial(t *testing.T, lis *bufconn.Listener) *grpc.ClientConn {
	t.Helper()
	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
	)
	if err != nil {
		t.Fatalf("grpc.NewClient: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

func TestUnary_AllowedRequest(t *testing.T) {
	limiter := newTestLimiter(t, 5)
	_, lis := startServer(t, grpc.UnaryInterceptor(
		UnaryServerInterceptor(InterceptorConfig{Limiter: limiter}),
	))

	conn := dial(t, lis)
	client := pb.NewHealthClient(conn)

	var trailer metadata.MD
	resp, err := client.Check(context.Background(), &pb.HealthCheckRequest{},
		grpc.Trailer(&trailer))
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if resp.Status != pb.HealthCheckResponse_SERVING {
		t.Errorf("status=%v, want SERVING", resp.Status)
	}

	// Verify rate limit metadata in trailer.
	limit := trailer.Get("x-ratelimit-limit")
	if len(limit) == 0 || limit[0] != "5" {
		t.Errorf("x-ratelimit-limit=%v, want [5]", limit)
	}

	remaining := trailer.Get("x-ratelimit-remaining")
	if len(remaining) == 0 || remaining[0] != "4" {
		t.Errorf("x-ratelimit-remaining=%v, want [4]", remaining)
	}
}

func TestUnary_DeniedRequest(t *testing.T) {
	limiter := newTestLimiter(t, 2)
	_, lis := startServer(t, grpc.UnaryInterceptor(
		UnaryServerInterceptor(InterceptorConfig{Limiter: limiter}),
	))

	conn := dial(t, lis)
	client := pb.NewHealthClient(conn)

	// Exhaust the bucket.
	for i := 0; i < 2; i++ {
		_, err := client.Check(context.Background(), &pb.HealthCheckRequest{})
		if err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
	}

	// This should be denied.
	var trailer metadata.MD
	_, err := client.Check(context.Background(), &pb.HealthCheckRequest{},
		grpc.Trailer(&trailer))
	if err == nil {
		t.Fatal("expected error on denied request")
	}

	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("expected gRPC status error, got %v", err)
	}
	if st.Code() != codes.ResourceExhausted {
		t.Errorf("code=%v, want ResourceExhausted", st.Code())
	}

	remaining := trailer.Get("x-ratelimit-remaining")
	if len(remaining) == 0 || remaining[0] != "0" {
		t.Errorf("x-ratelimit-remaining=%v, want [0]", remaining)
	}
}

func TestUnary_SkipFunc(t *testing.T) {
	limiter := newTestLimiter(t, 1)
	_, lis := startServer(t, grpc.UnaryInterceptor(
		UnaryServerInterceptor(InterceptorConfig{
			Limiter: limiter,
			SkipFunc: func(fullMethod string) bool {
				return fullMethod == "/grpc.health.v1.Health/Check"
			},
		}),
	))

	conn := dial(t, lis)
	client := pb.NewHealthClient(conn)

	// Should always pass because Health/Check is skipped.
	for i := 0; i < 10; i++ {
		_, err := client.Check(context.Background(), &pb.HealthCheckRequest{})
		if err != nil {
			t.Fatalf("request %d should be skipped: %v", i, err)
		}
	}
}

func TestUnary_CustomKeyExtractor(t *testing.T) {
	limiter := newTestLimiter(t, 1)
	_, lis := startServer(t, grpc.UnaryInterceptor(
		UnaryServerInterceptor(InterceptorConfig{
			Limiter:      limiter,
			KeyExtractor: MetadataExtractor("x-api-key"),
		}),
	))

	conn := dial(t, lis)
	client := pb.NewHealthClient(conn)

	// Request with key "abc" — allowed.
	ctx := metadata.AppendToOutgoingContext(context.Background(), "x-api-key", "abc")
	_, err := client.Check(ctx, &pb.HealthCheckRequest{})
	if err != nil {
		t.Fatalf("first request: %v", err)
	}

	// Same key again — denied (burst=1).
	ctx = metadata.AppendToOutgoingContext(context.Background(), "x-api-key", "abc")
	_, err = client.Check(ctx, &pb.HealthCheckRequest{})
	if err == nil {
		t.Fatal("second request with same key should be denied")
	}
	st, _ := status.FromError(err)
	if st.Code() != codes.ResourceExhausted {
		t.Errorf("code=%v, want ResourceExhausted", st.Code())
	}

	// Different key "xyz" — allowed (independent bucket).
	ctx = metadata.AppendToOutgoingContext(context.Background(), "x-api-key", "xyz")
	_, err = client.Check(ctx, &pb.HealthCheckRequest{})
	if err != nil {
		t.Errorf("different key should be allowed: %v", err)
	}
}

func TestUnary_MethodExtractor(t *testing.T) {
	limiter := newTestLimiter(t, 1)
	_, lis := startServer(t, grpc.UnaryInterceptor(
		UnaryServerInterceptor(InterceptorConfig{
			Limiter:      limiter,
			KeyExtractor: MethodExtractor(),
		}),
	))

	conn := dial(t, lis)
	client := pb.NewHealthClient(conn)

	// First call — allowed.
	_, err := client.Check(context.Background(), &pb.HealthCheckRequest{})
	if err != nil {
		t.Fatalf("first request: %v", err)
	}

	// Second call same method — denied (burst=1, keyed by method).
	_, err = client.Check(context.Background(), &pb.HealthCheckRequest{})
	if err == nil {
		t.Fatal("second request should be denied (same method)")
	}
	st, _ := status.FromError(err)
	if st.Code() != codes.ResourceExhausted {
		t.Errorf("code=%v, want ResourceExhausted", st.Code())
	}
}

func TestUnary_FailOpen(t *testing.T) {
	_, lis := startServer(t, grpc.UnaryInterceptor(
		UnaryServerInterceptor(InterceptorConfig{Limiter: &failingLimiter{}}),
	))

	conn := dial(t, lis)
	client := pb.NewHealthClient(conn)

	// Should succeed even though limiter errors — fail-open.
	resp, err := client.Check(context.Background(), &pb.HealthCheckRequest{})
	if err != nil {
		t.Fatalf("should fail-open: %v", err)
	}
	if resp.Status != pb.HealthCheckResponse_SERVING {
		t.Errorf("status=%v, want SERVING", resp.Status)
	}
}

func TestStream_DeniedRequest(t *testing.T) {
	limiter := newTestLimiter(t, 1)
	_, lis := startServer(t, grpc.StreamInterceptor(
		StreamServerInterceptor(InterceptorConfig{Limiter: limiter}),
	))

	conn := dial(t, lis)
	client := pb.NewHealthClient(conn)

	// First watch — allowed (consumes the one token).
	ctx1, cancel1 := context.WithCancel(context.Background())
	stream1, err := client.Watch(ctx1, &pb.HealthCheckRequest{})
	if err != nil {
		t.Fatalf("first Watch: %v", err)
	}
	// Receive at least one response to confirm stream is open.
	_, err = stream1.Recv()
	if err != nil {
		t.Fatalf("first Recv: %v", err)
	}
	cancel1()

	// Second watch — should be denied.
	stream2, err := client.Watch(context.Background(), &pb.HealthCheckRequest{})
	if err != nil {
		// Some versions return error at stream creation.
		st, ok := status.FromError(err)
		if ok && st.Code() == codes.ResourceExhausted {
			return
		}
		t.Fatalf("unexpected error: %v", err)
	}
	// Others return error on first Recv.
	_, err = stream2.Recv()
	if err == nil {
		t.Fatal("second stream should be denied")
	}
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("expected gRPC status error, got %v", err)
	}
	if st.Code() != codes.ResourceExhausted {
		t.Errorf("code=%v, want ResourceExhausted", st.Code())
	}
}

func TestStream_SkipFunc(t *testing.T) {
	limiter := newTestLimiter(t, 1)
	_, lis := startServer(t, grpc.StreamInterceptor(
		StreamServerInterceptor(InterceptorConfig{
			Limiter: limiter,
			SkipFunc: func(fullMethod string) bool {
				return true
			},
		}),
	))

	conn := dial(t, lis)
	client := pb.NewHealthClient(conn)

	// All calls skipped, should always succeed.
	for i := 0; i < 5; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		stream, err := client.Watch(ctx, &pb.HealthCheckRequest{})
		if err != nil {
			t.Fatalf("Watch %d: %v", i, err)
		}
		_, err = stream.Recv()
		if err != nil {
			t.Fatalf("Recv %d: %v", i, err)
		}
		cancel()
	}
}

// failingLimiter always returns an error, used to test fail-open behavior.
type failingLimiter struct{}

func (f *failingLimiter) Allow(_ context.Context, _ string) (ratelimiter.Result, error) {
	return ratelimiter.Result{}, context.DeadlineExceeded
}

func (f *failingLimiter) AllowN(_ context.Context, _ string, _ int64) (ratelimiter.Result, error) {
	return ratelimiter.Result{}, context.DeadlineExceeded
}

func (f *failingLimiter) Reset(_ context.Context, _ string) error {
	return context.DeadlineExceeded
}

func (f *failingLimiter) Close() error { return nil }
