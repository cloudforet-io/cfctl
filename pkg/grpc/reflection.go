package grpc

import (
	"context"
	"crypto/tls"
	"fmt"
	"log"
	"net/url"
	"strings"

	"github.com/jhump/protoreflect/grpcreflect"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/reflection/grpc_reflection_v1alpha"
)

// CheckIdentityProxyAvailable checks if the given gRPC endpoint can be used as an identity proxy
// by verifying the presence of both Endpoint and Token services.
// These services are required for the identity service to act as a proxy.
// Returns true if both services are present (proxy available), false otherwise.
// The function uses gRPC reflection to discover available services.
// Only grpc+ssl:// scheme is supported.
func CheckIdentityProxyAvailable(endpoint string) (bool, error) {
	parsedURL, err := url.Parse(endpoint)
	if err != nil {
		return false, fmt.Errorf("failed to parse endpoint: %w", err)
	}

	host := parsedURL.Hostname()
	port := parsedURL.Port()
	if port == "" {
		port = "443"
	}

	var opts []grpc.DialOption
	if strings.HasPrefix(endpoint, "grpc+ssl://") {
		tlsSetting := &tls.Config{
			InsecureSkipVerify: false,
		}
		credential := credentials.NewTLS(tlsSetting)
		opts = append(opts, grpc.WithTransportCredentials(credential))
	} else {
		return false, fmt.Errorf("unsupported scheme in endpoint: %s", endpoint)
	}

	conn, err := grpc.Dial(fmt.Sprintf("%s:%s", host, port), opts...)
	if err != nil {
		return false, fmt.Errorf("failed to dial gRPC endpoint: %w", err)
	}
	defer func(conn *grpc.ClientConn) {
		err := conn.Close()
		if err != nil {
			log.Printf("failed to close gRPC connection: %v", err)
		}
	}(conn)

	ctx := context.Background()
	refClient := grpcreflect.NewClientV1Alpha(ctx, grpc_reflection_v1alpha.NewServerReflectionClient(conn))
	defer refClient.Reset()

	services, err := refClient.ListServices()
	if err != nil {
		return false, fmt.Errorf("failed to list services: %w", err)
	}

	hasEndpoint := false
	hasToken := false

	for _, svc := range services {
		if strings.HasSuffix(svc, ".Endpoint") {
			hasEndpoint = true
		}
		if strings.HasSuffix(svc, ".Token") {
			hasToken = true
		}
		if hasEndpoint && hasToken {
			return true, nil
		}
	}

	return false, nil
}
