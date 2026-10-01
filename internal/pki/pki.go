// Package pki is the Controller's private CA for Agent mTLS (FR-S03, FR-N01, FR-N09).
// The CA key never leaves the Controller host (file 0600); Agents get a client certificate whose
// CommonName and URI SAN carry the node id, and the Controller checks the presented serial against
// the registry, so a rotated or revoked certificate stops working immediately.
package pki

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ClientCertTTL is the agent certificate lifetime; agents renew when a third is left.
const ClientCertTTL = 90 * 24 * time.Hour

// CA holds the Controller CA.
type CA struct {
	Cert    *x509.Certificate
	Key     *ecdsa.PrivateKey
	CertPEM []byte
}

// LoadOrCreate loads <dir>/ca.pem + ca-key.pem or creates a new 10-year CA.
func LoadOrCreate(dir, controllerID string) (*CA, error) {
	certPath, keyPath := filepath.Join(dir, "ca.pem"), filepath.Join(dir, "ca-key.pem")
	if cp, err := os.ReadFile(certPath); err == nil {
		kp, err := os.ReadFile(keyPath)
		if err != nil {
			return nil, err
		}
		return parse(cp, kp)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber:          serial(),
		Subject:               pkix.Name{CommonName: "Continuum CA " + controllerID, Organization: []string{"Continuum"}},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().AddDate(10, 0, 0),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true, IsCA: true, MaxPathLenZero: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}
	cp := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	kb, _ := x509.MarshalECPrivateKey(key)
	kp := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb})
	if err := os.WriteFile(keyPath, kp, 0o600); err != nil {
		return nil, err
	}
	if err := os.WriteFile(certPath, cp, 0o644); err != nil {
		return nil, err
	}
	return parse(cp, kp)
}

func parse(certPEM, keyPEM []byte) (*CA, error) {
	cb, _ := pem.Decode(certPEM)
	kb, _ := pem.Decode(keyPEM)
	if cb == nil || kb == nil {
		return nil, errors.New("pki: bad CA PEM")
	}
	cert, err := x509.ParseCertificate(cb.Bytes)
	if err != nil {
		return nil, err
	}
	key, err := x509.ParseECPrivateKey(kb.Bytes)
	if err != nil {
		return nil, err
	}
	return &CA{Cert: cert, Key: key, CertPEM: certPEM}, nil
}

func serial() *big.Int {
	n, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 126))
	return n
}

// Fingerprint is the SHA-256 of the CA certificate (pinned by agents at enrollment).
func (ca *CA) Fingerprint() string { return Fingerprint(ca.Cert.Raw) }

// Fingerprint of a DER certificate.
func Fingerprint(der []byte) string {
	h := sha256.Sum256(der)
	return hex.EncodeToString(h[:])
}

// Pool returns a cert pool with only this CA.
func (ca *CA) Pool() *x509.CertPool {
	p := x509.NewCertPool()
	p.AddCert(ca.Cert)
	return p
}

// ServerTLS returns a fresh server certificate for the agent listener (hosts = DNS names or IPs).
// Client certificates are requested but verified by the caller (enrollment runs without one).
func (ca *CA) ServerTLS(hosts []string) (*tls.Config, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial(),
		Subject:      pkix.Name{CommonName: "continuum-controller"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().AddDate(1, 0, 0),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	for _, h := range hosts {
		if h = strings.TrimSpace(h); h == "" {
			continue
		}
		if ip := net.ParseIP(h); ip != nil {
			tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
		} else {
			tmpl.DNSNames = append(tmpl.DNSNames, h)
		}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.Cert, &key.PublicKey, ca.Key)
	if err != nil {
		return nil, err
	}
	return &tls.Config{
		MinVersion:   tls.VersionTLS13, // FR-S07
		Certificates: []tls.Certificate{{Certificate: [][]byte{der, ca.Cert.Raw}, PrivateKey: key}},
		ClientAuth:   tls.VerifyClientCertIfGiven,
		ClientCAs:    ca.Pool(),
		NextProtos:   []string{"h2", "http/1.1"},
	}, nil
}

// NodeURI is the URI SAN identifying an agent.
func NodeURI(nodeID string) *url.URL {
	return &url.URL{Scheme: "continuum", Host: "node", Path: "/" + nodeID}
}

// IssueClient signs an agent certificate for the CSR-less flow: the agent sends its public key (PEM).
func (ca *CA) IssueClient(nodeID string, pubPEM []byte) (certPEM []byte, serialHex string, notAfter time.Time, err error) {
	b, _ := pem.Decode(pubPEM)
	if b == nil {
		return nil, "", time.Time{}, errors.New("pki: bad public key PEM")
	}
	pub, err := x509.ParsePKIXPublicKey(b.Bytes)
	if err != nil {
		return nil, "", time.Time{}, err
	}
	sn := serial()
	notAfter = time.Now().Add(ClientCertTTL)
	tmpl := &x509.Certificate{
		SerialNumber: sn,
		Subject:      pkix.Name{CommonName: nodeID, Organization: []string{"Continuum Agent"}},
		URIs:         []*url.URL{NodeURI(nodeID)},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     notAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.Cert, pub, ca.Key)
	if err != nil {
		return nil, "", time.Time{}, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), sn.Text(16), notAfter, nil
}

// PeerNode extracts and checks the node id of a verified client certificate (SAN must match CN).
func PeerNode(cs *tls.ConnectionState) (nodeID, serialHex string, err error) {
	if cs == nil || len(cs.VerifiedChains) == 0 || len(cs.PeerCertificates) == 0 {
		return "", "", errors.New("client certificate required")
	}
	c := cs.PeerCertificates[0]
	for _, u := range c.URIs {
		if u.Scheme == "continuum" && u.Host == "node" && strings.TrimPrefix(u.Path, "/") == c.Subject.CommonName {
			return c.Subject.CommonName, c.SerialNumber.Text(16), nil
		}
	}
	return "", "", fmt.Errorf("certificate SAN does not name node %q", c.Subject.CommonName)
}

// NewKey creates an agent key pair; returns private key PEM and public key PEM.
func NewKey() (keyPEM, pubPEM []byte, err error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	kb, _ := x509.MarshalECPrivateKey(key)
	pb, _ := x509.MarshalPKIXPublicKey(&key.PublicKey)
	return pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb}), pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pb}), nil
}

// LocalHosts lists names/IPs this host answers on (server certificate SANs).
func LocalHosts(extra ...string) []string {
	hosts := []string{"localhost", "127.0.0.1", "::1", "host.docker.internal"}
	if h, err := os.Hostname(); err == nil {
		hosts = append(hosts, h)
	}
	if addrs, err := net.InterfaceAddrs(); err == nil {
		for _, a := range addrs {
			if ipn, ok := a.(*net.IPNet); ok && !ipn.IP.IsLinkLocalUnicast() {
				hosts = append(hosts, ipn.IP.String())
			}
		}
	}
	return append(hosts, extra...)
}
