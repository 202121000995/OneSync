package certutil

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const DefaultValidity = 365 * 24 * time.Hour

// Options controls local TLS certificate generation.
type Options struct {
	Hosts    []string
	CertPath string
	KeyPath  string
	Validity time.Duration
	Now      func() time.Time
}

// Generate creates a self-signed TLS certificate for local OneSync testing.
func Generate(options Options) error {
	if options.Validity == 0 {
		options.Validity = DefaultValidity
	}
	if options.Validity <= 0 {
		return errors.New("certificate validity must be positive")
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if strings.TrimSpace(options.CertPath) == "" || strings.TrimSpace(options.KeyPath) == "" {
		return errors.New("certificate and key paths are required")
	}
	dnsNames, ipAddresses, err := parseHosts(options.Hosts)
	if err != nil {
		return err
	}

	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return fmt.Errorf("generate private key: %w", err)
	}
	serialNumberLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serialNumber, err := rand.Int(rand.Reader, serialNumberLimit)
	if err != nil {
		return fmt.Errorf("generate certificate serial: %w", err)
	}
	now := options.Now().UTC()
	template := &x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			CommonName: "OneSync Local",
		},
		NotBefore:             now.Add(-time.Minute),
		NotAfter:              now.Add(options.Validity),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLen:            0,
		DNSNames:              dnsNames,
		IPAddresses:           ipAddresses,
	}
	certificateDER, err := x509.CreateCertificate(rand.Reader, template, template, &privateKey.PublicKey, privateKey)
	if err != nil {
		return fmt.Errorf("create certificate: %w", err)
	}
	privateKeyDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		return fmt.Errorf("marshal private key: %w", err)
	}
	if err := writePEM(options.CertPath, 0o644, &pem.Block{Type: "CERTIFICATE", Bytes: certificateDER}); err != nil {
		return err
	}
	if err := writePEM(options.KeyPath, 0o600, &pem.Block{Type: "PRIVATE KEY", Bytes: privateKeyDER}); err != nil {
		return err
	}
	return nil
}

func parseHosts(hosts []string) ([]string, []net.IP, error) {
	var dnsNames []string
	var ipAddresses []net.IP
	seen := make(map[string]struct{})
	for _, raw := range hosts {
		for _, part := range strings.Split(raw, ",") {
			host := strings.TrimSpace(part)
			if host == "" {
				continue
			}
			if strings.ContainsAny(host, `/\`+"\x00") {
				return nil, nil, errors.New("host contains an unsafe character")
			}
			if _, exists := seen[host]; exists {
				continue
			}
			seen[host] = struct{}{}
			if ip := net.ParseIP(host); ip != nil {
				ipAddresses = append(ipAddresses, ip)
			} else {
				dnsNames = append(dnsNames, host)
			}
		}
	}
	if len(dnsNames) == 0 && len(ipAddresses) == 0 {
		return nil, nil, errors.New("at least one host is required")
	}
	return dnsNames, ipAddresses, nil
}

func writePEM(path string, permission os.FileMode, block *pem.Block) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create certificate directory: %w", err)
	}
	data := pem.EncodeToMemory(block)
	if data == nil {
		return errors.New("encode PEM block failed")
	}
	if err := os.WriteFile(path, data, permission); err != nil {
		return fmt.Errorf("write PEM file: %w", err)
	}
	if err := os.Chmod(path, permission); err != nil {
		return fmt.Errorf("set PEM file permissions: %w", err)
	}
	return nil
}
