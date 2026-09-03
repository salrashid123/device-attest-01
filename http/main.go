package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/gorilla/mux"
	"github.com/smallstep/certinfo"
	"golang.org/x/crypto/acme"
	"golang.org/x/net/http2"
)

const (
	// Replace with your smallstep ACME directory endpoint
	// https://<your-ca-hostname>/acme/<provisioner-name>/directory
	acmeDirectory = "https://ca.domain.com:8443/acme/myacme/directory"
	domainToCert  = "server.domain.com"
	serverPort    = ":80"
)

var (
	caCertPath = flag.String("caCertPath", "/home/srashid/.step/certs/root_ca.crt", "tls Certificate")
)

func main() {

	flag.Parse()

	ctx := context.Background()

	stepCAFile := *caCertPath

	if *caCertPath == "" {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			log.Fatalf("Failed to detect home directory: %v", err)
		}

		stepCAFile = filepath.Join(homeDir, ".step/certs/root_ca.crt")
	}
	// 1. Configure custom HTTP Client to trust your step-ca Root Certificate
	caCert, err := os.ReadFile(stepCAFile)
	if err != nil {
		log.Fatalf("Failed to read root CA cert: %v", err)
	}
	caCertPool := x509.NewCertPool()
	caCertPool.AppendCertsFromPEM(caCert)

	httpClient := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				RootCAs: caCertPool,
			},
		},
	}

	// 2. Initialize the ACME key pair & client
	accountKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		log.Fatalf("Failed to generate account key: %v", err)
	}

	client := &acme.Client{
		Key:          accountKey,
		DirectoryURL: acmeDirectory,
		HTTPClient:   httpClient,
	}

	// 3. Register a new ACME account
	account := &acme.Account{Contact: []string{"mailto:admin@example.local"}}
	_, err = client.Register(ctx, account, acme.AcceptTOS)
	if err != nil {
		log.Fatalf("Failed to register account: %v", err)
	}
	log.Println("Successfully registered ACME account.")

	// 4. Create a Certificate Order
	order, err := client.AuthorizeOrder(ctx, []acme.AuthzID{
		{Type: "dns", Value: domainToCert},
	})
	if err != nil {
		log.Fatalf("Failed to authorize order: %v", err)
	}
	log.Printf("Order created. URI: %s\n", order.URI)

	// 5. Handle Challenges (Iterate through required authorizations)
	for _, authzURL := range order.AuthzURLs {
		authz, err := client.GetAuthorization(ctx, authzURL)
		if err != nil {
			log.Fatalf("Failed to get authz: %v", err)
		}

		// Find your preferred challenge type (e.g., "http-01" or "dns-01")
		var challenge *acme.Challenge
		for _, chal := range authz.Challenges {
			if chal.Type == "http-01" { // Switch to "dns-01" if using DNS
				challenge = chal
				break
			}
		}

		if challenge == nil {
			log.Fatalf("No suitable challenge found for %s", authz.Identifier.Value)
		}

		// [ACTION REQUIRED]: Fulfill the challenge dynamically here.
		// For http-01, you must serve the 'token' content at:
		// http://<domain>/.well-known/acme-challenge/<token>
		log.Printf("Fulfill challenge token: %s\n", challenge.Token)

		go func() error {
			router := mux.NewRouter()
			router.Methods(http.MethodGet).Path(fmt.Sprintf("/.well-known/acme-challenge/%s", challenge.Token)).HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				log.Printf("Got Acme challenge on HTTP server endpoint %s", challenge.Token)
				responseBody, err := client.HTTP01ChallengeResponse(challenge.Token)
				if err != nil {
					log.Fatalf("Failed to generate challenge response: %v", err)
				}
				log.Printf("Acme responseBody %s", responseBody)
				w.Header().Set("Content-Type", "text/plain")
				w.WriteHeader(http.StatusOK)
				fmt.Fprint(w, responseBody)
			})
			server := &http.Server{
				Addr:    serverPort,
				Handler: router,
			}

			http2.ConfigureServer(server, &http2.Server{})
			log.Printf("Starting HTTP Server")

			lis, err := net.Listen("tcp", serverPort)
			if err != nil {
				log.Printf("        Error Listening \n%v\n", err)
				return fmt.Errorf("ERROR: listening: %v", err)
			}

			return server.Serve(lis)
		}()
		time.Sleep(3 * time.Second)
		log.Println("started server accepting challenge")
		// Notify step-ca that the challenge is ready for validation
		_, err = client.Accept(ctx, challenge)
		if err != nil {
			log.Fatalf("Failed to accept challenge: %v", err)
		}
	}

	// 6. Poll the order status until it transitions to 'StatusReady'
	log.Println("Waiting for order readiness validation...")
	readyOrder, err := client.WaitOrder(ctx, order.URI)
	if err != nil {
		log.Fatalf("Order validation failed or timed out: %v", err)
	}

	// 7. Generate a CSR (Certificate Signing Request) for the requested domain
	certKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		log.Fatalf("Failed to generate certificate private key: %v", err)
	}

	derBytes, err := x509.MarshalECPrivateKey(certKey)
	if err != nil {
		fmt.Printf("Failed to marshal key: %v\n", err)
		return
	}

	pemBlock := &pem.Block{
		Type:  "EC PRIVATE KEY",
		Bytes: derBytes,
	}

	fmt.Println("--- HTTPS Server Private Key---")
	err = pem.Encode(os.Stdout, pemBlock)
	if err != nil {
		fmt.Printf("Failed to encode PEM: %v\n", err)
	}

	csrTemplate := x509.CertificateRequest{
		Subject:  pkix.Name{CommonName: domainToCert},
		DNSNames: []string{domainToCert},
	}
	csrDER, err := x509.CreateCertificateRequest(rand.Reader, &csrTemplate, certKey)
	if err != nil {
		log.Fatalf("Failed to create CSR: %v", err)
	}

	// 8. Finalize the order and fetch the signed certificate chain
	log.Println("Finalizing order with CSR...")
	derChain, _, err := client.CreateOrderCert(ctx, readyOrder.FinalizeURL, csrDER, true)
	if err != nil {
		log.Fatalf("Failed to finalize certificate order: %v", err)
	}

	// 9. Save issued certificates to disk

	for _, b := range derChain {

		issuedcert, err := x509.ParseCertificate(b)
		if err != nil {
			log.Fatalf("Failed to create certificate: %v", err)
		}
		issuedcertPrintable, err := certinfo.CertificateText(issuedcert)
		if err != nil {
			log.Fatalf("Failed to format certificate:  %v", err)
		}
		log.Printf("Certificate: \n%s\n", issuedcertPrintable)

		// log.Println("--- HTTPS Server certificate --")

		// err = pem.Encode(os.Stdout, &pem.Block{Type: "CERTIFICATE", Bytes: b})
		// if err != nil {
		// 	log.Fatalf("Failed to decode  certificate: %v", err)
		// }
		// log.Println("Successfully downloaded and saved cert.pem!")

	}

}
