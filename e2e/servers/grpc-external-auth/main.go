package main

import (
	"context"
	"log"
	"net"
	"os"
	"time"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	authv3 "github.com/envoyproxy/go-control-plane/envoy/service/auth/v3"
	typev3 "github.com/envoyproxy/go-control-plane/envoy/type/v3"
	"google.golang.org/genproto/googleapis/rpc/status"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
)

type authServer struct {
	authv3.UnimplementedAuthorizationServer
}

func (s *authServer) Check(_ context.Context, req *authv3.CheckRequest) (*authv3.CheckResponse, error) {
	httpReq := req.GetAttributes().GetRequest().GetHttp()
	log.Printf("gRPC Auth Check: %s %s%s", httpReq.GetMethod(), httpReq.GetHost(), httpReq.GetPath())
	for name, value := range httpReq.GetHeaders() {
		log.Printf("  Header: %s = %s", name, value)
	}

	allowHeader := httpReq.GetHeaders()["x-ext-auth-allow"]
	timestamp := time.Now().UTC().Format(time.RFC3339)

	if allowHeader == "true" {
		log.Printf("Auth decision: ALLOWED")
		return &authv3.CheckResponse{
			Status: &status.Status{Code: int32(codes.OK)},
			HttpResponse: &authv3.CheckResponse_OkResponse{
				OkResponse: &authv3.OkHttpResponse{
					Headers: []*corev3.HeaderValueOption{
						{
							Header: &corev3.HeaderValue{
								Key:   "x-auth-decision",
								Value: "allowed",
							},
							AppendAction: corev3.HeaderValueOption_OVERWRITE_IF_EXISTS_OR_ADD,
						},
						{
							Header: &corev3.HeaderValue{
								Key:   "x-auth-timestamp",
								Value: timestamp,
							},
							AppendAction: corev3.HeaderValueOption_OVERWRITE_IF_EXISTS_OR_ADD,
						},
					},
				},
			},
		}, nil
	}

	log.Printf("Auth decision: DENIED (x-ext-auth-allow=%q)", allowHeader)
	return &authv3.CheckResponse{
		Status: &status.Status{Code: int32(codes.PermissionDenied)},
		HttpResponse: &authv3.CheckResponse_DeniedResponse{
			DeniedResponse: &authv3.DeniedHttpResponse{
				Status: &typev3.HttpStatus{Code: typev3.StatusCode_Forbidden},
				Headers: []*corev3.HeaderValueOption{
					{
						Header: &corev3.HeaderValue{
							Key:   "x-auth-decision",
							Value: "denied",
						},
						AppendAction: corev3.HeaderValueOption_OVERWRITE_IF_EXISTS_OR_ADD,
					},
				},
				Body: `{"status":"denied","reason":"x-ext-auth-allow header not set to 'true'"}`,
			},
		},
	}, nil
}

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "9003"
	}

	lis, err := net.Listen("tcp", ":"+port)
	if err != nil {
		log.Fatalf("Failed to listen: %v", err)
	}

	s := grpc.NewServer()
	authv3.RegisterAuthorizationServer(s, &authServer{})

	log.Printf("gRPC ext-auth server starting on :%s", port)
	log.Printf("  Service: envoy.service.auth.v3.Authorization")
	log.Printf("  Set 'x-ext-auth-allow: true' header to allow requests")

	if err := s.Serve(lis); err != nil {
		log.Fatalf("Failed to serve: %v", err)
	}
}
