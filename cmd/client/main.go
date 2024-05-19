package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	testpb "google.golang.org/grpc/interop/grpc_testing"

	_ "google.golang.org/grpc/xds" // Register xDS transport
)

func main() {
	die := make(chan os.Signal, 1)
	signal.Notify(die, os.Interrupt)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		<-die
		cancel()
	}()

	// START MAIN OMIT
	if len(os.Args) < 2 {
		fmt.Println("Usage: client [http|grpc] <args>")
		os.Exit(1)
	}

	switch os.Args[1] {
	case "http":
		err := httpClient(ctx, os.Args[2:])
		if err != nil {
			panic(err)
		}
	case "grpc":
		err := grpcClient(ctx, os.Args[2:])
		if err != nil {
			panic(err)
		}
	default:
		panic("unknown client type")
	}
	// END MAIN OMIT
}

func runEvery(ctx context.Context, d time.Duration, f func(ctx context.Context) error) {
	t := time.NewTicker(d)
	defer t.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			err := f(ctx)
			if err != nil {
				fmt.Println(err)
			}
		}
	}
}

func grpcClient(ctx context.Context, args []string) error {
	if len(args) < 1 {
		fmt.Println("Usage: client grpc <address>")
		return fmt.Errorf("address required")
	}

	// START GRPC OMIT
	conn, err := grpc.NewClient(args[0], grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		panic(err)
	}
	client := testpb.NewTestServiceClient(conn)

	fmt.Println("Starting gRPC client request loop")
	runEvery(ctx, time.Second/2, func(ctx context.Context) error {
		resp, err := client.UnaryCall(ctx, &testpb.SimpleRequest{})
		if err != nil {
			return fmt.Errorf("failed to make request: %w", err)
		}

		fmt.Println("Reponse Hostname:", resp.Hostname)
		return nil
	})
	// END GRPC OMIT

	return nil
}

func httpClient(ctx context.Context, args []string) error {
	if len(args) < 1 {
		fmt.Println("Usage: client  http [headerpair...] <target>")
		return fmt.Errorf("invalid usage")
	}

	headers := http.Header{}
	for _, pair := range args[0 : len(args)-1] {
		kv := strings.Split(pair, ":")
		if len(kv) != 2 {
			return fmt.Errorf("invalid header pair: %s", pair)
		}
		headers.Add(strings.TrimSpace(kv[0]), strings.TrimSpace(kv[1]))
	}

	target := args[len(args)-1]

	client := &http.Client{
		Timeout: time.Second * 5,
	}

	fmt.Println("Starting HTTP client request loop")
	runEvery(ctx, time.Second/2, func(ctx context.Context) error {
		// START HTTP OMIT
		req, err := http.NewRequest(http.MethodGet, target, nil)
		if err != nil {
			return fmt.Errorf("failed to create request: %w", err)
		}
		for k, v := range headers {
			req.Header[k] = v
		}

		resp, err := client.Do(req)
		if err != nil {
			return fmt.Errorf("failed to make request: %w", err)
		}
		defer resp.Body.Close()

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return fmt.Errorf("failed to read response body: %w", err)
		}

		fmt.Println("> Request Headers:", req.Header)
		fmt.Println("< Response Headers:", resp.Header)
		fmt.Println("< Response Body:", string(body))

		return nil
		// END HTTP OMIT
	})

	return nil
}
