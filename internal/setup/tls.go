package setup

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"strings"
	"time"
)

const (
	platformTLSCert  = "ftc-platform-tls"
	platformCASecret = "ftc-platform-ca"
)

type TLSBundle struct {
	ServerCert []byte
	ServerKey  []byte
	CACert     []byte
	CAKey      []byte
}

// ValidateInternalDomain accepts a DNS name suitable for an internal Ingress.
func ValidateInternalDomain(value string) (string, error) {
	domain := strings.ToLower(strings.TrimSpace(value))
	if domain == "" {
		return "", fmt.Errorf("内部域名不能为空")
	}
	if net.ParseIP(domain) != nil {
		return "", fmt.Errorf("不能使用 IP 地址，请输入内部域名")
	}
	if strings.Contains(domain, "*") || strings.Contains(domain, "_") {
		return "", fmt.Errorf("域名不能包含通配符或下划线")
	}
	if len(domain) > 253 || strings.HasPrefix(domain, ".") || strings.HasSuffix(domain, ".") {
		return "", fmt.Errorf("域名长度或首尾字符无效")
	}
	labels := strings.Split(domain, ".")
	if len(labels) < 2 {
		return "", fmt.Errorf("请输入完整域名，例如 ftc-platform.cmb.local")
	}
	for _, label := range labels {
		if len(label) == 0 || len(label) > 63 {
			return "", fmt.Errorf("域名标签长度无效")
		}
		if label[0] == '-' || label[len(label)-1] == '-' {
			return "", fmt.Errorf("域名标签不能以 - 开头或结尾")
		}
		for _, character := range label {
			if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '-' {
				return "", fmt.Errorf("域名只能包含小写字母、数字和 -")
			}
		}
	}
	return domain, nil
}

// GenerateSelfSignedTLS creates a private CA and a server certificate for the
// domain. The CA key is kept so a later version can rotate the server cert
// without asking the user to trust a new root.
func GenerateSelfSignedTLS(domain string) (TLSBundle, error) {
	if _, err := ValidateInternalDomain(domain); err != nil {
		return TLSBundle{}, err
	}
	now := time.Now()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return TLSBundle{}, err
	}
	caSerial, err := randomSerial()
	if err != nil {
		return TLSBundle{}, err
	}
	caTemplate := &x509.Certificate{
		SerialNumber:          caSerial,
		Subject:               pkix.Name{CommonName: "FTC Platform Local CA", Organization: []string{"FTC Platform"}},
		NotBefore:             now.Add(-5 * time.Minute),
		NotAfter:              now.AddDate(10, 0, 0),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		return TLSBundle{}, err
	}
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		return TLSBundle{}, err
	}

	serverKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return TLSBundle{}, err
	}
	serverSerial, err := randomSerial()
	if err != nil {
		return TLSBundle{}, err
	}
	serverTemplate := &x509.Certificate{
		SerialNumber: serverSerial,
		Subject:      pkix.Name{CommonName: domain, Organization: []string{"FTC Platform"}},
		NotBefore:    now.Add(-5 * time.Minute),
		NotAfter:     now.AddDate(5, 0, 0),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{domain},
	}
	serverDER, err := x509.CreateCertificate(rand.Reader, serverTemplate, caCert, &serverKey.PublicKey, caKey)
	if err != nil {
		return TLSBundle{}, err
	}
	serverKeyDER, err := x509.MarshalECPrivateKey(serverKey)
	if err != nil {
		return TLSBundle{}, err
	}
	caKeyDER, err := x509.MarshalECPrivateKey(caKey)
	if err != nil {
		return TLSBundle{}, err
	}
	serverPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: serverDER})
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})
	// Include the CA in the served chain so clients that only trust the root
	// can validate the leaf without an extra intermediate file.
	chain := append(append([]byte{}, serverPEM...), caPEM...)
	return TLSBundle{
		ServerCert: chain,
		ServerKey:  pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: serverKeyDER}),
		CACert:     caPEM,
		CAKey:      pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: caKeyDER}),
	}, nil
}

func randomSerial() (*big.Int, error) {
	limit := new(big.Int).Lsh(big.NewInt(1), 128)
	return rand.Int(rand.Reader, limit)
}
