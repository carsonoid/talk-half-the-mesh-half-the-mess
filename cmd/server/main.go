package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"

	"google.golang.org/grpc"
	testpb "google.golang.org/grpc/interop/grpc_testing"
)

// START MAIN OMIT
func main() {
	die := make(chan os.Signal, 1)
	signal.Notify(die, os.Interrupt)

	hostname, err := os.Hostname()
	if err != nil {
		panic(err)
	}

	go runGRPCServer(hostname)
	go httpServer(hostname)

	<-die
}

// END MAIN OMIT

// START GRPC OMIT
type testService struct {
	testpb.UnimplementedTestServiceServer
	hostname string
}

func (s *testService) UnaryCall(ctx context.Context, req *testpb.SimpleRequest) (*testpb.SimpleResponse, error) {
	return &testpb.SimpleResponse{Hostname: s.hostname}, nil
}

func runGRPCServer(hostname string) {
	fmt.Println("Starting gRPC server on :9000")
	server := grpc.NewServer()

	testpb.RegisterTestServiceServer(server, &testService{hostname: hostname})

	l, err := net.Listen("tcp", ":9000")
	if err != nil {
		panic(err)
	}

	err = server.Serve(l)
	if err != nil {
		panic(err)
	}
}

// END GRPC OMIT

// START HTTP OMIT
func httpServer(hostname string) {
	server := &http.Server{
		Addr: ":8080",
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			fmt.Println("> Request Headers:", r.Header)
			fmt.Fprintln(w, "Hello! From", hostname)
		}),
	}

	fmt.Println("Starting HTTP server on :8080")
	err := server.ListenAndServe()
	if err != nil {
		panic(err)
	}
}

// END HTTP OMIT
