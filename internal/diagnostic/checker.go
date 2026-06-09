package diagnostic

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"
)

const DefaultTimeout = 5 * time.Second

// EndpointResult reports whether one TLS endpoint can be reached and verified.
type EndpointResult struct {
	Endpoint string `json:"endpoint"`
	OK       bool   `json:"ok"`
	Error    string `json:"error,omitempty"`
}

// Result reports direct and optional Relay endpoint checks for one link.
type Result struct {
	Direct EndpointResult  `json:"direct"`
	Relay  *EndpointResult `json:"relay,omitempty"`
	Usable bool            `json:"usable"`
}

// Checker performs short TLS handshakes without sending synchronization secrets.
type Checker struct {
	config  *tls.Config
	timeout time.Duration
}

// NewChecker creates a verified TLS endpoint checker.
func NewChecker(config *tls.Config, timeout time.Duration) (*Checker, error) {
	if config == nil {
		return nil, errors.New("TLS client configuration is required")
	}
	if config.InsecureSkipVerify {
		return nil, errors.New("TLS certificate verification cannot be disabled")
	}
	if timeout == 0 {
		timeout = DefaultTimeout
	}
	if timeout < 0 {
		return nil, errors.New("connection check timeout cannot be negative")
	}
	copied := config.Clone()
	copied.MinVersion = tls.VersionTLS13
	return &Checker{config: copied, timeout: timeout}, nil
}

// Test checks a direct endpoint and, when present, a Relay endpoint.
func (c *Checker) Test(ctx context.Context, endpoint, relayEndpoint string) Result {
	return c.TestWithCertificate(ctx, endpoint, relayEndpoint, "")
}

// TestWithCertificate checks endpoints with an optional additional CA certificate.
func (c *Checker) TestWithCertificate(ctx context.Context, endpoint, relayEndpoint, caCertificatePEM string) Result {
	checker := c
	if strings.TrimSpace(caCertificatePEM) != "" {
		config, err := addCACertificate(c.config, caCertificatePEM)
		if err != nil {
			direct := EndpointResult{Endpoint: strings.TrimSpace(endpoint), Error: err.Error()}
			return Result{Direct: direct}
		}
		checker = &Checker{config: config, timeout: c.timeout}
	}
	direct := checker.Check(ctx, endpoint)
	result := Result{Direct: direct, Usable: direct.OK}
	if strings.TrimSpace(relayEndpoint) != "" {
		relay := checker.Check(ctx, relayEndpoint)
		result.Relay = &relay
		result.Usable = result.Usable || relay.OK
	}
	return result
}

// Check verifies that one TLS endpoint accepts a TLS 1.3 handshake.
func (c *Checker) Check(ctx context.Context, endpoint string) EndpointResult {
	endpoint = strings.TrimSpace(endpoint)
	result := EndpointResult{Endpoint: endpoint}
	if endpoint == "" {
		result.Error = "endpoint is required"
		return result
	}
	checkContext, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	config := c.config.Clone()
	config.MinVersion = tls.VersionTLS13
	dialer := tls.Dialer{NetDialer: &net.Dialer{}, Config: config}
	connection, err := dialer.DialContext(checkContext, "tcp", endpoint)
	if err != nil {
		result.Error = fmt.Sprintf("TLS connection failed: %v", err)
		return result
	}
	_ = connection.Close()
	result.OK = true
	return result
}

func addCACertificate(config *tls.Config, caCertificatePEM string) (*tls.Config, error) {
	copied := config.Clone()
	var roots *x509.CertPool
	if copied.RootCAs != nil {
		roots = copied.RootCAs.Clone()
	} else {
		var err error
		roots, err = x509.SystemCertPool()
		if err != nil || roots == nil {
			roots = x509.NewCertPool()
		}
	}
	if !roots.AppendCertsFromPEM([]byte(caCertificatePEM)) {
		return nil, errors.New("link CA certificate is invalid")
	}
	copied.RootCAs = roots
	copied.MinVersion = tls.VersionTLS13
	return copied, nil
}
