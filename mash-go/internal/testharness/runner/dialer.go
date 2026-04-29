package runner

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"time"

	"github.com/mash-protocol/mash-go/pkg/cert"
	"github.com/mash-protocol/mash-go/pkg/transport"
)

// Dialer abstracts TLS connection establishment for testability.
type Dialer interface {
	// DialCommissioning dials a commissioning TLS connection (PASE phase).
	DialCommissioning(ctx context.Context, target string) (*tls.Conn, error)

	// DialOperational dials an operational TLS connection using zone crypto.
	//
	// zoneID is the post-PASE zone identifier (16 hex chars) when known,
	// empty string when not. SRV-resolving dialers use it to filter
	// `_mash._tcp` mDNS records and pick the correct device's operational
	// port -- mandatory under DEC-082 two-port deployments where the
	// commissioning and operational ports differ. The base TLS dialer
	// ignores this hint and dials the literal target.
	DialOperational(ctx context.Context, target, zoneID string, crypto CryptoState) (*tls.Conn, error)
}

// tlsDialer is the production Dialer that uses real TLS connections.
type tlsDialer struct {
	insecureSkipVerify bool
	debugf             func(string, ...any)
}

// NewDialer creates a Dialer that performs real TLS connections.
func NewDialer(insecureSkipVerify bool, debugf func(string, ...any)) Dialer {
	return &tlsDialer{
		insecureSkipVerify: insecureSkipVerify,
		debugf:             debugf,
	}
}

// DialCommissioning dials a commissioning TLS connection using the
// standard commissioning TLS config (mash-comm/1 ALPN, no client certs).
func (d *tlsDialer) DialCommissioning(_ context.Context, target string) (*tls.Conn, error) {
	tlsConfig := transport.NewCommissioningTLSConfig()
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	conn, err := tls.DialWithDialer(dialer, "tcp", target, tlsConfig)
	if err != nil {
		return nil, fmt.Errorf("commissioning dial failed: %w", err)
	}
	return conn, nil
}

// DialOperational dials an operational TLS connection using zone crypto
// material (controller cert + zone CA for peer verification). The base
// dialer ignores zoneID -- it has no service discovery to filter; the
// SRV-resolving wrapper uses zoneID to pick the right operational port
// under DEC-082 two-port deployments.
func (d *tlsDialer) DialOperational(_ context.Context, target, _ string, crypto CryptoState) (*tls.Conn, error) {
	tlsConfig := buildOperationalTLSConfig(crypto, d.insecureSkipVerify, d.debugf)
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	conn, err := tls.DialWithDialer(dialer, "tcp", target, tlsConfig)
	if err != nil {
		return nil, fmt.Errorf("operational dial failed: %w", err)
	}
	return conn, nil
}

// buildOperationalTLSConfig builds a TLS config for operational (post-commissioning)
// connections. It uses the Zone CA for chain validation but skips hostname
// verification since MASH identifies peers by device ID in the certificate CN.
func buildOperationalTLSConfig(crypto CryptoState, insecureSkipVerify bool, debugf func(string, ...any)) *tls.Config {
	tlsConfig := &tls.Config{
		MinVersion: tls.VersionTLS13,
		NextProtos: []string{transport.ALPNProtocol},
		// Explicit curve preferences to match the MASH spec and avoid
		// Go 1.24+ defaulting to post-quantum X25519MLKEM768.
		CurvePreferences: []tls.CurveID{tls.X25519, tls.CurveP256},
	}

	if crypto.ZoneCAPool != nil {
		// Skip hostname verification but validate the cert chain against Zone CA.
		tlsConfig.InsecureSkipVerify = true
		tlsConfig.VerifyPeerCertificate = func(rawCerts [][]byte, chains [][]*x509.Certificate) error {
			return verifyPeerCert(rawCerts, chains, crypto.ZoneCAPool, crypto.ZoneCA, debugf)
		}
	} else if insecureSkipVerify {
		tlsConfig.InsecureSkipVerify = true
	}

	// Present controller cert for mutual TLS if available.
	if crypto.ControllerCert != nil {
		tlsConfig.Certificates = []tls.Certificate{crypto.ControllerCert.TLSCertificate()}
	}
	if crypto.IssuedDeviceCert != nil && crypto.IssuedDeviceCert.Subject.CommonName != "" {
		tlsConfig.ServerName = crypto.IssuedDeviceCert.Subject.CommonName
	}

	return tlsConfig
}

// verifyPeerCert validates the peer certificate chain against the Zone CA pool
// without checking hostname. This is used for operational connections where
// MASH identifies peers by device ID in the cert CN.
func verifyPeerCert(rawCerts [][]byte, _ [][]*x509.Certificate, caPool *x509.CertPool, zoneCA *cert.ZoneCA, debugf func(string, ...any)) error {
	if len(rawCerts) == 0 {
		return fmt.Errorf("no peer certificates presented")
	}

	// Parse the leaf certificate.
	leaf, err := x509.ParseCertificate(rawCerts[0])
	if err != nil {
		return fmt.Errorf("parse peer certificate: %w", err)
	}

	// Build intermediate pool from any additional certs.
	intermediates := x509.NewCertPool()
	for _, raw := range rawCerts[1:] {
		c, err := x509.ParseCertificate(raw)
		if err != nil {
			continue
		}
		intermediates.AddCert(c)
	}

	// Verify the chain against the Zone CA pool.
	opts := x509.VerifyOptions{
		Roots:         caPool,
		Intermediates: intermediates,
	}

	if _, err := leaf.Verify(opts); err != nil {
		leafFP := sha256.Sum256(leaf.Raw)
		issuerFP := sha256.Sum256(leaf.RawIssuer)
		debugf("verifyPeerCert FAILED: leaf.CN=%s leaf.FP=%x issuer.FP=%x pool=%p err=%v",
			leaf.Subject.CommonName, leafFP[:4], issuerFP[:4], caPool, err)
		if zoneCA != nil {
			caFP := sha256.Sum256(zoneCA.Certificate.Raw)
			debugf("verifyPeerCert: zoneCA.CN=%s zoneCA.FP=%x",
				zoneCA.Certificate.Subject.CommonName, caFP[:4])
		}
		return fmt.Errorf("certificate chain verification failed: %w", err)
	}

	return nil
}
