// Command bkp-server exposes the backup_log SQLite store over gRPC.
package main

import (
	"flag"
	"fmt"
	"net"
	"os"

	"google.golang.org/grpc"

	"github.com/evbruno/bkp.go/internal/config"
	"github.com/evbruno/bkp.go/internal/grpcserver"
	"github.com/evbruno/bkp.go/internal/pb"
	"github.com/evbruno/bkp.go/internal/store"
)

func main() {
	configPath := flag.String("config", "", "path to backup spec YAML (required); its target field is the SQLite database to serve")
	addr := flag.String("addr", ":50051", "gRPC listen address")
	flag.Parse()

	if *configPath == "" {
		fmt.Fprintln(os.Stderr, "error: --config is required")
		flag.Usage()
		os.Exit(2)
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	st, err := store.Open(cfg.Target)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	defer st.Close()

	lis, err := net.Listen("tcp", *addr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: listening on %s: %v\n", *addr, err)
		os.Exit(1)
	}

	grpcServer := grpc.NewServer()
	pb.RegisterBkpServiceServer(grpcServer, grpcserver.New(st))

	fmt.Printf("bkp-server: serving %s on %s\n", cfg.Target, *addr)
	if err := grpcServer.Serve(lis); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
