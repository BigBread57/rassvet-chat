package nodetls

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"time"
)

// Generate создаёт самоподписанную идентичность узла для выдачи при установке.
// Закрытый ключ сохраняется установщиком с правами только владельца.
func Generate(nodeID string, addresses ...string) (certPEM, keyPEM []byte, err error) {
	if nodeID == "" {
		return nil, nil, errors.New("empty node ID")
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, nil, err
	}
	now := time.Now()
	template := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: nodeID},
		NotBefore:             now.Add(-5 * time.Minute),
		NotAfter:              now.AddDate(1, 0, 0),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
	}
	for _, address := range append([]string{nodeID}, addresses...) {
		if address == "" {
			return nil, nil, errors.New("empty certificate address")
		}
		if ip := net.ParseIP(address); ip != nil {
			template.IPAddresses = append(template.IPAddresses, ip)
		} else {
			template.DNSNames = append(template.DNSNames, address)
		}
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return nil, nil, err
	}
	privateDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: privateDER}), nil
}

// Config доверяет ровно указанному сертификату второго узла.
func Config(certPEM, keyPEM, peerCertPEM []byte, peerID string) (server, client *tls.Config, err error) {
	if peerID == "" {
		return nil, nil, errors.New("empty peer ID")
	}
	identity, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, nil, err
	}
	peerBlock, _ := pem.Decode(peerCertPEM)
	if peerBlock == nil || peerBlock.Type != "CERTIFICATE" {
		return nil, nil, errors.New("invalid peer certificate")
	}
	peer, err := x509.ParseCertificate(peerBlock.Bytes)
	if err != nil {
		return nil, nil, err
	}
	if err := peer.VerifyHostname(peerID); err != nil {
		return nil, nil, err
	}
	pool := x509.NewCertPool()
	pool.AddCert(peer)
	server = &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{identity},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    pool,
	}
	client = &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{identity},
		RootCAs:      pool,
		ServerName:   peerID,
	}
	return server, client, nil
}

// OptionalPeerServer принимает обычных клиентов без сертификата, а предъявивших
// сертификат допускает только с точной идентичностью настроенного второго узла.
func OptionalPeerServer(certPEM, keyPEM, peerCertPEM []byte, peerID string) (*tls.Config, error) {
	server, _, err := Config(certPEM, keyPEM, peerCertPEM, peerID)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(peerCertPEM)
	peer, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, err
	}
	server.ClientAuth = tls.RequestClientCert
	server.VerifyConnection = func(state tls.ConnectionState) error {
		if len(state.PeerCertificates) == 0 {
			return nil
		}
		if len(state.PeerCertificates) != 1 || !bytes.Equal(state.PeerCertificates[0].Raw, peer.Raw) {
			return errors.New("untrusted peer certificate")
		}
		_, err := peer.Verify(x509.VerifyOptions{
			Roots:     server.ClientCAs,
			DNSName:   peerID,
			KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		})
		return err
	}
	return server, nil
}

func SPKIHash(certPEM []byte) (string, error) {
	block, _ := pem.Decode(certPEM)
	if block == nil || block.Type != "CERTIFICATE" {
		return "", errors.New("invalid certificate")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(cert.RawSubjectPublicKeyInfo)
	return base64.StdEncoding.EncodeToString(hash[:]), nil
}

func CertificateID(certPEM []byte) (string, error) {
	block, _ := pem.Decode(certPEM)
	if block == nil || block.Type != "CERTIFICATE" {
		return "", errors.New("invalid certificate")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return "", err
	}
	return cert.Subject.CommonName, nil
}

func VerifyHostname(certPEM []byte, hostname string) error {
	block, _ := pem.Decode(certPEM)
	if block == nil || block.Type != "CERTIFICATE" {
		return errors.New("invalid certificate")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return err
	}
	return cert.VerifyHostname(hostname)
}
