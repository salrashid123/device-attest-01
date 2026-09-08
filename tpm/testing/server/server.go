package main

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"

	"flag"
	"os"

	"github.com/gorilla/mux"
	"golang.org/x/net/http2"
)

var (
	stepCACertPath    = flag.String("stepCACertPath", "/home/srashid/.step/certs/intermediate_ca.crt", "Step CA which issued the client cert")
	testTLSServerCert = flag.String("testTLSServerCert", "../../certs/server.crt", "tls test server Certificate")
	testTLSServerKey  = flag.String("testTLSServerKey", "../../certs/server.key", "tls test server key")
)

func main() {
	os.Exit(run()) // since defer func() needs to get called first
}

func run() int {
	flag.Parse()

	// *************************************************************************
	fmt.Println("Starting mTLS Server")

	// load the tls server's TLS certs
	defaultServerCerts, err := tls.LoadX509KeyPair(*testTLSServerCert, *testTLSServerKey)
	if err != nil {
		fmt.Printf("failed to load test server certificates  error=%v", err)
		return 1
	}
	clientCertPool := x509.NewCertPool()
	ca_pem, err := os.ReadFile(*stepCACertPath)
	if err != nil {
		fmt.Printf("failed to load root CA certificates  error=%v", err)
		return 1
	}
	if !clientCertPool.AppendCertsFromPEM(ca_pem) {
		fmt.Printf("no root CA certs parsed from file ")
		return 1
	}

	router := mux.NewRouter()
	router.Methods(http.MethodGet).Path("/").HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "ok")
	})
	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{defaultServerCerts}, // the servers listener tls cert
		MinVersion:   tls.VersionTLS13,
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    clientCertPool, // the CA that the client cert must be signed by
		VerifyPeerCertificate: func(rawCerts [][]byte, verifiedChains [][]*x509.Certificate) error {

			// lists out the remote peers (i.e the client certs sent over to the server)
			for _, rawCert := range rawCerts {
				c, err := x509.ParseCertificate(rawCert)
				if err != nil {
					return err
				}
				fmt.Printf("Server connected with client certificate Issuer %s\n", c.Issuer)
				fmt.Printf("Server connected with client certificate Subject %s\n", c.Subject)
			}
			return nil
		},
	}

	var server *http.Server
	server = &http.Server{
		Addr:      ":18081",
		Handler:   router,
		TLSConfig: tlsConfig, // start the server
	}
	http2.ConfigureServer(server, &http2.Server{})
	fmt.Println("Starting Test TLS Server.. on port :18081")
	server.ListenAndServeTLS("", "")

	return 0
}
