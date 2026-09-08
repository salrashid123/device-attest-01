package main

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"net/http"
	"slices"
	"time"

	"flag"
	"os"

	keyfile "github.com/foxboron/go-tpm-keyfiles"
	"github.com/google/go-tpm/tpm2/transport"
	"github.com/google/go-tpm/tpmutil"
)

var (
	tpmPath         = flag.String("tpm-path", "127.0.0.1:2321", "Path to the TPM device (character device or a Unix socket).")
	issuedCertFile  = flag.String("issuedCertFile", "../../certs/cert.pem", "file to save the mtls cert")
	tpmKeyFilePEM   = flag.String("tpmKeyFilePEM", "../../certs/tpmkey.pem", "file to save the go-tpm formatted key")
	tlsTestServerCA = flag.String("tlsTestServerCA", "../../certs/tls-root-ca.crt", "tls Root Certificate")
)

var TPMDEVICES = []string{"/dev/tpm0", "/dev/tpmrm0"}

func openTPM(path string) (io.ReadWriteCloser, error) {
	if slices.Contains(TPMDEVICES, path) {
		return tpmutil.OpenTPM(path)
	} else {
		return net.Dial("tcp", path)
	}
}

func main() {
	os.Exit(run()) // since defer func() needs to get called first
}

func run() int {
	flag.Parse()

	fmt.Println("Using mTLS certificate to make mTLS call")

	// this cert pool is for the client to trust the server's cert (i.,e the CA that signed the http server)
	tlsTestCertPool := x509.NewCertPool()
	tlscapem, err := os.ReadFile(*tlsTestServerCA)
	if err != nil {
		fmt.Printf("failed to load test server client cert trust CA error=%v", err)
		return 1
	}
	if !tlsTestCertPool.AppendCertsFromPEM(tlscapem) {
		fmt.Printf("error parsing tlsttestcertpool")
		return 1
	}

	pemData, err := os.ReadFile(*issuedCertFile)
	if err != nil {
		fmt.Printf("error reading private keyfile: %v", err)
		return 1
	}

	// 2. Decode the PEM block
	block, _ := pem.Decode(pemData)
	if block == nil || block.Type != "CERTIFICATE" {
		fmt.Printf("error reading private keyfile: %v", err)
		return 1
	}

	// 3. Parse the X.509 certificate
	issuedcert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		fmt.Printf("error parsing private keyfile: %v", err)
		return 1
	}

	c, err := os.ReadFile(*tpmKeyFilePEM)
	if err != nil {
		fmt.Printf("error reading private keyfile: %v", err)
		return 1
	}
	tpmkey, err := keyfile.Decode(c)
	if err != nil {
		fmt.Printf("failed decoding key: %v", err)
		return 1
	}

	rwc, err := openTPM(*tpmPath)
	if err != nil {
		fmt.Printf("can't open TPM %q: %v", *tpmPath, err)
		return 1
	}
	defer func() {
		rwc.Close()
	}()
	rwrc, err := getTPM(rwc)
	if err != nil {
		fmt.Printf("failed decoding key: %v", err)
		return 1
	}
	clientsigner, err := tpmkey.Signer(rwrc, nil, nil)
	if err != nil {
		fmt.Printf("failed getting singer from key %v", err)
		return 1
	}
	// configure mtls to use the ACME issued leaf cert and the TPM based signer for our key
	clientTLS := tls.Certificate{
		Certificate: [][]byte{issuedcert.Raw},
		PrivateKey:  clientsigner,
	}

	// set up the client cert tls config
	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{clientTLS}, // set the tlsCertificate struct for our client cert
		RootCAs:      tlsTestCertPool,
		MinVersion:   tls.VersionTLS13,

		VerifyPeerCertificate: func(rawCerts [][]byte, verifiedChains [][]*x509.Certificate) error {

			// print out some specifics of the server though its not important and not a client cert..
			for _, rawCert := range rawCerts {
				c, err := x509.ParseCertificate(rawCert)
				if err != nil {
					return err
				}
				fmt.Printf("client connected to server with cn %s\n", c.Subject)
			}
			return nil
		},
	}

	tr := &http.Transport{
		TLSClientConfig: tlsConfig,
		DialTLS: func(network, addr string) (net.Conn, error) {

			// extract the connection the client made to the server

			tlsConn, err := tls.Dial(network, addr, tlsConfig)
			if err != nil {
				return tlsConn, err
			}
			err = tlsConn.Handshake()
			if err != nil {
				return tlsConn, err
			}
			state := tlsConn.ConnectionState()
			certs := state.PeerCertificates
			for _, cert := range certs {
				fmt.Printf("client connected with server Issuer: %s \n", cert.Issuer)
			}
			return tlsConn, nil
		},
	}

	hclient := &http.Client{
		Transport: tr,
		Timeout:   30 * time.Second,
	}

	hresp, err := hclient.Get("https://server.domain.com:18081/")
	if err != nil {
		fmt.Printf("Failed connecting to test tls server:  %v", err)
		return 1
	}
	defer hresp.Body.Close()

	if hresp.TLS != nil {
		if len(hresp.TLS.PeerCertificates) > 0 {
			fmt.Printf("client successfully verified server certificate.")
		}
	}

	body, err := io.ReadAll(hresp.Body)
	if err != nil {
		fmt.Printf("Failed reading the server response  %v", err)
		return 1
	}

	fmt.Printf("server Response: %s\n", body)

	return 0
}

type TPM struct {
	transport io.ReadWriteCloser
}

func (t *TPM) Send(input []byte) ([]byte, error) {
	return tpmutil.RunCommandRaw(t.transport, input)
}

func getTPM(s io.ReadWriteCloser) (transport.TPMCloser, error) {
	return &TPM{

		transport: s,
	}, nil
}

func (t *TPM) Close() error {
	return t.transport.Close()
}

// compatible channel for use with the TPM provided by go-attestation
type linuxCmdChannel struct {
	io.ReadWriteCloser
}
