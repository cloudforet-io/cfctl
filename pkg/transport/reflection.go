package transport

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

// ListGRPCServices retrieves a list of available gRPC services from the specified endpoint.
// It supports only grpc+ssl:// scheme, with proper TLS configuration for secure connections.
// The function uses gRPC reflection to discover available services.
//
// Parameters:
//   - endpoint: The gRPC endpoint URL (e.g., "grpc+ssl://api.example.com:443")
//
// Returns:
//   - []string: A list of available service names
//   - error: An error if the operation fails
//
// Example:
//
//	services, err := GetGRPCServices("grpc+ssl://api.example.com:443")
//	if err != nil {
//	    log.Fatalf("Failed to get services: %v", err)
//	}
func ListGRPCServices(endpoint string) ([]string, error) {
	parsedURL, err := url.Parse(endpoint)
	if err != nil {
		return nil, fmt.Errorf("failed to parse endpoint: %w", err)
	}

	host := parsedURL.Hostname()
	port := parsedURL.Port()
	if port == "" {
		port = "443"
	}

	conn, err := dialGRPC(endpoint, host, port)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := conn.Close(); err != nil {
			log.Printf("failed to close gRPC connection: %v", err)
		}
	}()

	return listServices(conn)
}

// CheckIdentityProxyAvailable checks if the given gRPC endpoint can be used as an identity proxy
// by verifying the presence of both Endpoint and Token services.
// These services are required for the identity service to act as a proxy.
//
// Parameters:
//   - endpoint: The gRPC endpoint URL to check
//
// Returns:
//   - bool: true if both Endpoint and Token services are present
//   - error: An error if the operation fails
func CheckIdentityProxyAvailable(endpoint string) (bool, error) {
	services, err := ListGRPCServices(endpoint)
	if err != nil {
		return false, fmt.Errorf("failed to get services: %w", err)
	}

	return checkRequiredServices(services)
}

// dialGRPC establishes a gRPC connection with the specified endpoint
func dialGRPC(endpoint, host, port string) (*grpc.ClientConn, error) {
	var opts []grpc.DialOption
	if strings.HasPrefix(endpoint, "grpc+ssl://") {
		tlsSetting := &tls.Config{
			InsecureSkipVerify: false,
		}
		credential := credentials.NewTLS(tlsSetting)
		opts = append(opts, grpc.WithTransportCredentials(credential))
	} else {
		return nil, fmt.Errorf("unsupported scheme in endpoint: %s", endpoint)
	}

	conn, err := grpc.Dial(fmt.Sprintf("%s:%s", host, port), opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to dial gRPC endpoint: %w", err)
	}
	return conn, nil
}

// listServices uses gRPC reflection to list available services
func listServices(conn *grpc.ClientConn) ([]string, error) {
	ctx := context.Background()
	refClient := grpcreflect.NewClientV1Alpha(ctx, grpc_reflection_v1alpha.NewServerReflectionClient(conn))
	defer refClient.Reset()

	services, err := refClient.ListServices()
	if err != nil {
		return nil, fmt.Errorf("failed to list services: %w", err)
	}

	return services, nil
}

// checkRequiredServices checks if both Endpoint and Token services are present
func checkRequiredServices(services []string) (bool, error) {
	var (
		hasEndpoint bool
		hasToken    bool
	)

	for _, svc := range services {
		switch {
		case strings.HasSuffix(svc, ".Endpoint"):
			hasEndpoint = true
		case strings.HasSuffix(svc, ".Token"):
			hasToken = true
		}

		if hasEndpoint && hasToken {
			return true, nil
		}
	}

	return false, nil
}
