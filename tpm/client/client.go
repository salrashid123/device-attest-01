package main

import (
	"bytes"
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net"
	"net/http"
	"path/filepath"
	"slices"
	"time"

	"flag"
	"fmt"
	"os"

	"github.com/fxamacker/cbor/v2"
	"github.com/golang/glog"
	"github.com/google/go-attestation/attest"
	"github.com/google/go-tpm/tpmutil"
	"github.com/gorilla/mux"
	"github.com/salrashid123/go_tpm_registrar/verifier"
	"github.com/smallstep/certinfo"
	"golang.org/x/crypto/acme"
	"golang.org/x/net/http2"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/peer"
)

const (
	acmeDirectory = "https://ca.domain.com:8443/acme/acme-da/directory"
)

var (
	address        = flag.String("host", "localhost:50051", "host:port of gRPC server")
	grpcServerName = flag.String("grpcservername", "attestor.domain.com", "SNI for grpc server")
	tlsRootCert    = flag.String("tlsRootCert", "certs/tls-root-ca.crt", "tls Root Certificate")
	tpmPath        = flag.String("tpm-path", "127.0.0.1:2321", "Path to the TPM device (character device or a Unix socket).")

	attestationRootCA = flag.String("attestationRootCA", "certs/attestation-root-ca.crt", "tls Certificate")
	stepCACertPath    = flag.String("stepCACertPath", "/home/srashid/.step/certs/root_ca.crt", "tls Certificate")
	eventLogPath      = flag.String("eventLogPath", "binary_bios_measurements", "Path to the eventlog")
	issuedCertFile    = flag.String("issuedCertFile", "certs/cert.pem", "file to save the mtls cert")
	tpmKeyFile        = flag.String("tpmKeyFile", "certs/tpmkey.json", "file to save the go-attestaton tpm formatted key")

	tlsTestServerCA   = flag.String("tlsTestServerCA", "certs/tls-root-ca.crt", "tls Root Certificate")
	testTLSServerCert = flag.String("testTLSServerCert", "certs/server.crt", "tls test server Certificate")
	testTLSServerKey  = flag.String("testTLSServerKey", "certs/server.key", "tls test server key")

	oidExtensionSubjectAltName = asn1.ObjectIdentifier{2, 5, 29, 17}
	// https://trustedcomputinggroup.org/wp-content/uploads/TCG-OID-Registry-Version-1.00_pub-1.pdf
	oidPermanentIdentifier = asn1.ObjectIdentifier{1, 3, 6, 1, 5, 5, 7, 8, 3}
	oidHardwareModuleName  = asn1.ObjectIdentifier{1, 3, 6, 1, 5, 5, 7, 8, 4}
	oidTPMHardwareType     = asn1.ObjectIdentifier{2, 23, 133, 1, 2}
)

var TPMDEVICES = []string{"/dev/tpm0", "/dev/tpmrm0"}

func openTPM(path string) (io.ReadWriteCloser, error) {
	if slices.Contains(TPMDEVICES, path) {
		return tpmutil.OpenTPM(path)
	} else {
		return net.Dial("tcp", path)
	}
}

const (
	ekmLabel = "EXPORTER-my_label"
)

func main() {
	os.Exit(run()) // since defer func() needs to get called first
}

func run() int {
	flag.Parse()

	flag.Set("logtostderr", "true")
	flag.Set("stderrthreshold", "INFO")
	flag.Parse()

	if *address == "" {
		fmt.Fprintln(os.Stderr, "missing -address flag (localhost:50051)")
		flag.Usage()
		return 0
	}

	grpcRootCAs := x509.NewCertPool()
	ca_pem, err := os.ReadFile(*tlsRootCert)
	if err != nil {
		glog.Errorf("failed to load root CA certificates  error=%v", err)
		return 1
	}
	if !grpcRootCAs.AppendCertsFromPEM(ca_pem) {
		glog.Errorf("no root CA certs parsed from file ")
		return 1
	}
	tlsCfg := tls.Config{
		RootCAs:    grpcRootCAs,
		ServerName: *grpcServerName,
	}

	ce := credentials.NewTLS(&tlsCfg)
	ctx := context.Background()

	conn, err := grpc.NewClient(*address, grpc.WithTransportCredentials(ce))
	if err != nil {
		glog.Errorf("did not connect: %v", err)
		return 1
	}
	defer conn.Close()

	glog.V(5).Infof("=============== HealthCheck ===============")

	pr := new(peer.Peer)

	hctx, cancel := context.WithTimeout(ctx, 1*time.Second)
	defer cancel()
	resp, err := healthpb.NewHealthClient(conn).Check(hctx, &healthpb.HealthCheckRequest{Service: verifier.Verifier_ServiceDesc.ServiceName}, grpc.Peer(pr))
	if err != nil {
		glog.Errorf("HealthCheck failed %+v", err)
		return 1
	}

	if resp.GetStatus() != healthpb.HealthCheckResponse_SERVING {
		glog.Errorf("service not in serving state: ", resp.GetStatus().String())
		return 1
	}
	glog.V(5).Infof("RPC HealthChekStatus: %v\n", resp.GetStatus())

	switch info := pr.AuthInfo.(type) {
	case credentials.TLSInfo:
		authType := info.AuthType()
		sn := info.State.ServerName
		glog.V(60).Infof("AuthType, ServerName %s, %s\n", authType, sn)
	default:
		glog.Errorf("Unknown AuthInfo type")
		return 1
	}

	// first get the ek so we can stuff it into the platform cert

	var config *attest.OpenConfig
	if !slices.Contains(TPMDEVICES, *tpmPath) {
		glog.Info("Opening swtpm socket")
		rwc, err := openTPM(*tpmPath)
		if err != nil {
			glog.Errorf("can't open TPM %q: %v", *tpmPath, err)
			return 1
		}
		defer func() {
			rwc.Close()
		}()

		//rwr := transport.FromReadWriter(rwc)
		config = &attest.OpenConfig{
			CommandChannel: &linuxCmdChannel{rwc},
		}
	}

	tpmh, err := attest.OpenTPM(config)
	if err != nil {
		glog.Errorf("error opening TPM %v", err)
		return 1
	}
	defer tpmh.Close()

	r, err := tpmh.Info()
	if err != nil {
		glog.Errorf("error getting TPMInfo %v", err)
		return 1
	}

	//https://github.com/google/go-attestation/blob/master/attest/tpm.go#L232C1-L233C44

	// $ tpm2_getcap  properties-fixed
	// 	TPM2_PT_FIRMWARE_VERSION_1:
	//   raw: 0x20240125     <<< u16 bigendian: 8228
	// TPM2_PT_FIRMWARE_VERSION_2:
	//   raw: 0x120000

	// Manufacturer: IBM
	// VendorInfo: SW   TPM
	// FirmwareVersionMajor: 8228
	// FirmwareVersionMinor: 293

	glog.V(10).Infof("Manufacturer: %s\n", r.Manufacturer)
	glog.V(10).Infof("VendorInfo: %s\n", r.VendorInfo)
	glog.V(10).Infof("FirmwareVersionMajor: %d\n", r.FirmwareVersionMajor)
	glog.V(10).Infof("FirmwareVersionMinor: %d\n", r.FirmwareVersionMinor)

	eks, err := tpmh.EKs()
	if err != nil {
		glog.Errorf("error getting EK %v", err)
		return 1
	}

	for _, e := range eks {
		if e.Certificate != nil {
			glog.Infof("EKCert Issuer: %s", e.Certificate.Issuer)
		}
	}

	if len(eks) == 0 {
		glog.Error("error no EK found")
		return 1
	}

	// use the  ek at 0 for now...
	ek := &eks[0]

	if ek.Public == nil {
		glog.Error("error no Public not found")
		return 1
	}

	glog.V(10).Infof("EKCert SerialNumber: %d\n", ek.Certificate.SerialNumber)

	c := verifier.NewVerifierClient(conn)

	glog.V(5).Infof("=============== OfferEK ===============")

	_, err = c.OfferEK(ctx, &verifier.OfferEKRequest{
		EkCert: ek.Certificate.Raw,
	})
	if err != nil {
		glog.Errorf("error sending ekcert: %v", err)
		return 1
	}
	glog.V(5).Infof("Verified EK Cert\n")

	glog.V(5).Infof("=============== OfferAK ===============")
	// generate the attestation key
	// TODO: see how to get the GCE signed attestation key:
	// https://github.com/salrashid123/gcp-vtpm-ek-ak
	akConfig := &attest.AKConfig{
		Parent: &attest.ParentKeyConfig{
			Algorithm: attest.RSA,
			Handle:    0x81000001, // SRK, pg 29 https://trustedcomputinggroup.org/wp-content/uploads/TCG-TPM-v2.0-Provisioning-Guidance-Published-v1r1.pdf
		},
	}
	//akConfig := &attest.AKConfig{}
	ak, err := tpmh.NewAK(akConfig)
	if err != nil {
		glog.Errorf("ERROR:  could not get AK %v", err)
		return 1
	}

	attestParams := ak.AttestationParameters()
	attestParametersBytes := new(bytes.Buffer)
	err = json.NewEncoder(attestParametersBytes).Encode(attestParams)
	if err != nil {
		glog.Errorf("ERROR:  encode attestation parameters AK %v", err)
		return 1
	}

	glog.V(5).Infof("Creating AK CSR")

	// construct a CSR to send to the attestation_server
	// TBH, this is unnecessary because the server will inject the specifications unilaterally w/o regard for any of this
	var akcsrtemplate = x509.CertificateRequest{
		Subject: pkix.Name{
			CommonName: "attestor.domain.com",
		},
		DNSNames:           []string{"attestor.domain.com"},
		SignatureAlgorithm: x509.SHA256WithRSA,
	}

	aks, err := NewTPMCrypto(&TPM{
		TPM: tpmh,
		AK:  ak,
	})
	if err != nil {
		glog.Errorf("Failed to create CSR: %s", err)
		return 1
	}

	akcsrBytes, err := x509.CreateCertificateRequest(rand.Reader, &akcsrtemplate, aks)
	if err != nil {
		glog.Errorf("Failed to create CSR: %s", err)
		return 1
	}
	akpemcsr := pem.EncodeToMemory(
		&pem.Block{
			Type:  "CERTIFICATE REQUEST",
			Bytes: akcsrBytes,
		},
	)
	glog.V(5).Infof("AK CSR \n%s\n", string(akpemcsr))
	defer ak.Close(tpmh)

	_, err = c.OfferAK(ctx, &verifier.OfferAKRequest{
		AttestationParameters: attestParametersBytes.Bytes(),
		AkCsr:                 akcsrBytes,
	})
	if err != nil {
		glog.Errorf("error sending attestation parameters: %v", err)
		return 1
	}
	glog.V(5).Infof("Verified AK \n")

	glog.V(5).Infof("=============== GetMakeCredential ===============")

	mk, err := c.GetMakeCredential(ctx, &verifier.GetMakeCredentialRequest{})
	if err != nil {
		glog.Errorf("error sending getMakeCredentials: %v", err)
		return 1
	}
	glog.V(60).Infof("EncryptedCredentials %s", base64.StdEncoding.EncodeToString(mk.EncryptedCredentials))

	// akf, err := tpm.LoadAK(akbytes)
	// if err != nil {
	// 	glog.Errorf("ERROR:  error loading ak AK %v", err)
	// 	return 1
	// }
	// defer ak.Close(tpm)

	var encryptedCredentials attest.EncryptedCredential
	err = json.Unmarshal(mk.EncryptedCredentials, &encryptedCredentials)
	if err != nil {
		glog.Errorf("ERROR:  error decoding encryptedCredentials %v", err)
		return 1
	}

	secret, err := ak.ActivateCredential(tpmh, encryptedCredentials)
	//secret, err := ak.ActivateCredentialWithEK(tpm, encryptedCredentials, *ek)
	if err != nil {
		glog.Errorf("ERROR:  error activating Credential  AK %v", err)
		return 1
	}
	glog.V(5).Infof("EncryptedCredentials Secret %s", base64.StdEncoding.EncodeToString(secret))

	glog.V(5).Infof("=============== SetActivateCredential ===============")

	_, err = c.SetActivateCredential(ctx, &verifier.SetActivateCredentialRequest{
		Secret: secret,
	})
	if err != nil {
		glog.Errorf("error sending getMakeCredentials: %v", err)
		return 1
	}
	glog.V(5).Infof("SetActivateCredential complete \n")

	glog.V(5).Infof("=============== OfferQuote ===============")

	oq, err := c.OfferQuote(ctx, &verifier.OfferQuoteRequest{})
	if err != nil {
		glog.Errorf("error sending OfferQuote: %v", err)
		return 1
	}
	glog.V(5).Infof("OfferQuote complete \n")

	glog.V(5).Infof("=============== SetQuote ===============")

	evtLog, err := os.ReadFile(*eventLogPath)
	if err != nil {
		glog.Errorf("     Error reading eventLog %v", err)
		return 1
	}

	platformAttestation, err := tpmh.AttestPlatform(ak, oq.Nonce, &attest.PlatformAttestConfig{
		EventLog: evtLog,
	})
	if err != nil {
		glog.Errorf("ERROR: creating Attestation %v", err)
		return 1
	}

	platformAttestationBytes := new(bytes.Buffer)
	err = json.NewEncoder(platformAttestationBytes).Encode(platformAttestation)
	if err != nil {
		glog.Errorf("ERROR: encoding platformAttestationBytes %v", err)
		return 1
	}

	sq, err := c.SetQuote(ctx, &verifier.SetQuoteRequest{
		PlatformAttestation: platformAttestationBytes.Bytes(),
	})
	if err != nil {
		glog.Errorf("error sending SetQuote: %v", err)
		return 1
	}

	issuedakcrtPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: sq.AkCertificate})

	akcert, err := x509.ParseCertificate(sq.AkCertificate)
	if err != nil {
		glog.Errorf("Failed to create certificate: %v", err)
		return 1
	}

	akcertPrintable, err := certinfo.CertificateText(akcert)
	if err != nil {
		glog.Errorf("Failed to format certificate: %v", err)
		return 1
	}
	glog.V(5).Infof("Issued AK Certificate: \n%s\n%s\n", string(issuedakcrtPEM), akcertPrintable)

	glog.V(5).Infof("=============== Create new Key ===============")

	extractedData, err := extractCustomOtherName(akcert, oidPermanentIdentifier)
	if err != nil {
		glog.Errorf("Error: %v", err)
		return 1
	}
	var pi permanentIdentifier

	_, err = asn1.Unmarshal(extractedData, &pi)
	if err != nil {
		glog.Errorf("Failed to parse ASN.1 data: %v", err)
		return 1
	}
	devserialNumber := pi.IdentifierValue
	glog.V(5).Infof("Extracted Permanent Identfier: %s\n", devserialNumber)

	extractedDatahs, err := extractCustomOtherName(akcert, oidHardwareModuleName)
	if err != nil {
		glog.Errorf("Error: %v", err)
		return 1
	}
	var hs hardwareModuleName
	_, err = asn1.Unmarshal(extractedDatahs, &hs)
	if err != nil {
		glog.Errorf("Failed to parse ASN.1 data: %v", err)
		return 1
	}

	glog.V(5).Infof("Extracted HardwareSerialNumber: %s\n", hs.SerialNumber)

	glog.V(5).Infof("     Starting ACME Key generation")
	stepCACert := *stepCACertPath
	if *stepCACertPath != "" {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			glog.Errorf("Failed to detect home directory: %v", err)
			return 1
		}
		stepCACert = filepath.Join(homeDir, ".step/certs/root_ca.crt")
	}

	caCert, err := os.ReadFile(stepCACert)
	if err != nil {
		glog.Errorf("Failed to read root CA cert: %v", err)
		return 1
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
		glog.Errorf("Failed to generate account key: %v", err)
		return 1
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
		glog.Errorf("Failed to register account: %v", err)
		return 1
	}
	glog.V(5).Infof("Successfully registered ACME account.")

	order, err := client.AuthorizeOrder(ctx, []acme.AuthzID{
		{Type: "permanent-identifier", Value: devserialNumber},
	})
	if err != nil {
		glog.Errorf("Failed to authorize order: %v", err)
		return 1
	}
	glog.V(5).Infof("Order created. URI: %s\n", order.URI)

	var newPublicKey crypto.PublicKey
	var nk *attest.Key
	// 5. Handle Challenges (Iterate through required authorizations)
	for _, authzURL := range order.AuthzURLs {
		authz, err := client.GetAuthorization(ctx, authzURL)
		if err != nil {
			glog.Errorf("Failed to get authz: %v", err)
			return 1
		}

		var challenge *acme.Challenge
		for _, chal := range authz.Challenges {
			if chal.Type == "device-attest-01" {
				challenge = chal
				break
			}
		}

		if challenge == nil {
			glog.Errorf("No suitable challenge found for %s", authz.Identifier.Value)
			return 1
		}

		glog.V(5).Infof("Fulfill challenge token: %s\n", challenge.Token)

		glog.V(5).Infof("=============== Create New Key and set challengToken ===============")

		keyauthString, err := keyAuth(client.Key.Public(), challenge.Token)
		if err != nil {
			glog.Errorf("Could not create keyauth %v", err)
			return 1
		}

		h := sha256.New()
		h.Write([]byte(keyauthString))
		glog.V(5).Infof("KEYAUTH: %s\n", keyauthString)

		glog.V(5).Infof("Create a TPM based key\n")

		// now create the TLS EC key on the TPM
		kConfig := &attest.KeyConfig{
			Algorithm: attest.ECDSA,
			Size:      256,
			// Parent: &attest.ParentKeyConfig{
			// 	Algorithm: attest.RSA,
			// 	Handle:    0x81000001, // default RSA SRK
			// },
			QualifyingData: h.Sum(nil), // encode some client-side data into the attestatio that the server can verify
		}
		nk, err = tpmh.NewKey(ak, kConfig)
		if err != nil {
			glog.V(5).Infof("ERROR:  error creating key  %v", err)
			return 1
		}
		defer nk.Close()
		err = ak.Close(tpmh)
		if err != nil {
			glog.Errorf("ERROR:  error closing ak  %v", err)
			return 1
		}

		var ok bool
		newPublicKey, ok = nk.Public().(*ecdsa.PublicKey)
		if !ok {
			glog.Errorf("Could not assert the public key to ec public key")
			return 1
		}

		issuedKeyderBytes, err := x509.MarshalPKIXPublicKey(newPublicKey)
		if err != nil {
			glog.Errorf("Could not MarshalPKIXPublicKey ec public key")
			return 1
		}
		pubkeyPem := pem.EncodeToMemory(
			&pem.Block{
				Type:  "PUBLIC KEY",
				Bytes: issuedKeyderBytes,
			},
		)

		glog.V(5).Infof("Generated ECC Public \n%s", string(pubkeyPem))

		// *********************************************************************************************************************************************

		clientCAcrtBytes, err := os.ReadFile(*attestationRootCA)
		if err != nil {
			glog.Errorf("could not load attestationCA certificate: %v", err)
			return 1
		}

		block, _ := pem.Decode(clientCAcrtBytes)
		if block == nil {
			glog.Errorf("error decoding attestation ca certificate file %v", err)
			return 1
		}
		ccacrt, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			glog.Errorf("error parsing attestation ca certificate  %v", err)
			return 1
		}

		type AttestationObject struct {
			Fmt      string         `cbor:"fmt"`
			AttStmt  map[string]any `cbor:"attStmt"`
			AuthData []byte         `cbor:"authData"`
		}

		myAttestation := AttestationObject{
			Fmt: "tpm",
			AttStmt: map[string]any{
				"ver":      "2.0",                                          // String literal
				"alg":      -7,                                             // COSE algorithm identifier ECDSA
				"sig":      nk.CertificationParameters().CreateSignature,   // []byte signature
				"x5c":      [][]byte{akcert.Raw, ccacrt.Raw},               // Attestation Identity Key certificate chain
				"certInfo": nk.CertificationParameters().CreateAttestation, // []byte TPMS_ATTEST structure
				"pubArea":  nk.CertificationParameters().Public,            // []byte TPM2B_PUBLIC structure
			},
			//AuthData: rawAuthData,
		}

		encMode, err := cbor.CoreDetEncOptions().EncMode()
		if err != nil {
			glog.Errorf("Failed to establish CBOR encoding configuration: %v", err)
			return 1
		}

		// 4. Marshal into the final raw binary representation
		cborBytes, err := encMode.Marshal(myAttestation)
		if err != nil {
			glog.Errorf("Failed to marshal CBOR payload: %v", err)
			return 1
		}

		// Output raw hexadecimal representation of the attestationObject
		cborURL := base64.RawURLEncoding.EncodeToString(cborBytes)

		time.Sleep(3 * time.Second)
		glog.V(5).Infoln("started server accepting challenge")

		raw := json.RawMessage(fmt.Sprintf("{\"attObj\": \"%s\"}", cborURL))

		challenge.Payload = raw
		_, err = client.Accept(ctx, challenge)
		if err != nil {
			glog.Errorf("Failed to accept challenge: %v", err)
			return 1
		}
	}

	// 6. Poll the order status until it transitions to 'StatusReady'
	glog.Infoln("Waiting for order readiness validation...")
	readyOrder, err := client.WaitOrder(ctx, order.URI)
	if err != nil {
		glog.Errorf("Order validation failed or timed out: %v", err)
		return 1
	}

	pid, err := marshalOtherName(oidPermanentIdentifier, permanentIdentifier{
		IdentifierValue: devserialNumber,
	})
	if err != nil {
		glog.Errorf("Failed to create permanentIdentifier: %s", err)
		return 1
	}

	ccd, err := mustMarshal([]asn1.RawValue{pid})
	if err != nil {
		glog.Errorf("Failed to mustMarshal otherName: %s", err)
		return 1
	}

	extSubjectAltNamed := pkix.Extension{
		Id:       oidExtensionSubjectAltName,
		Critical: false,
		Value:    ccd,
	}

	csrTemplate := x509.CertificateRequest{
		Subject: pkix.Name{
			CommonName: devserialNumber,
		},
		PublicKey:          newPublicKey,
		ExtraExtensions:    []pkix.Extension{extSubjectAltNamed},
		SignatureAlgorithm: x509.ECDSAWithSHA256,
	}

	priv1, err := nk.Private(nk.Public())
	if err != nil {
		glog.Errorf("Failed to mustMarshal otherName: %s", err)
		return 1
	}
	signer1, ok := priv1.(crypto.Signer)
	if !ok {
		glog.Errorf("Failed to mustMarshal otherName: %s", err)
		return 1
	}

	csrDER, err := x509.CreateCertificateRequest(rand.Reader, &csrTemplate, signer1)
	if err != nil {
		glog.Errorf("Failed to create CSR: %v", err)
		return 1
	}

	// 8. Finalize the order and fetch the signed certificate chain
	glog.Infoln("Finalizing order with CSR...")
	derChain, _, err := client.CreateOrderCert(ctx, readyOrder.FinalizeURL, csrDER, true)
	if err != nil {
		glog.Errorf("Failed to finalize certificate order: %v", err)
		return 1
	}

	// 9. Save issued certificates to disk
	certOut, err := os.Create(*issuedCertFile)
	if err != nil {
		glog.Errorf("Failed to write certificate:  %v", err)
		return 1
	}
	defer certOut.Close()

	tpmkeybytes, err := nk.Marshal()
	if err != nil {
		glog.Errorf("Failed to get tpm key bytes:  %v", err)
		return 1
	}
	err = os.WriteFile(*tpmKeyFile, tpmkeybytes, 0644)
	if err != nil {
		glog.Errorf("Failed to write private key:  %v", err)
		return 1
	}

	caCertblock, _ := pem.Decode(caCert)
	if caCertblock == nil {
		glog.Errorf("Failed to parse PEM block")
		return 1
	}

	if caCertblock.Type != "CERTIFICATE" {
		glog.Errorf("Expected CERTIFICATE block, got %s", caCertblock.Type)
		return 1
	}

	acmeRootCert, err := x509.ParseCertificate(caCertblock.Bytes)
	if err != nil {
		glog.Errorf("Failed to parse acmeRootCert: %v", err)
		return 1
	}

	acmeRootPrintable, err := certinfo.CertificateText(acmeRootCert)
	if err != nil {
		glog.Errorf("Failed to prettyprint acmeRootPrintable:  %v", err)
		return 1
	}

	glog.V(5).Infof("Acme Root Certificate: \n%s\n", acmeRootPrintable)

	// *************************************************************************
	// test the client cert by launching a HTTP server locally which expects the client cert we just got
	//  then constuct an http client which loades the TPM based client cert and makes a connection to the server
	// *************************************************************************
	glog.V(5).Infoln("Using mTLS certificate to make mTLS call")

	// this cert pool is for the client to trust the server's cert (i.,e the CA that signed the http server)
	tlsTestCertPool := x509.NewCertPool()
	tlscapem, err := os.ReadFile(*tlsTestServerCA)
	if err != nil {
		glog.Errorf("failed to load test server client cert trust CA error=%v", err)
		return 1
	}
	if !tlsTestCertPool.AppendCertsFromPEM(tlscapem) {
		glog.Errorf("error parsing tlsttestcertpool")
		return 1
	}

	// issuedcert is the TPM  bound key's issued cert by acme
	var issuedcert *x509.Certificate

	// this certpools will list the CA's that server will expect the client cert'sissuer
	// this will be populated by the intermdidate ACME CA

	clientCertPool := x509.NewCertPool()
	for _, b := range derChain {
		pem.Encode(certOut, &pem.Block{Type: "CERTIFICATE", Bytes: b})
		var err error
		crt, err := x509.ParseCertificate(b)
		if err != nil {
			glog.Errorf("Failed to parse certificate: %v", err)
			return 1
		}

		issuedcertPrintable, err := certinfo.CertificateText(crt)
		if err != nil {
			glog.Errorf("Failed to prettyprint certificate:  %v", err)
			return 1
		}

		glog.V(5).Infof("Certificate: \n%s\n", issuedcertPrintable)

		// there could be many in this chain but for this test, its just the intermediate and leaf...
		if crt.IsCA {
			clientCertPool.AddCert(crt)
		} else {
			issuedcert = crt
		}

	}

	// load the tls server's TLS certs
	defaultServerCerts, err := tls.LoadX509KeyPair(*testTLSServerCert, *testTLSServerKey)
	if err != nil {
		glog.Errorf("failed to load test server certificates  error=%v", err)
		return 1
	}
	go func() error {

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
					glog.V(5).Infof("Server connected with client certificate Issuer %s\n", c.Issuer)
					glog.V(5).Infof("Server connected with client certificate Subject %s\n", c.Subject)
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
		glog.V(5).Infof("Starting Test TLS Server..")
		return server.ListenAndServeTLS("", "")

	}()
	time.Sleep(3 * time.Second)

	// load a crypto.Signer() representation for the TPM based key
	clientsigner, err := nk.Private(nk.Public())
	if err != nil {
		glog.Errorf("Failed to prettyprint certificate:  %v", err)
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
				glog.V(5).Infof("client connected to server with cn %s\n", c.Subject)
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
				glog.V(5).Infof("client connected with server Issuer: %s \n", cert.Issuer)
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
		glog.Errorf("Failed connecting to test tls server:  %v", err)
		return 1
	}
	defer hresp.Body.Close()

	if hresp.TLS != nil {
		if len(hresp.TLS.PeerCertificates) > 0 {
			glog.V(5).Infof("client successfully verified server certificate.")
		}
	}

	body, err := io.ReadAll(hresp.Body)
	if err != nil {
		glog.Errorf("Failed reading the server response  %v", err)
		return 1
	}

	glog.V(5).Infof("server Response: %s\n", body)

	return 0
}

// compatible channel for use with the TPM provided by go-attestation
type linuxCmdChannel struct {
	io.ReadWriteCloser
}

// MeasurementLog implements CommandChannelTPM20.
func (cc *linuxCmdChannel) MeasurementLog() ([]byte, error) {
	return os.ReadFile(*eventLogPath)
}

// a crypto.Signer for go-attestation AK objects
type TPM struct {
	_   crypto.Signer
	_   crypto.MessageSigner
	TPM *attest.TPM
	AK  *attest.AK
}

func NewTPMCrypto(conf *TPM) (TPM, error) {

	if conf.TPM == nil {
		return TPM{}, fmt.Errorf("AK TPM cannot be null")
	}

	if conf.AK == nil {
		return TPM{}, fmt.Errorf("AK cannot be null")
	}

	return *conf, nil
}

func (t TPM) Public() crypto.PublicKey {
	return t.AK.Public()
}

func (t TPM) Sign(rr io.Reader, digest []byte, opts crypto.SignerOpts) ([]byte, error) {
	//return t.AK.SignMsg(t.TPM, digest, opts)
	return nil, errors.New("AK cert is restricted:  cannot Sign() directly; use SignMessage()")
}

func (t TPM) SignMessage(rand io.Reader, msg []byte, opts crypto.SignerOpts) (signature []byte, err error) {
	return t.AK.SignMsg(t.TPM, msg, opts)
}

// utility functons for asn1
func mustMarshal(val any) ([]byte, error) {
	data, err := asn1.Marshal(val)
	if err != nil {
		return nil, err
	}
	return data, nil
}

func marshalOtherName(oid asn1.ObjectIdentifier, value interface{}) (asn1.RawValue, error) {
	valueBytes, err := asn1.MarshalWithParams(value, "explicit,tag:0")
	if err != nil {
		return asn1.RawValue{}, err
	}
	b, err := asn1.MarshalWithParams(otherName{
		TypeID: oid,
		Value:  asn1.RawValue{FullBytes: valueBytes},
	}, "tag:0")
	if err != nil {
		return asn1.RawValue{}, err
	}
	return asn1.RawValue{FullBytes: b}, nil
}

type otherName struct {
	TypeID asn1.ObjectIdentifier
	Value  asn1.RawValue
}

type permanentIdentifier struct {
	IdentifierValue string                `asn1:"utf8,optional"`
	Assigner        asn1.ObjectIdentifier `asn1:"optional"`
}

type hardwareModuleName struct {
	Type         asn1.ObjectIdentifier
	SerialNumber []byte `asn1:"tag:4"`
}

// keyAuth generates a key authorization string for a given token.
func keyAuth(pub crypto.PublicKey, token string) (string, error) {
	th, err := JWKThumbprint(pub)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s.%s", token, th), nil
}

// THe following functions are from smallstep-ca codebase:

// JWKThumbprint creates a JWK thumbprint out of pub
// as specified in https://tools.ietf.org/html/rfc7638.
func JWKThumbprint(pub crypto.PublicKey) (string, error) {
	jwk, err := jwkEncode(pub)
	if err != nil {
		return "", err
	}
	b := sha256.Sum256([]byte(jwk))
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}

// jwkEncode encodes public part of an RSA or ECDSA key into a JWK.
// The result is also suitable for creating a JWK thumbprint.
// https://tools.ietf.org/html/rfc7517
func jwkEncode(pub crypto.PublicKey) (string, error) {
	switch pub := pub.(type) {
	case *rsa.PublicKey:
		// https://tools.ietf.org/html/rfc7518#section-6.3.1
		n := pub.N
		e := big.NewInt(int64(pub.E))
		// Field order is important.
		// See https://tools.ietf.org/html/rfc7638#section-3.3 for details.
		return fmt.Sprintf(`{"e":"%s","kty":"RSA","n":"%s"}`,
			base64.RawURLEncoding.EncodeToString(e.Bytes()),
			base64.RawURLEncoding.EncodeToString(n.Bytes()),
		), nil
	case *ecdsa.PublicKey:
		// https://tools.ietf.org/html/rfc7518#section-6.2.1
		p := pub.Curve.Params()
		n := p.BitSize / 8
		if p.BitSize%8 != 0 {
			n++
		}
		x := pub.X.Bytes()
		if n > len(x) {
			x = append(make([]byte, n-len(x)), x...)
		}
		y := pub.Y.Bytes()
		if n > len(y) {
			y = append(make([]byte, n-len(y)), y...)
		}
		// Field order is important.
		// See https://tools.ietf.org/html/rfc7638#section-3.3 for details.
		return fmt.Sprintf(`{"crv":"%s","kty":"EC","x":"%s","y":"%s"}`,
			p.Name,
			base64.RawURLEncoding.EncodeToString(x),
			base64.RawURLEncoding.EncodeToString(y),
		), nil
	}
	return "", errors.New("acme: unknown key type; only RSA and ECDSA are supported")
}

// this bit i got from gemini...
func extractCustomOtherName(cert *x509.Certificate, targetOID asn1.ObjectIdentifier) ([]byte, error) {
	for _, ext := range cert.Extensions {
		if !ext.Id.Equal(oidExtensionSubjectAltName) {
			continue
		}

		// SAN extension values are wrapped inside an ASN.1 SEQUENCE
		var sequence []asn1.RawValue
		if _, err := asn1.Unmarshal(ext.Value, &sequence); err != nil {
			return nil, fmt.Errorf("failed to unmarshal SAN sequence: %w", err)
		}

		for _, rawName := range sequence {
			var genName otherName
			// Use FullBytes to maintain appropriate context-specific tagging
			if _, err := asn1.UnmarshalWithParams(rawName.FullBytes, &genName, "tag:0"); err != nil {
				// Safely ignore elements matching other choice types (DNS, IP, etc.)
				continue
			}

			// Validate if the parsed otherName matches your intended custom OID target
			if genName.TypeID.Equal(targetOID) {
				// The payload structure varies depending on how the otherName value was encoded.
				// For primitive strings (e.g., IA5String/UTF8String), use genName.OtherName.Value.Bytes
				return genName.Value.Bytes, nil
			}
		}
	}
	return nil, fmt.Errorf("target otherName OID %s not found", targetOID)
}
