package engineclient

import (
	"context"
	"log"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/backoff"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"
	"github.com/nacl-org/nacl-cloud-go/internal/config"
)

// unaryLoggingInterceptor logs the duration and status of unary gRPC calls.
func unaryLoggingInterceptor(
	ctx context.Context,
	method string,
	req, reply interface{},
	cc *grpc.ClientConn,
	invoker grpc.UnaryInvoker,
	opts ...grpc.CallOption,
) error {
	start := time.Now()
	err := invoker(ctx, method, req, reply, cc, opts...)
	log.Printf("gRPC Client Invocation: %s | Duration: %s | Error: %v", method, time.Since(start), err)
	return err
}

// NewEngineClient establishes a highly optimized, production-grade gRPC connection to the Rust engine backend.
func NewEngineClient(cfg *config.Config) (*grpc.ClientConn, func(), error) {
	log.Printf("Dialing Rust nacl-engine gRPC server at: %s", cfg.EngineAddr)

	// 1. Define connection timeout for fail-fast boots
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// 2. Configure keep-alive parameters (Google Best Practice - adjusted to prevent GOAWAY)
	kacp := keepalive.ClientParameters{
		Time:                30 * time.Second, // Ping every 30 seconds if idle
		Timeout:             5 * time.Second,  // Wait 5 seconds for ping ack
		PermitWithoutStream: false,            // Only ping when active calls exist
	}

	// 3. Configure rapid reconnection backoff parameters
	connectParams := grpc.ConnectParams{
		Backoff: backoff.Config{
			BaseDelay:  500 * time.Millisecond,
			Multiplier: 1.5,
			Jitter:     0.2,
			MaxDelay:   5 * time.Second,
		},
	}

	// 4. Dial with latency optimization, fail-fast blocking, and telemetry logging
	conn, err := grpc.DialContext(ctx, cfg.EngineAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithKeepaliveParams(kacp),
		grpc.WithConnectParams(connectParams),
		// Optimize window sizes for large schema AST transfers
		grpc.WithInitialWindowSize(512*1024),     // 512 KB
		grpc.WithInitialConnWindowSize(512*1024), // 512 KB
		// Register unary interceptor for automated latency tracking
		grpc.WithUnaryInterceptor(unaryLoggingInterceptor),
	)
	if err != nil {
		return nil, nil, err
	}

	cleanup := func() {
		log.Println("Closing gRPC connection to nacl-engine...")
		if err := conn.Close(); err != nil {
			log.Printf("Error closing gRPC connection: %v", err)
		}
	}

	return conn, cleanup, nil
}
