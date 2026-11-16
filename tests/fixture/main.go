package main

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
	"fmt"
	"log"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func main() {
	root := "/tmp/awg-feed"
	if err := os.MkdirAll(root, 0o700); err != nil {
		log.Fatal(err)
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		log.Fatal(err)
	}
	cert := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "AWG fixture"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, IPAddresses: []net.IP{net.ParseIP("5.45.192.1")}}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if err != nil {
		log.Fatal(err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	if err := os.WriteFile(filepath.Join(root, "ca.pem"), certPEM, 0o600); err != nil {
		log.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		log.Fatal(err)
	}
	pair, err := tls.X509KeyPair(certPEM, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}))
	if err != nil {
		log.Fatal(err)
	}
	server := &http.Server{Addr: ":8443", ReadHeaderTimeout: 3 * time.Second, TLSConfig: &tls.Config{Certificates: []tls.Certificate{pair}, MinVersion: tls.VersionTLS12}, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/rules.txt" && r.URL.Path != "/rules.sha256" {
			http.NotFound(w, r)
			return
		}
		if _, err := os.Stat(filepath.Join(root, "slow")); err == nil {
			select {
			case <-time.After(10 * time.Second):
			case <-r.Context().Done():
				return
			}
		}
		raw, err := os.ReadFile(filepath.Join(root, "rules.txt"))
		if err != nil {
			http.Error(w, "missing fixture", 404)
			return
		}
		sum := sha256.Sum256(raw)
		hash := hex.EncodeToString(sum[:])
		peer, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			http.Error(w, "peer", 500)
			return
		}
		file, err := os.OpenFile(filepath.Join(root, "peers.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			http.Error(w, "log", 500)
			return
		}
		_, writeErr := fmt.Fprintln(file, peer)
		closeErr := file.Close()
		if writeErr != nil || closeErr != nil {
			http.Error(w, "log", 500)
			return
		}
		if strings.HasSuffix(r.URL.Path, "sha256") {
			if _, err := os.Stat(filepath.Join(root, "bad-sha")); err == nil {
				hash = strings.Repeat("0", 64)
			}
			if _, err := fmt.Fprintln(w, hash+"  rules.txt"); err != nil {
				log.Print("response failed")
			}
			return
		}
		etag := "\"" + hash + "\""
		w.Header().Set("ETag", etag)
		if r.Header.Get("If-None-Match") == etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		if _, err := w.Write(raw); err != nil {
			log.Print("response failed")
		}
	})}
	log.Fatal(server.ListenAndServeTLS("", ""))
}
