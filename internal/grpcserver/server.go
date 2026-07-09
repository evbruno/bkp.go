// Package grpcserver exposes internal/store's SQLite operations over gRPC.
package grpcserver

import (
	"context"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/evbruno/bkp.go/internal/pb"
	"github.com/evbruno/bkp.go/internal/store"
)

// Server implements pb.BkpServiceServer over a *store.Store.
type Server struct {
	pb.UnimplementedBkpServiceServer
	st *store.Store
}

// New wraps st as a pb.BkpServiceServer.
func New(st *store.Store) *Server {
	return &Server{st: st}
}

func (s *Server) InsertLog(ctx context.Context, req *pb.InsertLogRequest) (*pb.InsertLogResponse, error) {
	row := req.GetRow()
	if row == nil {
		return nil, status.Error(codes.InvalidArgument, "row is required")
	}

	ts, err := time.Parse(time.RFC3339, row.GetTimestamp())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "parsing timestamp: %v", err)
	}

	var compressedSize *int64
	if row.CompressedSize != nil {
		v := row.GetCompressedSize()
		compressedSize = &v
	}

	if err := s.st.InsertLog(store.LogRow{
		Timestamp:      ts,
		Project:        row.GetProject(),
		FilePath:       row.GetFilePath(),
		FileSize:       row.GetFileSize(),
		CompressedSize: compressedSize,
		Status:         row.GetStatus(),
		Error:          row.GetError(),
		DurationMs:     row.GetDurationMs(),
		SHA1:           row.GetSha1(),
	}); err != nil {
		return nil, status.Errorf(codes.Internal, "inserting log: %v", err)
	}

	return &pb.InsertLogResponse{}, nil
}

func (s *Server) GetLatestOKSHA1(ctx context.Context, req *pb.GetLatestOKSHA1Request) (*pb.GetLatestOKSHA1Response, error) {
	if req.GetProject() == "" {
		return nil, status.Error(codes.InvalidArgument, "project is required")
	}

	sha1, found, err := s.st.LatestOKSHA1(req.GetProject())
	if err != nil {
		return nil, status.Errorf(codes.Internal, "querying latest sha1: %v", err)
	}

	return &pb.GetLatestOKSHA1Response{Sha1: sha1, Found: found}, nil
}

func (s *Server) ListLatestPerProject(ctx context.Context, req *pb.ListLatestPerProjectRequest) (*pb.ListLatestPerProjectResponse, error) {
	rows, err := s.st.LatestPerProject()
	if err != nil {
		return nil, status.Errorf(codes.Internal, "querying latest per project: %v", err)
	}

	pbRows := make([]*pb.LogRow, len(rows))
	for i, r := range rows {
		pbRow := &pb.LogRow{
			Timestamp:  r.Timestamp.UTC().Format(time.RFC3339),
			Project:    r.Project,
			FilePath:   r.FilePath,
			FileSize:   r.FileSize,
			Status:     r.Status,
			Error:      r.Error,
			DurationMs: r.DurationMs,
			Sha1:       r.SHA1,
		}
		if r.CompressedSize != nil {
			pbRow.CompressedSize = r.CompressedSize
		}
		pbRows[i] = pbRow
	}

	return &pb.ListLatestPerProjectResponse{Rows: pbRows}, nil
}
