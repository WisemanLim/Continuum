package pki

import (
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Ticket lets agent Dst read Path from agent Src directly (D36: the Controller authorizes, never relays bytes).
// It is signed with the CA key, so every enrolled agent can verify it offline with ca.pem.
type Ticket struct {
	Src  string `json:"src"`
	Dst  string `json:"dst"`
	Path string `json:"path"`
	Exp  int64  `json:"exp"` // unix seconds
}

// SignTicket issues a ticket valid for ttl.
func (ca *CA) SignTicket(src, dst, path string, ttl time.Duration) (string, error) {
	b, _ := json.Marshal(Ticket{Src: src, Dst: dst, Path: path, Exp: time.Now().Add(ttl).Unix()})
	sum := sha256.Sum256(b)
	sig, err := ecdsa.SignASN1(rand.Reader, ca.Key, sum[:])
	if err != nil {
		return "", err
	}
	enc := base64.RawURLEncoding
	return enc.EncodeToString(b) + "." + enc.EncodeToString(sig), nil
}

// VerifyTicket checks the CA signature and expiry.
func VerifyTicket(caCert *x509.Certificate, s string) (Ticket, error) {
	var t Ticket
	body, sigS, ok := strings.Cut(s, ".")
	if !ok {
		return t, errors.New("malformed ticket")
	}
	enc := base64.RawURLEncoding
	b, err1 := enc.DecodeString(body)
	sig, err2 := enc.DecodeString(sigS)
	if err1 != nil || err2 != nil {
		return t, errors.New("malformed ticket")
	}
	pub, ok := caCert.PublicKey.(*ecdsa.PublicKey)
	if !ok {
		return t, errors.New("CA key is not ECDSA")
	}
	sum := sha256.Sum256(b)
	if !ecdsa.VerifyASN1(pub, sum[:], sig) {
		return t, errors.New("ticket signature invalid")
	}
	if err := json.Unmarshal(b, &t); err != nil {
		return t, err
	}
	if time.Now().Unix() > t.Exp {
		return t, fmt.Errorf("ticket expired %s", time.Unix(t.Exp, 0).UTC().Format(time.RFC3339))
	}
	return t, nil
}

// VerifyNodeCert checks that a peer certificate chains to the CA (any EKU: agent certificates are client
// certificates also used to serve peers) and returns its node id.
func VerifyNodeCert(caCert *x509.Certificate, rawCerts [][]byte) (string, error) {
	if len(rawCerts) == 0 {
		return "", errors.New("no peer certificate")
	}
	leaf, err := x509.ParseCertificate(rawCerts[0])
	if err != nil {
		return "", err
	}
	pool := x509.NewCertPool()
	pool.AddCert(caCert)
	if _, err := leaf.Verify(x509.VerifyOptions{Roots: pool, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageAny}}); err != nil {
		return "", err
	}
	for _, u := range leaf.URIs {
		if u.Scheme == "continuum" && u.Host == "node" && strings.TrimPrefix(u.Path, "/") == leaf.Subject.CommonName {
			return leaf.Subject.CommonName, nil
		}
	}
	return "", fmt.Errorf("certificate SAN does not name node %q", leaf.Subject.CommonName)
}
