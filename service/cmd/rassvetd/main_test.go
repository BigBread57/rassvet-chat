package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"rassvet-chat/service/internal/api"
	"rassvet-chat/service/internal/nodetls"
)

func TestBootstrapNodesUseInstalledCertificates(t *testing.T) {
	first, _, err := nodetls.Generate("node-a")
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := nodetls.Generate("node-b")
	if err != nil {
		t.Fatal(err)
	}
	pinA, err := nodetls.SPKIHash(first)
	if err != nil {
		t.Fatal(err)
	}
	pinB, err := nodetls.SPKIHash(second)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	nodesPath, firstPath, secondPath := filepath.Join(dir, "nodes.json"), filepath.Join(dir, "a.pem"), filepath.Join(dir, "b.pem")
	nodesJSON, err := json.Marshal([]api.Node{{URL: "https://node-a", TLSSPKISHA256: pinA}, {URL: "https://node-b", TLSSPKISHA256: pinB}})
	if err != nil {
		t.Fatal(err)
	}
	for path, data := range map[string][]byte{nodesPath: nodesJSON, firstPath: first, secondPath: second} {
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if nodes, issuer, err := bootstrapNodes(nodesPath, firstPath, secondPath); err != nil || issuer != "https://node-a" || len(nodes) != 2 {
		t.Fatalf("bootstrap nodes=%v issuer=%q err=%v", nodes, issuer, err)
	}
	if _, _, err := bootstrapNodes(nodesPath, secondPath, secondPath); err == nil {
		t.Fatal("same certificate was accepted for both nodes")
	}
	wrongHost, err := json.Marshal([]api.Node{{URL: "https://node-a", TLSSPKISHA256: pinA}, {URL: "https://other-host", TLSSPKISHA256: pinB}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(nodesPath, wrongHost, 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := bootstrapNodes(nodesPath, firstPath, secondPath); err == nil {
		t.Fatal("node URL with the wrong certificate hostname was accepted")
	}
}
