package grpcserver

import (
	"context"
	"net"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	"github.com/evbruno/bkp.go/internal/pb"
	"github.com/evbruno/bkp.go/internal/store"
)

// startTestServer opens a temp-file store, serves it over an in-memory
// bufconn listener, and returns a connected client plus its store.
func startTestServer(t *testing.T) (pb.BkpServiceClient, *store.Store) {
	t.Helper()

	path := filepath.Join(t.TempDir(), "test.sqlite3")
	st, err := store.Open(path)
	if err != nil {
		t.Fatalf("store.Open returned error: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	lis := bufconn.Listen(1024 * 1024)
	t.Cleanup(func() { lis.Close() })

	srv := grpc.NewServer()
	pb.RegisterBkpServiceServer(srv, New(st))
	go srv.Serve(lis)
	t.Cleanup(srv.Stop)

	conn, err := grpc.NewClient(
		"passthrough:///bufconn",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("grpc.NewClient returned error: %v", err)
	}
	t.Cleanup(func() { conn.Close() })

	return pb.NewBkpServiceClient(conn), st
}

func TestInsertLog_RoundTrip(t *testing.T) {
	client, st := startTestServer(t)
	ctx := context.Background()

	ts := time.Now().UTC().Format(time.RFC3339)
	compressed := int64(42)

	_, err := client.InsertLog(ctx, &pb.InsertLogRequest{
		Row: &pb.LogRow{
			Timestamp:      ts,
			Project:        "app1",
			FilePath:       "/tmp/app1.db",
			FileSize:       1000,
			CompressedSize: &compressed,
			Status:         "ok",
			DurationMs:     123,
			Sha1:           "deadbeef",
		},
	})
	if err != nil {
		t.Fatalf("InsertLog returned error: %v", err)
	}

	rows, err := st.LatestPerProject()
	if err != nil {
		t.Fatalf("LatestPerProject returned error: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if rows[0].Project != "app1" || rows[0].SHA1 != "deadbeef" {
		t.Fatalf("unexpected row: %+v", rows[0])
	}
}

func TestGetLatestOKSHA1(t *testing.T) {
	client, st := startTestServer(t)
	ctx := context.Background()

	if err := st.InsertLog(store.LogRow{
		Timestamp: time.Now(),
		Project:   "app1",
		FilePath:  "/tmp/app1.db",
		Status:    "ok",
		SHA1:      "abc123",
	}); err != nil {
		t.Fatalf("InsertLog returned error: %v", err)
	}

	resp, err := client.GetLatestOKSHA1(ctx, &pb.GetLatestOKSHA1Request{Project: "app1"})
	if err != nil {
		t.Fatalf("GetLatestOKSHA1 returned error: %v", err)
	}
	if !resp.Found || resp.Sha1 != "abc123" {
		t.Fatalf("unexpected response: %+v", resp)
	}

	resp, err = client.GetLatestOKSHA1(ctx, &pb.GetLatestOKSHA1Request{Project: "missing"})
	if err != nil {
		t.Fatalf("GetLatestOKSHA1 returned error: %v", err)
	}
	if resp.Found {
		t.Fatalf("expected found=false for unknown project, got %+v", resp)
	}
}

func TestListLatestPerProject(t *testing.T) {
	client, st := startTestServer(t)
	ctx := context.Background()

	for _, p := range []string{"app1", "app2"} {
		if err := st.InsertLog(store.LogRow{
			Timestamp: time.Now(),
			Project:   p,
			FilePath:  "/tmp/" + p + ".db",
			Status:    "ok",
		}); err != nil {
			t.Fatalf("InsertLog returned error: %v", err)
		}
	}

	resp, err := client.ListLatestPerProject(ctx, &pb.ListLatestPerProjectRequest{})
	if err != nil {
		t.Fatalf("ListLatestPerProject returned error: %v", err)
	}
	if len(resp.Rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(resp.Rows))
	}
}
