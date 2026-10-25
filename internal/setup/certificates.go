package setup

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"net"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"
)

func signWithCA(caCert, caKey []byte, domain string, ips []net.IP) (TLSBundle, error) {
	block, _ := pem.Decode(caCert)
	if block == nil {
		return TLSBundle{}, fmt.Errorf("invalid CA certificate")
	}
	ca, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return TLSBundle{}, err
	}
	if !ca.IsCA || time.Now().After(ca.NotAfter.Add(-30*24*time.Hour)) {
		return TLSBundle{}, fmt.Errorf("CA is invalid or expires within 30 days; restore or rotate CA explicitly")
	}
	keyBlock, _ := pem.Decode(caKey)
	if keyBlock == nil {
		return TLSBundle{}, fmt.Errorf("CA private key missing")
	}
	key, err := x509.ParseECPrivateKey(keyBlock.Bytes)
	if err != nil {
		return TLSBundle{}, err
	}
	if !key.PublicKey.Equal(ca.PublicKey) {
		return TLSBundle{}, fmt.Errorf("CA certificate and key mismatch")
	}
	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return TLSBundle{}, err
	}
	serial, err := randomSerial()
	if err != nil {
		return TLSBundle{}, err
	}
	expiry := time.Now().AddDate(1, 0, 0)
	if expiry.After(ca.NotAfter) {
		expiry = ca.NotAfter
	}
	template := &x509.Certificate{SerialNumber: serial, NotBefore: time.Now().Add(-5 * time.Minute), NotAfter: expiry, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, DNSNames: []string{domain}, IPAddresses: ips}
	der, err := x509.CreateCertificate(rand.Reader, template, ca, &leafKey.PublicKey, key)
	if err != nil {
		return TLSBundle{}, err
	}
	private, err := x509.MarshalECPrivateKey(leafKey)
	if err != nil {
		return TLSBundle{}, err
	}
	chain := append(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), caCert...)
	return TLSBundle{CACert: caCert, CAKey: caKey, ServerCert: chain, ServerKey: pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: private})}, nil
}

// EnsureTLS reuses the original CA after partial writes and on renewal. It
// never silently changes the trust root when a stored key is invalid.
func (server *Server) EnsureTLS(ctx context.Context, caName, secretName, domain string, ips []net.IP) (*corev1.Secret, error) {
	secrets := server.client.CoreV1().Secrets(server.config.Namespace)
	ca, err := secrets.Get(ctx, caName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		bundle, e := GenerateSelfSignedTLS(domain)
		if e != nil {
			return nil, e
		}
		ca, e = secrets.Create(ctx, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: caName, Namespace: server.config.Namespace}, Data: map[string][]byte{"ca.crt": bundle.CACert, "ca.key": bundle.CAKey}}, metav1.CreateOptions{})
		if apierrors.IsAlreadyExists(e) {
			ca, e = secrets.Get(ctx, caName, metav1.GetOptions{})
		}
		err = e
	}
	if err != nil {
		return nil, err
	}
	var result *corev1.Secret
	err = retry.RetryOnConflict(retry.DefaultRetry, func() error {
		existing, e := secrets.Get(ctx, secretName, metav1.GetOptions{})
		if e != nil && !apierrors.IsNotFound(e) {
			return e
		}
		if e == nil {
			pair, pairErr := tls.X509KeyPair(existing.Data["tls.crt"], existing.Data["tls.key"])
			if pairErr == nil {
				leaf, parseErr := x509.ParseCertificate(pair.Certificate[0])
				if parseErr == nil && leaf.VerifyHostname(domain) == nil && time.Now().After(leaf.NotBefore) && leaf.NotAfter.After(time.Now().Add(30*24*time.Hour)) {
					roots := x509.NewCertPool()
					roots.AppendCertsFromPEM(ca.Data["ca.crt"])
					_, verifyErr := leaf.Verify(x509.VerifyOptions{Roots: roots, DNSName: domain})
					for _, ip := range ips {
						if leaf.VerifyHostname(ip.String()) != nil {
							verifyErr = fmt.Errorf("node IP changed")
						}
					}
					if verifyErr == nil {
						result = existing
						return nil
					}
				}
			}
		}
		bundle, signErr := signWithCA(ca.Data["ca.crt"], ca.Data["ca.key"], domain, ips)
		if signErr != nil {
			return signErr
		}
		missing := apierrors.IsNotFound(e)
		if missing {
			existing = &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: secretName, Namespace: server.config.Namespace}}
		}
		existing.Type = corev1.SecretTypeTLS
		existing.Data = map[string][]byte{"tls.crt": bundle.ServerCert, "tls.key": bundle.ServerKey}
		if missing {
			result, e = secrets.Create(ctx, existing, metav1.CreateOptions{})
		} else {
			result, e = secrets.Update(ctx, existing, metav1.UpdateOptions{})
		}
		return e
	})
	return result, err
}

func (server *Server) BootstrapTLS(ctx context.Context) (*tls.Config, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	secrets := server.client.CoreV1().Secrets(server.config.Namespace)
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		secret, e := secrets.Get(ctx, platformBootstrapSecret, metav1.GetOptions{})
		if e != nil {
			return e
		}
		if len(secret.Data["encryption-key"]) > 0 {
			return nil
		}
		key := make([]byte, 32)
		if _, e = rand.Read(key); e != nil {
			return e
		}
		if secret.Data == nil {
			secret.Data = map[string][]byte{}
		}
		secret.Data["encryption-key"] = []byte(base64.StdEncoding.EncodeToString(key))
		_, e = secrets.Update(ctx, secret, metav1.UpdateOptions{})
		return e
	})
	if err != nil {
		return nil, err
	}
	nodes, err := server.client.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	ips := make([]net.IP, 0, len(nodes.Items))
	seen := map[string]struct{}{}
	for _, node := range nodes.Items {
		for _, address := range node.Status.Addresses {
			if address.Type == corev1.NodeInternalIP || address.Type == corev1.NodeExternalIP {
				if ip := net.ParseIP(address.Address); ip != nil && !ip.IsLoopback() {
					key := ip.String()
					if _, exists := seen[key]; !exists {
						seen[key] = struct{}{}
						ips = append(ips, ip)
					}
				}
			}
		}
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("no non-loopback Kubernetes node IP is available for the setup certificate")
	}
	secret, err := server.EnsureTLS(ctx, "ftc-platform-setup-ca", "ftc-platform-setup-tls", "setup.ftc.internal", ips)
	if err != nil {
		return nil, err
	}
	pair, err := tls.X509KeyPair(secret.Data["tls.crt"], secret.Data["tls.key"])
	if err != nil {
		return nil, err
	}
	return &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{pair}}, nil
}
