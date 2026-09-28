package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"rassvet-chat/service/internal/api"
	"rassvet-chat/service/internal/bootstrap"
	"rassvet-chat/service/internal/nodetls"
	"rassvet-chat/service/migrations"
)

var version = "dev"

func main() {
	if len(os.Args) == 2 && os.Args[1] == "version" {
		fmt.Println("rassvetd", version)
		return
	}
	if len(os.Args) == 3 && os.Args[1] == "init-db" {
		db, err := migrations.Open(os.Args[2])
		if err == nil {
			err = db.Close()
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if (len(os.Args) == 7 && os.Args[1] == "init-admin") || (len(os.Args) == 6 && os.Args[1] == "reissue-admin") {
		nodes, issuer, err := bootstrapNodes(os.Args[len(os.Args)-3], os.Args[len(os.Args)-2], os.Args[len(os.Args)-1])
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		db, err := migrations.Open(os.Args[2])
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		var id, code string
		if os.Args[1] == "init-admin" {
			id, code, err = bootstrap.Admin(db, os.Args[3], time.Now())
		} else {
			id, code, err = bootstrap.ReissueAdminCode(db, time.Now())
		}
		_ = db.Close()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Fprintln(os.Stderr, "admin_user_id:", id)
		if err := json.NewEncoder(os.Stdout).Encode(map[string]any{"code": code, "issuer": issuer, "nodes": nodes}); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if (len(os.Args) == 4 || len(os.Args) == 5) && os.Args[1] == "init-node" {
		cert, key, err := nodetls.Generate(os.Args[2], os.Args[4:]...)
		if err == nil {
			err = os.Mkdir(os.Args[3], 0700)
		}
		if err == nil {
			err = os.WriteFile(os.Args[3]+"/key.pem", key, 0600)
		}
		if err == nil {
			err = os.WriteFile(os.Args[3]+"/cert.pem", cert, 0644)
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) == 3 && os.Args[1] == "node-pin" {
		cert, err := os.ReadFile(os.Args[2])
		if err == nil {
			var pin string
			pin, err = nodetls.SPKIHash(cert)
			if err == nil {
				fmt.Println(pin)
			}
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) == 10 && os.Args[1] == "serve" {
		db, err := migrations.Open(os.Args[2])
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		defer db.Close()
		nodesJSON, err := os.ReadFile(os.Args[6])
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		nodes, err := api.ParseNodes(nodesJSON)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if err := os.MkdirAll(os.Args[7], 0700); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		storage, err := os.Stat(os.Args[7])
		if err != nil || !storage.IsDir() || storage.Mode().Perm() != 0700 {
			fmt.Fprintln(os.Stderr, "attachment directory must be a directory with permissions 0700")
			os.Exit(1)
		}
		if err := api.PruneExpired(db, time.Now(), os.Args[7]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		certPEM, err := os.ReadFile(os.Args[4])
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		keyPEM, err := os.ReadFile(os.Args[5])
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		peerCertPEM, err := os.ReadFile(os.Args[8])
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		serverTLS, err := nodetls.OptionalPeerServer(certPEM, keyPEM, peerCertPEM, os.Args[9])
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		ownID, err := nodetls.CertificateID(certPEM)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		_, clientTLS, err := nodetls.Config(certPEM, keyPEM, peerCertPEM, os.Args[9])
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		_, peerURL, err := validatedNodeURLs(nodes, certPEM, peerCertPEM)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if err := api.ConfigurePeer(db, ownID); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		var workers sync.WaitGroup
		workers.Add(2)
		go func() {
			defer workers.Done()
			ticker := time.NewTicker(time.Minute)
			defer ticker.Stop()
			for {
				select {
				case now := <-ticker.C:
					if err := api.PruneExpired(db, now, os.Args[7]); err != nil {
						fmt.Fprintln(os.Stderr, err)
					}
				case <-ctx.Done():
					return
				}
			}
		}()
		go func() {
			defer workers.Done()
			ticker := time.NewTicker(10 * time.Second)
			defer ticker.Stop()
			lastError := ""
			for {
				err := api.SyncPeerOnce(ctx, db, peerURL, os.Args[9], os.Args[7], clientTLS)
				if err != nil && ctx.Err() == nil && err.Error() != lastError {
					fmt.Fprintln(os.Stderr, "peer sync:", err)
					lastError = err.Error()
				}
				if err == nil {
					lastError = ""
				}
				select {
				case <-ticker.C:
				case <-ctx.Done():
					return
				}
			}
		}()
		server := &http.Server{Addr: os.Args[3], Handler: api.HandlerWithPeerStorage(db, os.Args[7], ownID, nodes...), ReadHeaderTimeout: 10 * time.Second, TLSConfig: serverTLS}
		shutdownDone := make(chan struct{})
		go func() {
			defer close(shutdownDone)
			<-ctx.Done()
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if err := server.Shutdown(shutdownCtx); err != nil {
				fmt.Fprintln(os.Stderr, "shutdown:", err)
				_ = server.Close()
			}
		}()
		serveErr := server.ListenAndServeTLS(os.Args[4], os.Args[5])
		stop()
		<-shutdownDone
		workers.Wait()
		if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			fmt.Fprintln(os.Stderr, serveErr)
			os.Exit(1)
		}
		return
	}
	fmt.Fprintln(os.Stderr, "usage: rassvetd version | init-db <path> | init-admin <db> <name> <nodes.json> <own-cert> <peer-cert> | reissue-admin <db> <nodes.json> <own-cert> <peer-cert> | init-node <id> <dir> [address] | node-pin <cert> | serve <db> <addr> <cert> <key> <nodes.json> <attachments-dir> <peer-cert> <peer-id>")
	os.Exit(2)
}

func bootstrapNodes(nodesPath, ownCertPath, peerCertPath string) ([]api.Node, string, error) {
	nodesJSON, err := os.ReadFile(nodesPath)
	if err != nil {
		return nil, "", err
	}
	nodes, err := api.ParseNodes(nodesJSON)
	if err != nil {
		return nil, "", err
	}
	ownCert, err := os.ReadFile(ownCertPath)
	if err != nil {
		return nil, "", err
	}
	peerCert, err := os.ReadFile(peerCertPath)
	if err != nil {
		return nil, "", err
	}
	issuer, _, err := validatedNodeURLs(nodes, ownCert, peerCert)
	return nodes, issuer, err
}

func validatedNodeURLs(nodes []api.Node, ownCert, peerCert []byte) (string, string, error) {
	ownPin, err := nodetls.SPKIHash(ownCert)
	if err != nil {
		return "", "", err
	}
	peerPin, err := nodetls.SPKIHash(peerCert)
	if err != nil {
		return "", "", err
	}
	peerURL, err := api.PeerURL(nodes, ownPin, peerPin)
	if err != nil {
		return "", "", err
	}
	issuer := ""
	for _, node := range nodes {
		cert := peerCert
		if node.TLSSPKISHA256 == ownPin {
			cert = ownCert
			issuer = node.URL
		}
		address, err := url.Parse(node.URL)
		if err != nil {
			return "", "", err
		}
		if err := nodetls.VerifyHostname(cert, address.Hostname()); err != nil {
			return "", "", fmt.Errorf("node URL %s does not match certificate: %w", node.URL, err)
		}
	}
	return issuer, peerURL, nil
}
