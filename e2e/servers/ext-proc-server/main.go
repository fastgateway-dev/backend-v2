package main

import (
	"log"
	"net"
	"os"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

type extProcServer struct {
	extprocv3.UnimplementedExternalProcessorServer
}

func (s *extProcServer) Process(stream extprocv3.ExternalProcessor_ProcessServer) error {
	for {
		req, err := stream.Recv()
		if err != nil {
			return err
		}

		switch v := req.Request.(type) {
		case *extprocv3.ProcessingRequest_RequestHeaders:
			log.Printf("Received request headers:")
			if v.RequestHeaders != nil && v.RequestHeaders.Headers != nil {
				for _, h := range v.RequestHeaders.Headers.Headers {
					log.Printf("  %s: %s", h.Key, string(h.RawValue))
				}
			}

			resp := &extprocv3.ProcessingResponse{
				Response: &extprocv3.ProcessingResponse_RequestHeaders{
					RequestHeaders: &extprocv3.HeadersResponse{
						Response: &extprocv3.CommonResponse{
							Status: extprocv3.CommonResponse_CONTINUE,
						},
					},
				},
			}
			if err := stream.Send(resp); err != nil {
				return err
			}

		case *extprocv3.ProcessingRequest_ResponseHeaders:
			log.Printf("Received response headers")

			// Add x-ext-proc-processed header to the response
			resp := &extprocv3.ProcessingResponse{
				Response: &extprocv3.ProcessingResponse_ResponseHeaders{
					ResponseHeaders: &extprocv3.HeadersResponse{
						Response: &extprocv3.CommonResponse{
							Status: extprocv3.CommonResponse_CONTINUE,
							HeaderMutation: &extprocv3.HeaderMutation{
								SetHeaders: []*corev3.HeaderValueOption{
									{
										Header: &corev3.HeaderValue{
											Key:      "x-ext-proc-processed",
											RawValue: []byte("true"),
										},
									},
								},
							},
						},
					},
				},
			}
			if err := stream.Send(resp); err != nil {
				return err
			}

		case *extprocv3.ProcessingRequest_RequestBody:
			log.Printf("Received request body")
			resp := &extprocv3.ProcessingResponse{
				Response: &extprocv3.ProcessingResponse_RequestBody{
					RequestBody: &extprocv3.BodyResponse{
						Response: &extprocv3.CommonResponse{
							Status: extprocv3.CommonResponse_CONTINUE,
						},
					},
				},
			}
			if err := stream.Send(resp); err != nil {
				return err
			}

		case *extprocv3.ProcessingRequest_ResponseBody:
			log.Printf("Received response body")
			resp := &extprocv3.ProcessingResponse{
				Response: &extprocv3.ProcessingResponse_ResponseBody{
					ResponseBody: &extprocv3.BodyResponse{
						Response: &extprocv3.CommonResponse{
							Status: extprocv3.CommonResponse_CONTINUE,
						},
					},
				},
			}
			if err := stream.Send(resp); err != nil {
				return err
			}

		default:
			log.Printf("Received unknown request type: %T", v)
		}
	}
}

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "9004"
	}

	lis, err := net.Listen("tcp", ":"+port)
	if err != nil {
		log.Fatalf("Failed to listen: %v", err)
	}

	grpcServer := grpc.NewServer()
	extprocv3.RegisterExternalProcessorServer(grpcServer, &extProcServer{})

	// Register health check
	healthServer := health.NewServer()
	healthpb.RegisterHealthServer(grpcServer, healthServer)
	healthServer.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)

	log.Printf("Ext-proc test server starting on :%s", port)
	log.Printf("  gRPC ext-proc service ready")
	log.Printf("  Adds 'x-ext-proc-processed: true' response header")

	if err := grpcServer.Serve(lis); err != nil {
		log.Fatalf("Failed to serve: %v", err)
	}
}
