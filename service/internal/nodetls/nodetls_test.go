package nodetls

import (
	"crypto/tls"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMutualTrust(t *testing.T) {
	aCert, aKey, err := Generate("node-a")
	if err != nil {
		t.Fatal(err)
	}
	bCert, bKey, err := Generate("node-b")
	if err != nil {
		t.Fatal(err)
	}
	forgedCert, forgedKey, err := Generate("node-c")
	if err != nil {
		t.Fatal(err)
	}
	aServer, _, err := Config(aCert, aKey, bCert, "node-b")
	if err != nil {
		t.Fatal(err)
	}
	_, bClient, err := Config(bCert, bKey, aCert, "node-a")
	if err != nil {
		t.Fatal(err)
	}
	listener, err := tls.Listen("tcp", "127.0.0.1:0", aServer)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	handshakes := make(chan error, 3)
	go func() {
		for i := 0; i < 3; i++ {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			if tlsConn, ok := conn.(*tls.Conn); ok {
				handshakes <- tlsConn.Handshake()
			}
			_ = conn.Close()
		}
	}()
	connect := func(config *tls.Config) error {
		conn, err := tls.DialWithDialer(&net.Dialer{}, "tcp", listener.Addr().String(), config)
		if err == nil {
			_ = conn.Close()
		}
		return err
	}
	if err := connect(bClient); err != nil {
		t.Fatalf("trusted peer rejected: %v", err)
	}
	if err := <-handshakes; err != nil {
		t.Fatalf("trusted peer rejected by server: %v", err)
	}
	forgedIdentity, err := tls.X509KeyPair(forgedCert, forgedKey)
	if err != nil {
		t.Fatal(err)
	}
	forgedClient := bClient.Clone()
	forgedClient.Certificates = []tls.Certificate{forgedIdentity}
	_ = connect(forgedClient)
	if err := <-handshakes; err == nil {
		t.Fatal("forged client accepted")
	}
	_, wrongServerTrust, err := Config(bCert, bKey, forgedCert, "node-c")
	if err != nil {
		t.Fatal(err)
	}
	if err := connect(wrongServerTrust); err == nil {
		t.Fatal("forged server trust accepted")
	}
}

func TestDistinctNodeIDsOnSharedAddress(t *testing.T) {
	for _, id := range []string{"node-a", "node-b"} {
		cert, _, err := Generate(id, "192.168.0.98")
		if err != nil {
			t.Fatal(err)
		}
		if got, err := CertificateID(cert); err != nil || got != id {
			t.Fatalf("certificate ID = %q, %v", got, err)
		}
		for _, host := range []string{id, "192.168.0.98"} {
			if err := VerifyHostname(cert, host); err != nil {
				t.Fatalf("certificate %s missing %s: %v", id, host, err)
			}
		}
	}
}

func TestOptionalPeerServer(t *testing.T) {
	aCert, aKey, err := Generate("node-a")
	if err != nil {
		t.Fatal(err)
	}
	bCert, bKey, err := Generate("node-b")
	if err != nil {
		t.Fatal(err)
	}
	wrongCert, wrongKey, err := Generate("node-c")
	if err != nil {
		t.Fatal(err)
	}
	serverTLS, err := OptionalPeerServer(aCert, aKey, bCert, "node-b")
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if len(r.TLS.PeerCertificates) == 0 {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	server.TLS = serverTLS
	server.StartTLS()
	defer server.Close()
	_, trustedClient, err := Config(bCert, bKey, aCert, "node-a")
	if err != nil {
		t.Fatal(err)
	}
	request := func(config *tls.Config) (*http.Response, error) {
		client := &http.Client{Transport: &http.Transport{TLSClientConfig: config}}
		defer client.CloseIdleConnections()
		return client.Get(server.URL)
	}
	response, err := request(trustedClient)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("trusted peer: %d", response.StatusCode)
	}
	withoutCert := trustedClient.Clone()
	withoutCert.Certificates = nil
	response, err = request(withoutCert)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("ordinary client: %d", response.StatusCode)
	}
	forged := trustedClient.Clone()
	forgedIdentity, err := tls.X509KeyPair(wrongCert, wrongKey)
	if err != nil {
		t.Fatal(err)
	}
	forged.Certificates = []tls.Certificate{forgedIdentity}
	forged.GetClientCertificate = func(*tls.CertificateRequestInfo) (*tls.Certificate, error) { return &forgedIdentity, nil }
	if response, err = request(forged); err == nil {
		response.Body.Close()
		t.Fatal("forged peer accepted")
	}
}
