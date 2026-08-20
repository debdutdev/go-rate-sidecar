package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"time"

	ratelimiter "github.com/12345debdut/rate-limiter"
	"github.com/12345debdut/rate-limiter/algorithm"
	rlgrpc "github.com/12345debdut/rate-limiter/grpc"
	"github.com/12345debdut/rate-limiter/store"
	"google.golang.org/grpc"
	pb "google.golang.org/grpc/health/grpc_health_v1"
)

type healthServer struct {
	pb.UnimplementedHealthServer
}

func (s *healthServer) Check(_ context.Context, _ *pb.HealthCheckRequest) (*pb.HealthCheckResponse, error) {
	return &pb.HealthCheckResponse{Status: pb.HealthCheckResponse_SERVING}, nil
}

func main() {
	mem := store.NewMemory(time.Minute)
	defer mem.Close()

	limiter, err := algorithm.NewTokenBucket(ratelimiter.Config{
		Rate:  5,
		Burst: 10,
	}, mem)
	if err != nil {
		log.Fatal(err)
	}
	defer limiter.Close()

	srv := grpc.NewServer(
		grpc.UnaryInterceptor(rlgrpc.UnaryServerInterceptor(rlgrpc.InterceptorConfig{
			Limiter:      limiter,
			KeyExtractor: rlgrpc.PeerAddrExtractor(),
			SkipFunc: func(method string) bool {
				return method == "/grpc.health.v1.Health/Check"
			},
		})),
		grpc.StreamInterceptor(rlgrpc.StreamServerInterceptor(rlgrpc.InterceptorConfig{
			Limiter: limiter,
		})),
	)

	pb.RegisterHealthServer(srv, &healthServer{})

	lis, err := net.Listen("tcp", ":50051")
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println("gRPC server listening on :50051")
	fmt.Println("Rate limited: 10 burst, 5/sec refill per peer")
	fmt.Println("Health checks bypass rate limiting")
	fmt.Println("Try: grpcurl -plaintext localhost:50051 grpc.health.v1.Health/Check")
	log.Fatal(srv.Serve(lis))
}
