package main

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"hash"
	"io"
	"math/big"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"context"

	"github.com/golang/glog"
	"github.com/google/go-attestation/attest"
	"github.com/google/go-attestation/oid"
	x509ext "github.com/google/go-attestation/x509"
	"github.com/google/go-tpm-tools/proto/tpm"
	tpmtoolsserver "github.com/google/go-tpm-tools/server"
	"github.com/google/uuid"
	"github.com/salrashid123/go_tpm_registrar/verifier"
	"github.com/smallstep/certinfo"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

const ()

type db struct {
	EKCert                *x509.Certificate
	AKPub                 crypto.PublicKey
	AttestationParameters *attest.AttestationParameters
	AKCSR                 *x509.CertificateRequest
	AKCert                *x509.Certificate
	Attested              bool
	Secret                []byte
	IssuedKey             *ecdsa.PublicKey
	IssuedCert            *x509.Certificate
	Nonce                 []byte
	AttestedKey           crypto.PublicKey
	DeviceSerialNumber    string
}

var (
	grpcPort             = flag.String("grpcPort", ":50051", "port of gRPC server")
	tlsRootCert          = flag.String("rootCert", "certs/tls-root-ca.crt", "tls Root CVA Certificate")
	attestationRootCert  = flag.String("attestationRootCert", "certs/attestation-root-ca.crt", "Attestation Root CA Certificate")
	attestationRootKey   = flag.String("attestationRootKey", "certs/attestation-root-ca.key", "Attestation Root CA Key")
	tlsCert              = flag.String("tlsCert", "certs/attestor.crt", "tls Certificate")
	tlsKey               = flag.String("tlsKey", "certs/attestor.key", "tls Key")
	expectedPCRMapSHA256 = flag.String("expectedPCRMapSHA256", "0:d0c70a9310cd0b55767084333022ce53f42befbb69c059ee6c0a32766f160783", "Sealing and Quote PCRMap (as comma separated key:value).  pcr#:sha256,pcr#sha256.  Default value uses pcr0:sha256")
	ekRootCA             = flag.String("ekrootCA", "certs/ek_root.pem", "EK rootsCA")
	ekIntermediateCA     = flag.String("ekintermediateCA", "", "EK intermediate CA")
	attestationKeys      = make(map[string]db) // map which holds the EKM value for a session and the database of attestation state; todo: evict stale, unused keys

)

type server struct {
	mu sync.Mutex // lock value to guard concurrent updates to attestationKeys[] map

	// statusMap stores the serving status of the services this Server monitors.
	statusMap map[string]healthpb.HealthCheckResponse_ServingStatus
	// Embed the unimplemented server
	verifier.UnimplementedVerifierServer
}

type contextKey string

const contextEventKey contextKey = "event"

const (
	ekmLabel   = "EXPORTER-my_label"
	ekmContext = "mycontext"
)

type event struct {
	EKM    string
	PeerIP string
}

var (
	oidExtensionSubjectAltName = asn1.ObjectIdentifier{2, 5, 29, 17}
	oidPermanentIdentifier     = asn1.ObjectIdentifier{1, 3, 6, 1, 5, 5, 7, 8, 3}
	oidHardwareModuleName      = asn1.ObjectIdentifier{1, 3, 6, 1, 5, 5, 7, 8, 4}
	oidTPMHardwareType         = asn1.ObjectIdentifier{2, 23, 133, 1, 2}
)

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
	SerialNumber []byte `asn1:"tag:4"` //  //4 asn1.TagOctetString,
}

func authUnaryInterceptor(
	ctx context.Context,
	req interface{},
	info *grpc.UnaryServerInfo,
	handler grpc.UnaryHandler,
) (interface{}, error) {
	var newCtx context.Context
	var peerIPPort string
	p, ok := peer.FromContext(ctx)
	if ok {
		var err error
		peerIPPort, _, err = net.SplitHostPort(p.Addr.String())
		if err != nil {
			return nil, status.Errorf(codes.PermissionDenied, "could not get Remote IP")
		}
		glog.V(60).Infof("     Connected from peer %v", peerIPPort)
		newCtx = context.WithValue(ctx, contextKey("peerIP"), peerIPPort)
	} else {
		glog.Errorf("ERROR:  Could not extract peerInfo from TLS")
		return nil, status.Errorf(codes.PermissionDenied, "ERROR:  Could not extract peerInfo from TLS")
	}
	tlsInfo, ok := p.AuthInfo.(credentials.TLSInfo)
	if !ok {
		glog.Errorf("ERROR:  Could get remote TLS")
		return nil, status.Errorf(codes.PermissionDenied, "ERROR: could not get remote TLS")
	}
	ekm, err := tlsInfo.State.ExportKeyingMaterial(ekmLabel, []byte(ekmContext), 32)
	if err != nil {
		glog.Errorf("ERROR:  Could getting EKM %v", err)
		return nil, status.Errorf(codes.PermissionDenied, "ERROR: error getting EKM")
	}
	glog.V(60).Infof("     EKM: %s\n", hex.EncodeToString(ekm))

	event := &event{
		EKM:    hex.EncodeToString(ekm),
		PeerIP: peerIPPort,
	}

	newCtx = context.WithValue(newCtx, contextEventKey, *event)
	return handler(newCtx, req)
}

func (s *server) Check(ctx context.Context, in *healthpb.HealthCheckRequest) (*healthpb.HealthCheckResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	glog.V(2).Infof("======= HealthCheck ========")
	if in.Service == "" {
		// return overall status
		return &healthpb.HealthCheckResponse{Status: healthpb.HealthCheckResponse_SERVICE_UNKNOWN}, nil
	}

	s.statusMap[verifier.Verifier_ServiceDesc.ServiceName] = healthpb.HealthCheckResponse_SERVING

	evt := ctx.Value(contextKey("event")).(event)
	glog.V(60).Infof("     Inbound gRPC request from: %s", evt.PeerIP)
	glog.V(60).Infof("     Inbound EKM: %s", evt.EKM)
	attestationKeys[evt.EKM] = db{}

	status, ok := s.statusMap[in.Service]
	if !ok {
		return &healthpb.HealthCheckResponse{Status: healthpb.HealthCheckResponse_UNKNOWN}, grpc.Errorf(codes.NotFound, "unknown service")
	}
	return &healthpb.HealthCheckResponse{Status: status}, nil
}

func (s *server) Watch(in *healthpb.HealthCheckRequest, srv healthpb.Health_WatchServer) error {
	return status.Error(codes.Unimplemented, "Watch is not implemented")
}

func (s *server) List(ctx context.Context, in *healthpb.HealthListRequest) (*healthpb.HealthListResponse, error) {
	r := make(map[string]*healthpb.HealthCheckResponse)

	r[verifier.Verifier_ServiceDesc.ServiceName] = &healthpb.HealthCheckResponse{
		Status: healthpb.HealthCheckResponse_SERVING,
	}
	return &healthpb.HealthListResponse{Statuses: r}, nil
}

func (s *server) OfferEK(ctx context.Context, in *verifier.OfferEKRequest) (*verifier.OfferEKResponse, error) {
	glog.V(2).Infof("======= OfferEK ========")

	evt := ctx.Value(contextKey("event")).(event)
	glog.V(60).Infof("     Inbound gRPC request from: %s", evt.PeerIP)
	glog.V(60).Infof("     Inbound EKM: %s", evt.EKM)

	if _, ok := attestationKeys[evt.EKM]; !ok {
		glog.Errorf("Error cannot process OfferEK before calling HealthCheck [%s]", evt.EKM)
		return &verifier.OfferEKResponse{}, status.Errorf(codes.Internal, "Error cannot process OfferPlatformCert before calling HealthCheck")
	}

	ekcert, err := x509.ParseCertificate(in.EkCert)
	if err != nil {
		glog.Errorf("Error  ParseCertificate [%s] %v", evt.EKM, err)
		return &verifier.OfferEKResponse{}, status.Errorf(codes.Internal, "ERROR:   ParseCertificate:  %v", err)
	}
	ekcertPrintable, err := certinfo.CertificateText(ekcert)
	if err != nil {
		glog.Errorf("Failed to format certificate: [%s] %v", evt.EKM, err)
		return &verifier.OfferEKResponse{}, status.Errorf(codes.Internal, "Failed to format ak certificate: %s", err)
	}
	glog.V(5).Infof("EK Certificate: \n%s\n", ekcertPrintable)

	// optionally parse SAN.DirName, eg:
	// pg 24,26: https://trustedcomputinggroup.org/wp-content/uploads/TCG_IWG_Credential_Profile_EK_V2.1_R13.pdf
	// X509v3 Subject Alternative Name: critical
	//   DirName:/2.23.133.2.1=id:53544D20/2.23.133.2.2=ST33HTPHAHD8/2.23.133.2.3=id:00010102
	// 2.23.133.2.1 tcg-at-tpmManufacturer TPM Manufacturer Name for EK Credential Profile for TPM 2.0
	//     id:53544D20 = hex("STM")
	// 2.23.133.2.2 tcg-at-tpmModel TPM Model Number defined in EK Credential Profile for TPM 2.0
	// 2.23.133.2.3 tcg-at-tpmVersion TPM Version defined in EK Credential Profile for TPM 2.0

	var oidExtensionSubjectDirectoryAttributes = []int{2, 5, 29, 9}
	type tpmSpecification struct {
		Family   string
		Level    int
		Revision int
	}
	type attribute struct {
		Type   asn1.ObjectIdentifier
		Values []asn1.RawValue `asn1:"set"`
	}
	for _, ex := range ekcert.Extensions {
		if ex.Id.Equal(oidExtensionSubjectAltName) {
			s, err := x509ext.ParseSubjectAltName(ex)
			if err != nil {
				glog.Errorf("Error  failed to parse EK to unmarshal EK SAN [%s] %v", evt.EKM, err.Error())
				return &verifier.OfferEKResponse{}, status.Errorf(codes.Internal, "failed to parse EK to unmarshal EK SAN [%s] %v", evt.EKM, err.Error())
			}
			for _, na := range s.DirectoryNames {
				for _, attr := range na.Names {
					if attr.Type.Equal(oid.TPMManufacturer) {
						glog.V(20).Infof("     TPM Manufacturer %s", attr.Value)
					}
					if attr.Type.Equal(oid.TPMModel) {
						glog.V(20).Infof("     TPM Model %s", attr.Value)
					}
					if attr.Type.Equal(oid.TPMVersion) {
						// todo: parse the major/minor version properly
						glog.V(20).Infof("     TPM Version %s", attr.Value)
					}
				}
			}
		}

		if ex.Id.Equal(oidExtensionSubjectDirectoryAttributes) {

			var attrs []attribute
			_, err := asn1.Unmarshal(ex.Value, &attrs)
			if err != nil {
				glog.Errorf("Error failed to parse EK SubjectDirectoryAttributes [%s] %v", evt.EKM, err.Error())
				return &verifier.OfferEKResponse{}, status.Errorf(codes.Internal, "failed to parse EK SubjectDirectoryAttributes [%s] %v", evt.EKM, err.Error())
			}

			for _, attr := range attrs {
				if attr.Type.Equal(oid.TPMSpecification) {
					if len(attr.Values) != 1 {
						glog.Errorf("Error failed to parse EK SubjectDirectoryAttributes [%s] ", evt.EKM)
						return &verifier.OfferEKResponse{}, status.Errorf(codes.Internal, "failed to parse EK SubjectDirectoryAttributes [%s] %v", evt.EKM, errors.New("expected SET size of 1"))
					}
					value := attr.Values[0]
					var spec tpmSpecification
					rest, err := asn1.Unmarshal(value.FullBytes, &spec)
					if err != nil {
						glog.Errorf("Error failed to parse EK SubjectDirectoryAttributes [%s] %v", evt.EKM, err)
						return &verifier.OfferEKResponse{}, status.Errorf(codes.Internal, "failed to parse EK SubjectDirectoryAttributes [%s] %v", evt.EKM, err)
					}
					if len(rest) != 0 {
						glog.Errorf("failed to parse EK SubjectDirectoryAttributes [%s] %v", evt.EKM, err)
						return &verifier.OfferEKResponse{}, status.Errorf(codes.Internal, "failed to parse EK SubjectDirectoryAttributes [%s] %v", evt.EKM, err)
					}
					glog.V(20).Infof("     TPM Family %s", spec.Family)
					glog.V(20).Infof("     TPM Level %d", spec.Level)
					glog.V(20).Infof("     TPM Revision %d", spec.Revision)
				}
			}
		}
	}

	// if the service is on GCP, the ekcert has some special details encoded inside it
	gceInfo, err := tpmtoolsserver.GetGCEInstanceInfo(ekcert)
	if err == nil && gceInfo != nil {
		glog.V(10).Infof("     EKCert  GCE InstanceID %d", gceInfo.InstanceId)
		glog.V(10).Infof("     EKCert  GCE InstanceName %s", gceInfo.InstanceName)
		glog.V(10).Infof("     EKCert  GCE ProjectId %s", gceInfo.ProjectId)
	}

	ekcrtPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: in.EkCert})
	glog.V(2).Infof("        EKCertificate ========\n%s\n", ekcrtPEM)

	spubKey := ekcert.PublicKey.(*rsa.PublicKey)

	skBytes, err := x509.MarshalPKIXPublicKey(spubKey)
	if err != nil {
		glog.Errorf("failed to parse EK SubjectDirectoryAttributes [%s] %v", evt.EKM, err)
		return &verifier.OfferEKResponse{}, status.Errorf(codes.Internal, "failed to parse EK SubjectDirectoryAttributes  [%s] %v", evt.EKM, err)
	}
	ekPubPEM := pem.EncodeToMemory(
		&pem.Block{
			Type:  "PUBLIC KEY",
			Bytes: skBytes,
		},
	)

	glog.V(10).Infof("     EKCert  Issuer %v", ekcert.Issuer)
	glog.V(10).Infof("     EKCert  IssuingCertificateURL %v", fmt.Sprint(ekcert.IssuingCertificateURL))
	glog.V(10).Infof("     EKCert  SerialNumber %v", fmt.Sprint(ekcert.SerialNumber))

	glog.V(40).Infof("    EkCert Public Key \n%s\n", ekPubPEM)

	// now try to verify the EKCert is legit using the CA's you expect woud've signed it
	glog.V(10).Info("    Verifying EKCert")
	ekRootPEM, err := os.ReadFile(*ekRootCA)
	if err != nil {
		glog.Errorf("failed to reading roots: [%s] %v", evt.EKM, err)
		return &verifier.OfferEKResponse{}, status.Errorf(codes.Internal, "failed to reading roots: %v", err.Error())
	}

	ekRoots := x509.NewCertPool()
	ok := ekRoots.AppendCertsFromPEM([]byte(ekRootPEM))
	if !ok {
		glog.Errorf("ffailed append to roots [%s]", evt.EKM)
		return &verifier.OfferEKResponse{}, status.Errorf(codes.Internal, "failed append to roots ")
	}

	var exts []asn1.ObjectIdentifier
	for _, ext := range ekcert.UnhandledCriticalExtensions {
		if ext.Equal(oidExtensionSubjectAltName) {
			continue
		}
		exts = append(exts, ext)
	}
	ekcert.UnhandledCriticalExtensions = exts

	//oid 2.23.133.8.1 tcg-kp-EKCertificate Identifies the certificate as an Endorsement Credential.
	// try to see if the ekcert includes the recommended oid as the extension value
	var tcgkpEKCertificate asn1.ObjectIdentifier = []int{2, 23, 133, 8, 1}
	for _, ku := range ekcert.UnknownExtKeyUsage {
		if ku.Equal(tcgkpEKCertificate) {
			glog.V(10).Infof("     EKCert Includes tcg-kp-EKCertificate ExtendedKeyUsage %s", ku.String())
		}
	}

	intermediates := x509.NewCertPool()
	if *ekIntermediateCA != "" {
		intermediatePEM, err := os.ReadFile(*ekIntermediateCA)
		if err != nil {
			glog.Errorf("failed to read intermediate CA: [%s] %v", evt.EKM, err.Error())
			return &verifier.OfferEKResponse{}, status.Errorf(codes.Internal, "failed to read intermediate CA: [%s] %v", evt.EKM, err.Error())
		}

		ok = intermediates.AppendCertsFromPEM([]byte(intermediatePEM))
		if !ok {
			glog.Errorf("failed to update intermediate CA: [%s] ", evt.EKM)
			return &verifier.OfferEKResponse{}, status.Errorf(codes.Internal, "failed to append intermediates: ")
		}
	}

	opts := x509.VerifyOptions{
		Roots:         ekRoots,
		Intermediates: intermediates,
		KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsage(x509.ExtKeyUsageAny)},
	}
	if _, err := ekcert.Verify(opts); err != nil {
		glog.Errorf("failed to verify certificate:  [%s] %v", evt.EKM, err.Error())
		return &verifier.OfferEKResponse{}, status.Errorf(codes.Internal, "failed to verify certificate:  [%s] %v", evt.EKM, err.Error())
	}

	glog.V(10).Info("    EKCert Verified")

	s.mu.Lock()
	defer s.mu.Unlock()

	if val, ok := attestationKeys[evt.EKM]; ok {
		val.EKCert = ekcert
		attestationKeys[evt.EKM] = val
	} else {
		attestationKeys[evt.EKM] = db{
			EKCert: ekcert,
		}
	}

	glog.V(5).Infof("=============== end OfferEK ===============")
	return &verifier.OfferEKResponse{}, nil
}

func (s *server) OfferAK(ctx context.Context, in *verifier.OfferAKRequest) (*verifier.OfferAKResponse, error) {
	glog.V(2).Infof("======= OfferAK ========")

	evt := ctx.Value(contextKey("event")).(event)
	glog.V(60).Infof("     Inbound gRPC request from: %s", evt.PeerIP)
	glog.V(60).Infof("     Inbound EKM: %s", evt.EKM)

	s.mu.Lock()
	defer s.mu.Unlock()
	if val, ok := attestationKeys[evt.EKM]; ok {
		if val.EKCert == nil {
			glog.Errorf("Error cannot process AK before calling OfferEK [%s]", evt.EKM)
			return &verifier.OfferAKResponse{}, status.Errorf(codes.Internal, "Error cannot process AK before calling OfferEK")
		}
	} else {
		glog.Errorf("Error cannot process AK before calling OfferEK [%s]", evt.EKM)
		return &verifier.OfferAKResponse{}, status.Errorf(codes.Internal, "Error cannot process AK before calling OfferEK")
	}

	serverAttestationParameter := &attest.AttestationParameters{}
	reader := bytes.NewReader(in.AttestationParameters)
	err := json.NewDecoder(reader).Decode(serverAttestationParameter)
	if err != nil {
		glog.Errorf("Error encoding serverAttestationParamer [%s] %v", evt.EKM, err)
		return &verifier.OfferAKResponse{}, status.Errorf(codes.Internal, "Error encoding serverAttestationParameter  %v", err)
	}

	akp, err := attest.ParseAKPublic(serverAttestationParameter.Public)
	if err != nil {
		glog.Errorf("Error Parsing AK [%s] %v", evt.EKM, err)
		return &verifier.OfferAKResponse{}, status.Errorf(codes.Internal, "Error Parsing AK %v", err)
	}

	akpPub, err := x509.MarshalPKIXPublicKey(akp.Public)
	if err != nil {
		glog.Errorf("Error MarshalPKIXPublicKey ak [%s] %v", evt.EKM, err)
		return &verifier.OfferAKResponse{}, status.Errorf(codes.Internal, "Error MarshalPKIXPublicKey ak %v", err)
	}
	akPubPEM := pem.EncodeToMemory(
		&pem.Block{
			Type:  "PUBLIC KEY",
			Bytes: akpPub,
		},
	)

	glog.V(5).Infof("      ak public \n%s\n", akPubPEM)

	akcsr, err := x509.ParseCertificateRequest(in.AkCsr)
	if err != nil {
		glog.Errorf("Error ParseCertificateRequest ak [%s] %v", evt.EKM, err)
		return &verifier.OfferAKResponse{}, status.Errorf(codes.Internal, "Error ParseCertificateRequest ak %v", err)
	}
	val := attestationKeys[evt.EKM]
	val.AKPub = akp.Public
	val.AKCSR = akcsr

	val.AttestationParameters = serverAttestationParameter
	attestationKeys[evt.EKM] = val

	glog.V(5).Infof("=============== end GetAK ===============")

	return &verifier.OfferAKResponse{}, nil
}

func (s *server) GetMakeCredential(ctx context.Context, in *verifier.GetMakeCredentialRequest) (*verifier.GetMakeCredentialResponse, error) {
	glog.V(2).Infof("======= GetMakeCredential ========")

	evt := ctx.Value(contextKey("event")).(event)
	glog.V(60).Infof("     Inbound gRPC request from: %s", evt.PeerIP)
	glog.V(60).Infof("     Inbound EKM: %s", evt.EKM)

	s.mu.Lock()
	defer s.mu.Unlock()
	if val, ok := attestationKeys[evt.EKM]; ok {
		if val.EKCert == nil || val.AKPub == nil {
			glog.Errorf("Error MakeCredential requires AK and EK [%s]", evt.EKM)
			return &verifier.GetMakeCredentialResponse{}, status.Errorf(codes.Internal, "Error MakeCredential requires AK and EK")
		}
	} else {
		glog.Errorf("Error MakeCredential requires AK and EK [%s]", evt.EKM)
		return &verifier.GetMakeCredentialResponse{}, status.Errorf(codes.Internal, "Error MakeCredential requires AK and EK")
	}
	glog.V(5).Infof("=============== end GetMakeCredential ===============")

	val := attestationKeys[evt.EKM]

	params := attest.ActivationParameters{
		EK: val.EKCert.PublicKey,
		AK: *val.AttestationParameters,
	}

	secret, encryptedCredentials, err := params.Generate()
	if err != nil {
		glog.Errorf("Error generating make credential parameters [%s] %v ", evt.EKM, err)
		return &verifier.GetMakeCredentialResponse{}, status.Errorf(codes.Internal, "Error generating make credential parameters %v ", err)
	}
	glog.Infof("      Outbound Secret: %s\n", base64.StdEncoding.EncodeToString(secret))

	encryptedCredentialsBytes := new(bytes.Buffer)
	err = json.NewEncoder(encryptedCredentialsBytes).Encode(encryptedCredentials)
	if err != nil {
		glog.Errorf("Error encoding encryptedCredentials [%s] %v ", evt.EKM, err)
		return &verifier.GetMakeCredentialResponse{}, status.Errorf(codes.Internal, "Error encoding encryptedCredentials %v", err)
	}

	val.Secret = secret
	attestationKeys[evt.EKM] = val

	return &verifier.GetMakeCredentialResponse{
		EncryptedCredentials: encryptedCredentialsBytes.Bytes(),
	}, nil
}

func (s *server) SetActivateCredential(ctx context.Context, in *verifier.SetActivateCredentialRequest) (*verifier.SetActivateCredentialResponse, error) {
	glog.V(2).Infof("======= SetActivateCredential ========")

	evt := ctx.Value(contextKey("event")).(event)
	glog.V(60).Infof("     Inbound gRPC request from: %s", evt.PeerIP)
	glog.V(60).Infof("     Inbound EKM: %s", evt.EKM)

	s.mu.Lock()
	defer s.mu.Unlock()
	if val, ok := attestationKeys[evt.EKM]; ok {
		if val.EKCert == nil || val.AKPub == nil || val.AttestationParameters == nil {
			glog.Errorf("Error SetActivateCredential requires AK and EK and AttestationParameters [%s] ", evt.EKM)
			return &verifier.SetActivateCredentialResponse{}, status.Errorf(codes.Internal, "Error SetActivateCredential requires AK and EK and AttestationParameters")
		}
	} else {
		glog.Errorf("Error SetActivateCredential requires AK and EK and AttestationParameters [%s] ", evt.EKM)
		return &verifier.SetActivateCredentialResponse{}, status.Errorf(codes.Internal, "Error SetActivateCredential requires AK and EK and AttestationParameters")
	}

	val := attestationKeys[evt.EKM]

	if !bytes.Equal(val.Secret, in.Secret) {
		glog.Errorf("Error SetActivateCredential secrets not equal [%s] ", evt.EKM)
		return &verifier.SetActivateCredentialResponse{}, status.Errorf(codes.Internal, "Error SetActivateCredential secrets not equal")
	}

	vv := attestationKeys[evt.EKM]
	vv.Attested = true

	attestationKeys[evt.EKM] = vv

	glog.V(5).Infof("=============== end SetActivateCredential ===============")
	return &verifier.SetActivateCredentialResponse{}, nil
}

func (s *server) OfferQuote(ctx context.Context, in *verifier.OfferQuoteRequest) (*verifier.OfferQuoteResponse, error) {
	glog.V(2).Infof("======= OfferQuote ========")

	evt := ctx.Value(contextKey("event")).(event)
	glog.V(60).Infof("     Inbound gRPC request from: %s", evt.PeerIP)
	glog.V(60).Infof("     Inbound EKM: %s", evt.EKM)

	s.mu.Lock()
	defer s.mu.Unlock()
	if val, ok := attestationKeys[evt.EKM]; ok {
		if val.EKCert == nil || val.AKPub == nil || val.AttestationParameters == nil || !val.Attested {
			glog.Errorf("Error OfferQuote requires AK and EK and AttestationParameters and must be Attested first [%s] ", evt.EKM)
			return &verifier.OfferQuoteResponse{}, status.Errorf(codes.Internal, "Error OfferQuote requires AK and EK and AttestationParameters and must be Attested first")
		}
	} else {
		glog.Errorf("Error OfferQuote requires AK and EK and AttestationParameters and must be Attested first [%s] ", evt.EKM)
		return &verifier.OfferQuoteResponse{}, status.Errorf(codes.Internal, "Error OfferQuote requires AK and EK and AttestationParameters and must be Attested first")
	}

	nonce := []byte(uuid.New().String())

	vv := attestationKeys[evt.EKM]
	vv.Nonce = nonce

	attestationKeys[evt.EKM] = vv

	glog.V(5).Infof("=============== end OfferQuote ===============")
	return &verifier.OfferQuoteResponse{
		Nonce: nonce,
	}, nil
}

func (s *server) SetQuote(ctx context.Context, in *verifier.SetQuoteRequest) (*verifier.SetQuoteResponse, error) {
	glog.V(2).Infof("======= SetQuote ========")

	evt := ctx.Value(contextKey("event")).(event)
	glog.V(60).Infof("     Inbound gRPC request from: %s", evt.PeerIP)
	glog.V(60).Infof("     Inbound EKM: %s", evt.EKM)

	s.mu.Lock()
	defer s.mu.Unlock()
	if val, ok := attestationKeys[evt.EKM]; ok {
		if val.EKCert == nil || val.AKPub == nil || val.AttestationParameters == nil || !val.Attested || val.Nonce == nil {
			glog.Errorf("Error OfferQuote requires AK and EK and AttestationParameters, OfferQuote(nonce) and must be Attested first [%s] ", evt.EKM)
			return &verifier.SetQuoteResponse{}, status.Errorf(codes.Internal, "Error OfferQuote requires AK and EK and AttestationParameters, OfferQuote(nonce) and must be Attested first")
		}
	} else {
		glog.Errorf("Error OfferQuote requires AK and EK and AttestationParameters, OfferQuote(nonce) and must be Attested first [%s] ", evt.EKM)
		return &verifier.SetQuoteResponse{}, status.Errorf(codes.Internal, "Error OfferQuote requires AK and EK and AttestationParameters,OfferQuote(nonce) and must be Attested first")
	}

	vv := attestationKeys[evt.EKM]
	// create pcr map for go-tpm-tools
	pcrMap, _, err := getPCRMap(*expectedPCRMapSHA256, tpm.HashAlgo_SHA256)
	if err != nil {
		glog.Errorf("Could not get PCRMap: [%s] %v", evt.EKM, err)
		return &verifier.SetQuoteResponse{}, status.Errorf(codes.Internal, "  Could not get PCRMap: %s", err)
	}
	//vpcrs := &tpmpb.PCRs{Hash: tpmpb.HashAlgo_SHA256, Pcrs: pcrMap}

	serverPlatformAttestationParameter := &attest.PlatformParameters{}
	err = json.NewDecoder(bytes.NewReader(in.PlatformAttestation)).Decode(serverPlatformAttestationParameter)
	if err != nil {
		glog.Errorf("Quote Failed: json decoding quote response:  [%s] %v", evt.EKM, err)
		return &verifier.SetQuoteResponse{}, status.Errorf(codes.Internal, "Quote Failed: json decoding quote response: %v", err)
	}

	pub, err := attest.ParseAKPublic(serverPlatformAttestationParameter.Public)
	if err != nil {
		glog.Errorf("Quote Failed ParseAKPublic:  [%s] %v", evt.EKM, err)
		return &verifier.SetQuoteResponse{}, status.Errorf(codes.Internal, "Quote Failed ParseAKPublic: %v", err)
	}

	// compare the ak provided earlier during attestation with the one bound to the quote; they must be the same
	qakBytes, err := x509.MarshalPKIXPublicKey(pub.Public)
	if err != nil {
		return &verifier.SetQuoteResponse{}, status.Errorf(codes.Internal, "Error MarshalPKIPublicKey for Quote %v", err)
	}
	qakPubPEM := pem.EncodeToMemory(
		&pem.Block{
			Type:  "PUBLIC KEY",
			Bytes: qakBytes,
		},
	)

	glog.V(5).Infof("      quote-attested public \n%s\n", qakPubPEM)

	akpPub, err := x509.MarshalPKIXPublicKey(vv.AKPub)
	if err != nil {
		glog.Errorf("Error MarshalPKIXPublicKey ak   [%s] %v", evt.EKM, err)
		return &verifier.SetQuoteResponse{}, status.Errorf(codes.Internal, "Error MarshalPKIXPublicKey ak %v", err)
	}
	akPubPEM := pem.EncodeToMemory(
		&pem.Block{
			Type:  "PUBLIC KEY",
			Bytes: akpPub,
		},
	)

	if base64.StdEncoding.EncodeToString(qakPubPEM) != base64.StdEncoding.EncodeToString(akPubPEM) {
		return &verifier.SetQuoteResponse{}, status.Errorf(codes.Internal, "Attested key does not match value in quote")
	}

	for _, quote := range serverPlatformAttestationParameter.Quotes {
		if err := pub.Verify(quote, serverPlatformAttestationParameter.PCRs, vv.Nonce); err != nil {
			glog.Errorf("Quote Failed Verify:  [%s] %v", evt.EKM, err)
			return &verifier.SetQuoteResponse{}, status.Errorf(codes.Internal, " Quote Failed Verify: %v", err)
		}
	}

	for _, p := range serverPlatformAttestationParameter.PCRs {
		if !p.QuoteVerified() {
			glog.Errorf("Quote Failed Verify:  [%s] for PCR [%d] %v", evt.EKM, p.Index, err)
			return &verifier.SetQuoteResponse{}, status.Errorf(codes.Internal, " Quote Failed Verify: for PCR [%d] %v", p.Index, err)
		}
		glog.V(20).Infof("     PCR: %d, verified: %t value: %s", p.Index, p.QuoteVerified(), hex.EncodeToString((p.Digest)))
		if p.DigestAlg == crypto.SHA256 {
			v, ok := pcrMap[uint32(p.Index)]
			if ok {
				if hex.EncodeToString(v) != hex.EncodeToString(p.Digest) {
					glog.Errorf("Quote Failed Verify for index: %d [%s] expected %s, got %s ", p.Index, evt.EKM, hex.EncodeToString(v), hex.EncodeToString(p.Digest))
					return &verifier.SetQuoteResponse{}, status.Errorf(codes.Internal, "Quote Failed Verify for index: %d", p.Index)
				}
			}
		}
	}

	glog.V(5).Infof("     quotes verified")
	el, err := attest.ParseEventLog(serverPlatformAttestationParameter.EventLog)
	if err != nil {
		glog.Errorf("Quote Parsing EventLog Failed:  [%s] %v", evt.EKM, err)
		return &verifier.SetQuoteResponse{}, status.Errorf(codes.Internal, "Quote Parsing EventLog Failed: %v", err)
	}

	for _, e := range el.Events(attest.HashSHA256) {
		glog.V(60).Infof("Event Index: %d", e.Index)
		glog.V(60).Infof("   Event Type: %s", e.Type)
		glog.V(60).Infof("   Event: %s", string(e.Data))
		// determine if SEV is enabled on GCE:
		//  see https://gist.github.com/salrashid123/0c7a4a6f7465cff19d05ac50d238cd57
		// if e.Index == 0 && e.Type.String() == "EV_NONHOST_INFO" {
		// 	sevStatus, err := server.ParseGCENonHostInfo(e.Data)
		// 	if err != nil {
		// 		glog.Errorf("Error parsing SEV Status: %v", err)
		//      retrun &verifier.SetQuoteResponse{}, status.Errorf(codes.Internal, "Error parsing SEV Status: %v", err)
		// 	}
		// 	glog.V(60).Infof("     EV SevStatus: %s\n", sevStatus.String())
		// }
	}

	sb, err := attest.ParseSecurebootState(el.Events(attest.HashSHA256))
	if err != nil {
		glog.Errorf("Quote Parsing ParseSecurebootState Failed: [%s] %v", evt.EKM, err)
		return &verifier.SetQuoteResponse{}, status.Errorf(codes.Internal, "Quote Parsing ParseSecurebootState Failed: %v", err)
	}

	glog.V(5).Infof("     secureBoot State enabled: [%t]", sb.Enabled)

	if _, err := el.Verify(serverPlatformAttestationParameter.PCRs); err != nil {
		glog.Errorf("Quote Verify Failed:[%s] %v", evt.EKM, err)
		return &verifier.SetQuoteResponse{}, status.Errorf(codes.Internal, "Quote Verify Failed: %v", err)
	}

	// now issue an AK x509
	// read the root cert and key that will sign the client cert
	clientCAcrtBytes, err := os.ReadFile(*attestationRootCert)
	if err != nil {
		glog.Errorf("could not load clientCA certificate [%s] %v", evt.EKM, err)
		return &verifier.SetQuoteResponse{}, status.Errorf(codes.Internal, "could not load clientCA certificate: %v", err)
	}

	block, _ := pem.Decode(clientCAcrtBytes)
	if block == nil {
		glog.Errorf("error decoding client ca certificate file [%s]", evt.EKM)
		return &verifier.SetQuoteResponse{}, status.Errorf(codes.Internal, "error decoding client ca certificate file %v", err)
	}
	ccacrt, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		glog.Errorf("error parsing client ca certificate [%s] %v", evt.EKM, err)
		return &verifier.SetQuoteResponse{}, status.Errorf(codes.Internal, "error parsing client ca certificate  %v", err)
	}

	clientCAKeyBytes, err := os.ReadFile(*attestationRootKey)
	if err != nil {
		glog.Errorf("error reading client ca certificate private key: [%s] %v", evt.EKM, err)
		return &verifier.SetQuoteResponse{}, status.Errorf(codes.Internal, "error reading client ca certificate private key: %v", err)
	}
	caPrivPem, _ := pem.Decode(clientCAKeyBytes)
	ccakey, err := x509.ParsePKCS8PrivateKey(caPrivPem.Bytes)
	if err != nil {
		glog.Errorf("error decoding client ca certificate ca key: [%s] %v", evt.EKM, err)
		return &verifier.SetQuoteResponse{}, status.Errorf(codes.Internal, "error decoding client ca certificate ca key %v", err)
	}

	var notBefore time.Time
	notBefore = time.Now()

	notAfter := notBefore.Add(time.Hour * 24 * 365)

	serialNumberLimit := new(big.Int).Lsh(big.NewInt(1), 32)
	serialNumber, err := rand.Int(rand.Reader, serialNumberLimit)
	if err != nil {
		glog.Errorf("Failed to generate serial number: %s", err)
		return &verifier.SetQuoteResponse{}, status.Errorf(codes.Internal, "Failed to generate serial number: %s", err)
	}

	// add tpm SAN as "OtherName"

	// create a unique device serial number; the deviceID can be issued by the attestorCA as is the case here
	// pg 55: https://trustedcomputinggroup.org/wp-content/uploads/TPM-2p0-Keys-for-Device-Identity-and-Attestation_v1_r12_pub10082021.pdf
	// The subject field’s DN encoding SHOULD include the “serialNumber” attribute with the device’s unique serial number.
	deviceSerialNumberLimit := new(big.Int).Lsh(big.NewInt(1), 64)
	devserialNumber, err := rand.Int(rand.Reader, deviceSerialNumberLimit)
	if err != nil {
		glog.Errorf("Failed to generate serial number: %s", err)
		return &verifier.SetQuoteResponse{}, status.Errorf(codes.Internal, "Failed to generate serial number: %s", err)
	}
	// 4. Create a Certificate Order
	glog.V(5).Infof(">>>>>>>>  DeviceSerial Number [%s]\n", hex.EncodeToString(devserialNumber.Bytes()))

	glog.V(5).Infof("      verify quote, PCRs and secureBootState")

	var oidAIKCertificate = asn1.ObjectIdentifier{2, 23, 133, 8, 3}

	pid, err := marshalOtherName(oidPermanentIdentifier, permanentIdentifier{
		IdentifierValue: hex.EncodeToString(devserialNumber.Bytes()),
	})
	if err != nil {
		glog.Errorf("Failed to create permanentIdentifier: %s", err)
		return &verifier.SetQuoteResponse{}, status.Errorf(codes.Internal, "Failed to create permanentIdentifier: %s", err)
	}

	// extract the DirName from the EK and use those
	// DirName:/tcg-at-tpmManufacturer=id:00001014/tcg-at-tpmModel=swtpm/tcg-at-tpmVersion=id:20240125
	// dirName := pkix.Name{
	// 	ExtraNames: []pkix.AttributeTypeAndValue{
	// 		{Type: oid.TPMManufacturer, Value: "id:00001014"},
	// 		{Type: oid.TPMModel, Value: "swtpm"},
	// 		{Type: oid.TPMVersion, Value: "id:20240125"},
	// 	},
	// }

	var tpmManufacturer string
	var tpmModel string
	var tpmVersion string

	for _, ex := range vv.EKCert.Extensions {
		if ex.Id.Equal(oidExtensionSubjectAltName) {
			s, err := x509ext.ParseSubjectAltName(ex)
			if err != nil {
				glog.Errorf("Error  failed to parse EK to unmarshal EK SAN [%s] %v", evt.EKM, err.Error())
				return &verifier.SetQuoteResponse{}, status.Errorf(codes.Internal, "failed to parse EK to unmarshal EK SAN [%s] %v", evt.EKM, err.Error())
			}
			for _, na := range s.DirectoryNames {
				for _, attr := range na.Names {
					if attr.Type.Equal(oid.TPMManufacturer) {
						tpmManufacturer = fmt.Sprintf("%s", attr.Value)
					}
					if attr.Type.Equal(oid.TPMModel) {
						tpmModel = fmt.Sprintf("%s", attr.Value)
					}
					if attr.Type.Equal(oid.TPMVersion) {
						tpmVersion = fmt.Sprintf("%s", attr.Value)
					}
				}
			}
		}
	}

	dirName := pkix.Name{
		ExtraNames: []pkix.AttributeTypeAndValue{
			{Type: oid.TPMManufacturer, Value: tpmManufacturer},
			{Type: oid.TPMModel, Value: tpmModel},
			{Type: oid.TPMVersion, Value: tpmVersion},
		},
	}

	rdnSeq := dirName.ToRDNSequence()
	marshaledRDN, err := asn1.Marshal(rdnSeq)
	if err != nil {
		glog.Errorf("Failed to marshal RDN sequence: %v", err)
		return &verifier.SetQuoteResponse{}, status.Errorf(codes.Internal, "Failed to marshal RDN sequence: %v", err)
	}

	dirNameRaw := asn1.RawValue{
		Class:      2,    // Context-specific class
		Tag:        4,    // tag [4] for directoryName
		IsCompound: true, // EXPLICIT wraps the inner elements
		Bytes:      marshaledRDN,
	}

	// pg 57 https://trustedcomputinggroup.org/wp-content/uploads/TPM-2p0-Keys-for-Device-Identity-and-Attestation_v1_r12_pub10082021.pdf
	// The TCG registered OID (2.23.133.1.2) represents the hwType of TPM 2.0.
	// The hwSerialNum value is an OCTET STRING and SHALL be constructed by one of two methods:
	// 1. When the TPM has an EK Certificate, the hwSerialNum is created by concatenating three ASCII values: The
	// TCG TPM Manufacturer code, the EK Authority Key Identifier and the EK CertificateSerialNumber. These three
	// fields SHALL be separated by a colon (‘:’) character. The three values SHALL be listed in the order specified
	// above.
	// 2. When the TPM does not have an EK certificate, the hwSerialNum is a digest of the EK Certificate public key.
	// swtpm: id:00001014

	sn := fmt.Sprintf("%s:%s:%s", "00001014", hex.EncodeToString(vv.EKCert.AuthorityKeyId), fmt.Sprintf("%x", vv.EKCert.SerialNumber))
	pic, err := marshalOtherName(oidHardwareModuleName, hardwareModuleName{
		Type:         oidTPMHardwareType,
		SerialNumber: []byte(sn),
	})
	if err != nil {
		glog.Errorf("Failed to create oidHardwareModuleName: %s", err)
		return &verifier.SetQuoteResponse{}, status.Errorf(codes.Internal, "Failed to create oidHardwareModuleName: %s", err)
	}

	ccd, err := mustMarshal([]asn1.RawValue{pic, pid, dirNameRaw})
	if err != nil {
		glog.Errorf("Failed to mustMarshal otherName: %s", err)
		return &verifier.SetQuoteResponse{}, status.Errorf(codes.Internal, "Failed to mustMarshal otherName: %s", err)
	}

	extSubjectAltNamed := pkix.Extension{
		Id:       oidExtensionSubjectAltName,
		Critical: false,
		Value:    ccd,
	}

	// add policy constraints
	// pg4  https://trustedcomputinggroup.org/wp-content/uploads/TCG-OID-Registry-Version-1.00-Revision-0.74_10July24.pdf
	// 2.23.133.11.1.1 tcg-cap-verifiedTPMResidency
	// 2.23.133.11.1.2 tcg-cap-verifiedTPMFixed

	oidverifiedTPMResidency, err := x509.OIDFromASN1OID(asn1.ObjectIdentifier{2, 23, 133, 11, 1, 1})
	if err != nil {
		glog.Errorf("Failed to crate Policy Constraint: [%s] %v", evt.EKM, err)
		return &verifier.SetQuoteResponse{}, status.Errorf(codes.Internal, "Failed to create x509OID oidverifiedTPMResidency: %s", err)
	}

	oidverifiedTPMFixed, err := x509.OIDFromASN1OID(asn1.ObjectIdentifier{2, 23, 133, 11, 1, 2})
	if err != nil {
		glog.Errorf("Failed to crate Policy Constraint: [%s] %v", evt.EKM, err)
		return &verifier.SetQuoteResponse{}, status.Errorf(codes.Internal, "Failed to create x509OID oidverifiedTPMFixed: %s", err)
	}

	oidverifiedTPMRestricted, err := x509.OIDFromASN1OID(asn1.ObjectIdentifier{2, 23, 133, 11, 1, 3})
	if err != nil {
		glog.Errorf("Failed to crate Policy Constraint: [%s] %v", evt.EKM, err)
		return &verifier.SetQuoteResponse{}, status.Errorf(codes.Internal, "Failed to create x509OID oidverifiedTPMRestricted %s", err)
	}

	template := x509.Certificate{
		SerialNumber:          serialNumber,
		NotBefore:             notBefore,
		NotAfter:              notAfter,
		KeyUsage:              x509.KeyUsageDigitalSignature,
		UnknownExtKeyUsage:    []asn1.ObjectIdentifier{oidAIKCertificate},
		Policies:              []x509.OID{oidverifiedTPMResidency, oidverifiedTPMFixed, oidverifiedTPMRestricted},
		ExtraExtensions:       []pkix.Extension{extSubjectAltNamed},
		BasicConstraintsValid: true,
		IsCA:                  false,
	}
	derBytes, err := x509.CreateCertificate(rand.Reader, &template, ccacrt, vv.AKCSR.PublicKey, ccakey)
	if err != nil {
		glog.Errorf("Failed to create certificate: [%s] %v", evt.EKM, err)
		return &verifier.SetQuoteResponse{}, status.Errorf(codes.Internal, "Failed to create ak certificate: %s", err)
	}

	issuedakcrtPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: derBytes})

	akcert, err := x509.ParseCertificate(derBytes)
	if err != nil {
		glog.Errorf("Failed to create certificate: [%s] %v", evt.EKM, err)
		return &verifier.SetQuoteResponse{}, status.Errorf(codes.Internal, "Failed to parse ak certificate: %s", err)
	}

	akcertPrintable, err := certinfo.CertificateText(akcert)
	if err != nil {
		glog.Errorf("Failed to format certificate: [%s] %v", evt.EKM, err)
		return &verifier.SetQuoteResponse{}, status.Errorf(codes.Internal, "Failed to format ak certificate: %s", err)
	}
	glog.V(5).Infof("Issued AK Certificate: \n%s\n%s\n", string(issuedakcrtPEM), akcertPrintable)

	vv.AKCert = akcert

	attestationKeys[evt.EKM] = vv

	glog.V(5).Infof("=============== Attestation x509 Sent ===============")
	return &verifier.SetQuoteResponse{
		AkCertificate: derBytes,
	}, nil
}

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

// NewServer returns a new Server.
func NewServer() *server {
	return &server{
		statusMap: make(map[string]healthpb.HealthCheckResponse_ServingStatus),
	}
}

func main() {
	os.Exit(run()) // since defer func() needs to get called first
}

func run() int {
	flag.Set("logtostderr", "true")
	flag.Set("stderrthreshold", "INFO")
	flag.Parse()
	var err error

	defaultCerts, err := tls.LoadX509KeyPair(*tlsCert, *tlsKey)
	if err != nil {
		glog.Errorf("failed to create default certs: %v", err)
		return 1
	}

	clientCAcrtBytes, err := os.ReadFile(*tlsCert)
	if err != nil {
		glog.Errorf("could not load clientCA certificate  %v", err)
		return 1
	}
	client_cert_pool := x509.NewCertPool()
	ok := client_cert_pool.AppendCertsFromPEM(clientCAcrtBytes)
	if !ok {
		glog.Errorf("Error parsing singing ca")
		return 1
	}
	tlsConfig := &tls.Config{
		NextProtos:   []string{"h2", "http/1.1"},
		Certificates: []tls.Certificate{defaultCerts},
		ClientAuth:   tls.RequestClientCert,
		ClientCAs:    client_cert_pool,
	}
	ce := credentials.NewTLS(tlsConfig)

	glog.V(2).Infof("Starting gRPC server on port %v", *grpcPort)
	sopts := []grpc.ServerOption{grpc.MaxConcurrentStreams(10)}
	sopts = append(sopts, grpc.Creds(ce), grpc.UnaryInterceptor(authUnaryInterceptor))
	s := grpc.NewServer(sopts...)
	srv := NewServer()
	verifier.RegisterVerifierServer(s, srv)
	healthpb.RegisterHealthServer(s, srv)

	lis, err := net.Listen("tcp", *grpcPort)
	if err != nil {
		glog.Errorf("failed to listen: %v", err)
		return 1
	}
	err = s.Serve(lis)
	if err != nil {
		glog.Errorf("could not start grpcServer  %v", err)
		return 1
	}

	return 0
}

func getPCRMap(expectedPCRMapSHA256 string, algo tpm.HashAlgo) (map[uint32][]byte, []byte, error) {

	pcrMap := make(map[uint32][]byte)
	var hsh hash.Hash
	// https://github.com/tpm2-software/tpm2-tools/blob/83f6f8ac5de5a989d447d8791525eb6b6472e6ac/lib/tpm2_openssl.c#L206
	if algo == tpm.HashAlgo_SHA1 {
		hsh = sha1.New()
	}
	if algo == tpm.HashAlgo_SHA256 {
		hsh = sha256.New()
	}
	if algo == tpm.HashAlgo_SHA1 || algo == tpm.HashAlgo_SHA256 {
		for _, v := range strings.Split(expectedPCRMapSHA256, ",") {
			entry := strings.Split(v, ":")
			if len(entry) == 2 {
				uv, err := strconv.ParseUint(entry[0], 10, 32)
				if err != nil {
					return nil, nil, fmt.Errorf(" PCR key:value is invalid in parsing %s", v)
				}
				hexEncodedPCR, err := hex.DecodeString(entry[1])
				if err != nil {
					return nil, nil, fmt.Errorf(" PCR key:value is invalid in encoding %s", v)
				}
				pcrMap[uint32(uv)] = hexEncodedPCR
				hsh.Write(hexEncodedPCR)
			} else {
				return nil, nil, fmt.Errorf(" PCR key:value is invalid %s", v)
			}
		}
	} else {
		return nil, nil, fmt.Errorf("Unknown Hash Algorithm for TPM PCRs %v", algo)
	}
	if len(pcrMap) == 0 {
		return nil, nil, fmt.Errorf(" PCRMap is null")
	}
	return pcrMap, hsh.Sum(nil), nil
}

type TPM struct {
	_ crypto.Signer
	//_ crypto.MessageSigner // introduced in https://tip.golang.org/doc/go1.25#cryptopkgcrypto
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
	return t.AK.SignMsg(t.TPM, digest, opts)
}

func (t TPM) SignMessage(rand io.Reader, msg []byte, opts crypto.SignerOpts) (signature []byte, err error) {
	return t.AK.SignMsg(t.TPM, msg, opts)
}
