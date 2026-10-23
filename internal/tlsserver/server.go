// Package tlsserver serves TLS from projected Secret files and keeps the last
// valid pair during a non-atomic projection change. No private key is logged.
package tlsserver

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"log"
	"sync/atomic"
	"time"
)

func FromFiles(ctx context.Context, certFile, keyFile string) (*tls.Config, error) {
	var current atomic.Pointer[tls.Certificate]
	load := func() error {
		pair, err := tls.LoadX509KeyPair(certFile, keyFile)
		if err != nil {
			return err
		}
		leaf, err := x509.ParseCertificate(pair.Certificate[0])
		if err != nil {
			return err
		}
		now := time.Now()
		if now.Before(leaf.NotBefore) || !now.Before(leaf.NotAfter) {
			return fmt.Errorf("TLS certificate is outside its validity period")
		}
		pair.Leaf = leaf
		current.Store(&pair)
		return nil
	}
	if err := load(); err != nil {
		return nil, err
	}
	go func() {
		tick := time.NewTicker(5 * time.Second)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				if err := load(); err != nil {
					log.Print("TLS certificate reload rejected; retaining last valid pair")
				}
			}
		}
	}()
	return &tls.Config{MinVersion: tls.VersionTLS12, GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) { return current.Load(), nil }}, nil
}
