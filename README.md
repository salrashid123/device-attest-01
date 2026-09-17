# ACME Device Attestation and Certificate Enrollment for Trusted Plaform Module

Sample application which uses ACME to provision an x509 mTLS client certificate such that it is bound to the client device TPM.

It is basially a replay of the prodocol described here

- [Automatic Certificate Management Environment (ACME) Device Attestation Extension](https://datatracker.ietf.org/doc/draft-ietf-acme-device-attest/)

As an overview, this repo consissts of several components

1. Client Device with a TPM
2. Attesttation Server which perform TPM Remote Attestation and issues an Attestation Certificate
3. ACME Server and CA which uses Attestions to issue an x509 certificate

Visually, its something like this. You can combine many of the remote attestation steps together but i've intentionally left them separate api calls:

![images/da_flow.png](images/da_flow.png)

In this specific setup, there are actually two distinct Certificate Authorities.

- `a.` Attestation CA on the Attestation server which verifies the client's TPM and issues an Attestation certificate to that device

- `b.` ACME server which runs its own CA to issue client certificates and is configured to accept device attestations signed by the Attestation CA.

For more general reading, see

- [Managed Device Attestation: ACME as the Bottom Turtle in Mobile Device Management](https://smallstep.com/blog/managed-device-attestation/)
- [ACME Device Attestation: The Modern Zero Trust Alternative to SCEP](https://www.bastionxp.com/blog/acme-device-attestation-vs-scep-zero-trust/)
- [ACME device attestation, smallstep and pkcs11: attezt](https://linderud.dev/blog/acme-device-attestation-smallstep-and-pkcs11-attezt/)

In this sample, once the x609 is issued, you can skip to the [Testing](#testing) to try out various mtls TPM clients

>> NOTE: this repo is *not* supported by google

---

* [Step-CA Setup](#step-ca-setup)
* [TPM ACME](#tpm-acme)
  - [Configure TPM](#configure-tpm)
  - [Attestation Server](#attestation-server)
  - [Device Client](#device-client)
  - [Logs](#logs)
    - [Client Logs](#client-logs)
    - [Server Logs](#server-logs)
    - [Acme Server](#acme-server)
  - [Testing](#testing)
    - [Start HTTPS mTLS Server](#start-https-mtls-server)
    - [go client](#go-client)
    - [openssl client](#openssl-client)
    - [python client](#python-client)     
* [HTTP ACME](#http-acme)    

---

## Step-CA Setup

To get started, you'll need golang and `smallstep-ca`, `smallstep-cli`

```bash
# First clear any existing smallstep config (if don't want to do this, make a backup of the `$HOME/.step` folder)
mv $HOME/.step $HOME/.step_backup

### setup some hosts files
$ cat /etc/hosts
127.0.0.1 server.domain.com ca.domain.com

step ca init 

### the values to use here
#### Standalone
#### mTLS ACME CA
#### ca.domain.com
#### 127.0.0.1:8443
#### provisioner1
#### someeasypassword

        ✔ Deployment Type: Standalone
        What would you like to name your new PKI?
        ✔ (e.g. Smallstep): mTLS ACME CA
        What DNS names or IP addresses will clients use to reach your CA?
        ✔ (e.g. ca.example.com[,10.1.2.3,etc.]): ca.domain.com
        What IP and port will your new CA bind to? (:443 will bind to 0.0.0.0:443)
        ✔ (e.g. :443 or 127.0.0.1:443): 127.0.0.1:8443
        What would you like to name the CA's first provisioner?
        ✔ (e.g. you@smallstep.com): provisioner1
        Choose a password for your CA keys and first provisioner.
        ✔ [leave empty and we'll generate one]: 

        Generating root certificate... done!
        Generating intermediate certificate... done!

        ✔ Root certificate: /home/srashid/.step/certs/root_ca.crt
        ✔ Root private key: /home/srashid/.step/secrets/root_ca_key
        ✔ Root fingerprint: 93e4817050f9f63ad34ecb9a10c7e47e8b7dd8290bd1d8bf8f45b9271934f348
        ✔ Intermediate certificate: /home/srashid/.step/certs/intermediate_ca.crt
        ✔ Intermediate private key: /home/srashid/.step/secrets/intermediate_ca_key
        ✔ Database folder: /home/srashid/.step/db
        ✔ Default configuration: /home/srashid/.step/config/defaults.json
        ✔ Certificate Authority configuration: /home/srashid/.step/config/ca.json


## start step-ca
step-ca $(step path)/config/ca.json

### configure the TPM challenge using a trust anchored on `certs/attestation-root-ca.crt` provided in this repo
cd tpm/
step ca provisioner add acme-da --type ACME   --attestation-roots certs/attestation-root-ca.crt   --challenge device-attest-01    --attestation-format tpm

### optionally setup an HTTP challenge (this is used for the optional HTTP demo later)
## step ca provisioner add myacme --type ACME

```

## TPM ACME

For the TPM demo, startup a software tpm `swtpm`:

### Configure TPM

Before an ACME certificate can get issued, the device must be attested.  This demo involves full TPM Remote Attestation and also verifies the TPM EventLog as part of the Quote-Verify flow.  The following starts a [software TPM](https://github.com/stefanberger/swtpm), and replays the events from a GCP Shielded VM's event log.  The net result is the PCR values the script will mimic a secure boot sequence from a GCP VM.  For more information, see [EventLog Replay](https://github.com/salrashid123/go_tpm_remote_attestation#setup-using-softwretpm)

```bash
cd tpm/swtpm/
# rm -rf myvtpm && mkdir myvtpm && swtpm_setup --tpmstate myvtpm --tpm2 --create-ek-cert
swtpm socket --tpmstate dir=myvtpm --tpm2 --server type=tcp,port=2321 --ctrl type=tcp,port=2322 --flags not-need-init,startup-clear --log level=5

export TPM2TOOLS_TCTI="swtpm:port=2321"

go run eventlog.go  --eventLogFile=binary_bios_measurements --tpm-path="127.0.0.1:2321"
```

Now start the server grpc Attestation Server

### Attestation Server

The remote attestation flow between the client and server are done over gRPC (you can use any other mechanism).  The specific flow and code is taken from:

* [TPM Remote Attestation protocol using go-tpm and gRPC](https://github.com/salrashid123/go_tpm_remote_attestation)

```bash
$ go run attestation_server/attestaion_server.go  \
        --ekrootCA swtpm/config/var/lib/swtpm-localca/issuercert.pem  \
        --expectedPCRMapSHA256=0:a0b5ff3383a1116bd7dc6df177c0c2d433b9ee1813ea958fa5d166a202cb2a85 \
        --v=40 -alsologtostderr
```

The current go client and attestation server uses [go-attestation](https://github.com/google/go-tpm) library constructs because its easier.  If you would rather use low level constructs for remote attestation see [TPM Remote Attestation, Quote/Verify, NewKey Certification with TPM2_Direct](https://github.com/salrashid123/tpm2/tree/master/tpm_remote_attestation)

### Device Client

Start the device Client

```bash
$ go run device/client.go -host 127.0.0.1:50051  \
  --tpm-path="127.0.0.1:2321" --stepCACertPath=$HOME/.step/certs/root_ca.crt \
  --eventLogPath=swtpm/binary_bios_measurements -tpmKeyFilePEM=certs/tpmkey.pem -tpmKeyFile=certs/tpmkey.json   \
  --v=10 -alsologtostderr
```

Note the client will write the issued x509 certificate to `certs/cert.pem` and will write the TPM based private key in two formats:  `go-attestation` key format to: `certs/tpmkey.json` and a PEM formatted TPM key to `certs/tpmkey.pem`.  The PEM format is described [here](https://www.hansenpartnership.com/draft-bottomley-tpm2-keys.html) and is compatible with openssl

---

### Logs

Once you run the client and server, you'll see the sample output on the client

#### Client Logs

```log
$ go run client/client.go -host 127.0.0.1:50051 \
   --tpm-path="127.0.0.1:2321" \
    --stepCACertPath=$HOME/.step/certs/root_ca.crt \
      --eventLogPath=swtpm/binary_bios_measurements    --v=10 -alsologtostderr

I0917 14:11:35.530506 1359517 client.go:133] Opening swtpm socket
I0917 14:11:35.532228 1359517 client.go:175] Manufacturer: IBM
I0917 14:11:35.532285 1359517 client.go:176] VendorInfo: SW   TPM
I0917 14:11:35.532304 1359517 client.go:177] FirmwareVersionMajor: 8228
I0917 14:11:35.532322 1359517 client.go:178] FirmwareVersionMinor: 293
I0917 14:11:35.533189 1359517 client.go:188] EKCert Issuer: CN=swtpm-localca
I0917 14:11:35.533242 1359517 client.go:205] EKCert SerialNumber: 1237
I0917 14:11:35.533276 1359517 client.go:209] =============== OfferEK ===============
I0917 14:11:35.544214 1359517 client.go:218] Verified EK Cert
I0917 14:11:35.544271 1359517 client.go:220] =============== OfferAK ===============
I0917 14:11:35.634911 1359517 client.go:245] Creating AK CSR
I0917 14:11:35.640984 1359517 client.go:277] AK CSR 
-----BEGIN CERTIFICATE REQUEST-----
MIIClDCCAXwCAQAwHjEcMBoGA1UEAxMTYXR0ZXN0b3IuZG9tYWluLmNvbTCCASIw
DQYJKoZIhvcNAQEBBQADggEPADCCAQoCggEBAKDfnq7bqTyO1VzC5AoOh4hZ34tK
J66+8tiYexRUGTY1Qh+7O97dxFtjjcnH6Gq4Wl+DJdjPCxD35XtTzoNHlfSyXtoR
D74IBAopH6hdFQq3FlduhYGWM6bDR3YC/DdXZp3mk+YhuKgwCIIadkzcFMcs/cJG
1K3NWrKGoOCMLXhQIjFQtlzPObGraEk7dml9ASF0U+8HUAkn0AU7Sz9wOpfNA7a6
mI0CAehqEcSF1V+TvX1PrmOeN2r4BVga0RIZo8VYIy1v4WUA+jswgUvsPZv+HIoV
SP6if9/PYp09zwtl2RaOZz1oN2RmNYs7ltwy5VnGsKGVedYS+e01tcwJa/cCAwEA
AaAxMC8GCSqGSIb3DQEJDjEiMCAwHgYDVR0RBBcwFYITYXR0ZXN0b3IuZG9tYWlu
LmNvbTANBgkqhkiG9w0BAQsFAAOCAQEAGLtJ9u9oXtiU4o7EfHKdwUeH+pjwTeKN
OWEJRp2/ygdeTY93sfXTtRkEFwM7SZx5bLfG69rIpNiLXQ4f+hILSV4VZrk86uZ0
+8CnUznllygQVc+ssEzG3aSjczrFrLbYm9wVifgw7Gkye8Ib/1MsNoByk4JDTNxt
1B7+8Ep5rlV9QUL1407/evn3c26NVG5H9ca/hXSMybWzfQuTHSxVNpCNr/8JSr6e
C0CICVlV/9iQzYBiYaPmLzWx8TupYvFWVtMfGzUw9Wl4KFStUV4nYlMS/bqrbgLX
byL5gBa4/zdZRzNo3GYoLZkT1Uzt65wap+OObH40FAL/kd5K/bd8jw==
-----END CERTIFICATE REQUEST-----

I0917 14:11:35.642679 1359517 client.go:288] Verified AK 
I0917 14:11:35.642745 1359517 client.go:290] =============== GetMakeCredential ===============
I0917 14:11:35.685124 1359517 client.go:319] EncryptedCredentials Secret 21xqc8dTBMLi+kzJgLOxjElRcy6jYi7Y1TKIvz14Rxo=
I0917 14:11:35.685207 1359517 client.go:321] =============== SetActivateCredential ===============
I0917 14:11:35.686430 1359517 client.go:330] SetActivateCredential complete 
I0917 14:11:35.686510 1359517 client.go:332] =============== OfferQuote ===============
I0917 14:11:35.687368 1359517 client.go:339] OfferQuote complete 
I0917 14:11:35.687438 1359517 client.go:341] =============== SetQuote ===============
I0917 14:11:35.704219 1359517 client.go:385] Issued AK Certificate: 
-----BEGIN CERTIFICATE-----
MIIDXTCCAwOgAwIBAgIFAMDYOUgwCgYIKoZIzj0EAwIwWzELMAkGA1UEBhMCVVMx
DzANBgNVBAoMBkdvb2dsZTEdMBsGA1UECwwUQXR0ZXN0YXRpb24gVmVyaWZpZXIx
HDAaBgNVBAMME0F0dGVzdGF0aW9uIFJvb3QgQ0EwHhcNMjYwOTE3MTgxMTM1WhcN
MjcwOTE3MTgxMTM1WjAAMIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEA
oN+ertupPI7VXMLkCg6HiFnfi0onrr7y2Jh7FFQZNjVCH7s73t3EW2ONycfoarha
X4Ml2M8LEPfle1POg0eV9LJe2hEPvggECikfqF0VCrcWV26FgZYzpsNHdgL8N1dm
neaT5iG4qDAIghp2TNwUxyz9wkbUrc1asoag4IwteFAiMVC2XM85satoSTt2aX0B
IXRT7wdQCSfQBTtLP3A6l80DtrqYjQIB6GoRxIXVX5O9fU+uY543avgFWBrREhmj
xVgjLW/hZQD6OzCBS+w9m/4cihVI/qJ/389inT3PC2XZFo5nPWg3ZGY1izuW3DLl
WcawoZV51hL57TW1zAlr9wIDAQABo4IBQjCCAT4wDgYDVR0PAQH/BAQDAgeAMBAG
A1UdJQQJMAcGBWeBBQgDMAwGA1UdEwEB/wQCMAAwHwYDVR0jBBgwFoAUUA0oLf1M
FqjzMvQhFZys3Xnv4jkwJwYDVR0gBCAwHjAIBgZngQULAQEwCAYGZ4EFCwECMAgG
BmeBBQsBAzCBwQYDVR0RBIG5MIG2oEwGCCsGAQUFBwgEoEAwPgYFZ4EFAQKENTAw
MDAxMDE0OjJmNmQ1MWRiNzczNmVjYjkyZGNkZTIyNzgwMzFjOGIxZWNjMzg3YjQ6
NGQ1oCAGCCsGAQUFBwgDoBQwEgwQYzE3OTMzZDE3NjkyMjk2NKREMEIxFjAUBgVn
gQUCARMLaWQ6MDAwMDEwMTQxEDAOBgVngQUCAhMFc3d0cG0xFjAUBgVngQUCAxML
aWQ6MjAyNDAxMjUwCgYIKoZIzj0EAwIDSAAwRQIhAP/yN+V/EvNdYuRv3Yf6DvI7
H5Ox5Qkb/A59hpq4q+H/AiBEVtqbgNwblkEdkx9RBTw8qu3iloExZBXfFLFAjCvM
eQ==
-----END CERTIFICATE-----

Certificate:
    Data:
        Version: 3 (0x2)
        Serial Number: 3235395912 (0xc0d83948)
        Signature Algorithm: ECDSA-SHA256
        Issuer: C=US,O=Google,OU=Attestation Verifier,CN=Attestation Root CA
        Validity
            Not Before: Sep 17 18:11:35 2026 UTC
            Not After : Sep 17 18:11:35 2027 UTC
        Subject:
        Subject Public Key Info:
            Public Key Algorithm: RSA
                Public-Key: (2048 bit)
                Modulus:
                    a0:df:9e:ae:db:a9:3c:8e:d5:5c:c2:e4:0a:0e:87:
                    88:59:df:8b:4a:27:ae:be:f2:d8:98:7b:14:54:19:
                    36:35:42:1f:bb:3b:de:dd:c4:5b:63:8d:c9:c7:e8:
                    6a:b8:5a:5f:83:25:d8:cf:0b:10:f7:e5:7b:53:ce:
                    83:47:95:f4:b2:5e:da:11:0f:be:08:04:0a:29:1f:
                    a8:5d:15:0a:b7:16:57:6e:85:81:96:33:a6:c3:47:
                    76:02:fc:37:57:66:9d:e6:93:e6:21:b8:a8:30:08:
                    82:1a:76:4c:dc:14:c7:2c:fd:c2:46:d4:ad:cd:5a:
                    b2:86:a0:e0:8c:2d:78:50:22:31:50:b6:5c:cf:39:
                    b1:ab:68:49:3b:76:69:7d:01:21:74:53:ef:07:50:
                    09:27:d0:05:3b:4b:3f:70:3a:97:cd:03:b6:ba:98:
                    8d:02:01:e8:6a:11:c4:85:d5:5f:93:bd:7d:4f:ae:
                    63:9e:37:6a:f8:05:58:1a:d1:12:19:a3:c5:58:23:
                    2d:6f:e1:65:00:fa:3b:30:81:4b:ec:3d:9b:fe:1c:
                    8a:15:48:fe:a2:7f:df:cf:62:9d:3d:cf:0b:65:d9:
                    16:8e:67:3d:68:37:64:66:35:8b:3b:96:dc:32:e5:
                    59:c6:b0:a1:95:79:d6:12:f9:ed:35:b5:cc:09:6b:
                    f7
                Exponent: 65537 (0x10001)
        X509v3 extensions:
            X509v3 Key Usage: critical
                Digital Signature
            X509v3 Extended Key Usage:
                2.23.133.8.3
            X509v3 Basic Constraints: critical
                CA:FALSE
            X509v3 Authority Key Identifier:
                50:0D:28:2D:FD:4C:16:A8:F3:32:F4:21:15:9C:AC:DD:79:EF:E2:39
            X509v3 Certificate Policies:
                Policy: 2.23.133.11.1.1
                Policy: 2.23.133.11.1.2
                Policy: 2.23.133.11.1.3
            X509v3 Subject Alternative Name:
                Hardware Module Name: Type: 2.23.133.1.2, Serial Number: 00001014:2f6d51db7736ecb92dcde2278031c8b1ecc387b4:4d5
                Permanent Identifier: c17933d176922964
                TPM Manufacturer: id:00001014
                TPM Model: swtpm
                TPM Version: id:20240125
    Signature Algorithm: ECDSA-SHA256
         30:45:02:21:00:ff:f2:37:e5:7f:12:f3:5d:62:e4:6f:dd:87:
         fa:0e:f2:3b:1f:93:b1:e5:09:1b:fc:0e:7d:86:9a:b8:ab:e1:
         ff:02:20:44:56:da:9b:80:dc:1b:96:41:1d:93:1f:51:05:3c:
         3c:aa:ed:e2:96:81:31:64:15:df:14:b1:40:8c:2b:cc:79

I0917 14:11:35.705309 1359517 client.go:387] =============== Create new Key ===============
I0917 14:11:35.705403 1359517 client.go:402] Extracted Permanent Identfier: c17933d176922964
I0917 14:11:35.705694 1359517 client.go:416] Extracted HardwareSerialNumber: 00001014:2f6d51db7736ecb92dcde2278031c8b1ecc387b4:4d5
I0917 14:11:35.705769 1359517 client.go:418]      Starting ACME Key generation
I0917 14:11:35.729603 1359517 client.go:465] Successfully registered ACME account.
I0917 14:11:35.741500 1359517 client.go:474] Order created. URI: https://ca.domain.com:8443/acme/acme-da/order/MVuKEeQUEoEdBKQ4OsUu7YYq0kilZEv8
I0917 14:11:35.747237 1359517 client.go:499] Fulfill challenge token: Zj81KIuk2YxowWclagoqCf3mC3sO9Uyz
I0917 14:11:35.747349 1359517 client.go:501] =============== Create New Key and set challengToken ===============
I0917 14:11:35.747433 1359517 client.go:512] KEYAUTH: Zj81KIuk2YxowWclagoqCf3mC3sO9Uyz.A2X3tkJk7itV64WdXN3PjFLFoimzwZBSDzu8yS9-kQI
I0917 14:11:35.747494 1359517 client.go:513] sha356sum(KEYAUTH): hVHqoV1u0Qch/6FRRTz8FMwMsfFQECGjYWEyZPdPjrE=
I0917 14:11:35.747553 1359517 client.go:514] Create a TPM based key
I0917 14:11:35.762627 1359517 client.go:578] NewKey Name 000b4ebdcd664a254140bcde7d82dea2f64dac7d76eddc9e6ee67fad91b7e3c93db2
I0917 14:11:35.762824 1359517 client.go:599] Generated ECC Public 
-----BEGIN PUBLIC KEY-----
MFkwEwYHKoZIzj0CAQYIKoZIzj0DAQcDQgAEUc2gOVkQ7Nrb8R/0ZRZGft5ZSlak
Rf47mRu9ze8ezHo7iJWP7cQPQFdzqEHyJJO2g+PwXujlnIhINXHbs/lX8g==
-----END PUBLIC KEY-----
I0917 14:11:35.763211 1359517 client.go:655]      NewKey derived Name from TPMSAttest: 000b4ebdcd664a254140bcde7d82dea2f64dac7d76eddc9e6ee67fad91b7e3c93db2
I0917 14:11:35.763300 1359517 client.go:656]      Certify Extra Data from TPMSAttest: hVHqoV1u0Qch/6FRRTz8FMwMsfFQECGjYWEyZPdPjrE=
I0917 14:11:35.763704 1359517 client.go:720]      ECDSA Signature verified
I0917 14:11:35.763919 1359517 client.go:770]      Regenerated New Key objectAttributes:
I0917 14:11:35.764003 1359517 client.go:771]        FixedParent: true
I0917 14:11:35.764083 1359517 client.go:772]        SensitiveDataOrigin: true
I0917 14:11:35.764156 1359517 client.go:773]        FixedTPM: true
I0917 14:11:35.764228 1359517 client.go:774]        Regenerated Name from PublicKey and template: 000b4ebdcd664a254140bcde7d82dea2f64dac7d76eddc9e6ee67fad91b7e3c93db2
I0917 14:11:35.764302 1359517 client.go:775]        Regenerated PublicKey: 
-----BEGIN PUBLIC KEY-----
MFkwEwYHKoZIzj0CAQYIKoZIzj0DAQcDQgAEUc2gOVkQ7Nrb8R/0ZRZGft5ZSlak
Rf47mRu9ze8ezHo7iJWP7cQPQFdzqEHyJJO2g+PwXujlnIhINXHbs/lX8g==
-----END PUBLIC KEY-----

I0917 14:11:38.766304 1359517 client.go:795] started server accepting challenge
I0917 14:11:38.783664 1359517 client.go:808] Waiting for order readiness validation...
I0917 14:11:38.795508 1359517 client.go:862] Finalizing order with CSR...
I0917 14:11:38.816528 1359517 client.go:950] Acme Root Certificate: 
Certificate:
    Data:
        Version: 3 (0x2)
        Serial Number: 204584451795115047511723820719834587626 (0x99e9883d6554a096e15cfe2da1cae1ea)
        Signature Algorithm: ECDSA-SHA256
        Issuer: O=mTLS ACME CA,CN=mTLS ACME CA Root CA
        Validity
            Not Before: Sep 16 16:50:03 2026 UTC
            Not After : Sep 13 16:50:03 2036 UTC
        Subject: O=mTLS ACME CA,CN=mTLS ACME CA Root CA
        Subject Public Key Info:
            Public Key Algorithm: ECDSA
                Public-Key: (256 bit)
                X:
                    14:4e:75:1f:64:35:ff:57:85:b8:2d:ab:27:c4:45:
                    94:bd:84:3f:08:0d:c2:9a:ac:ca:17:c3:4e:a9:b5:
                    35:9b
                Y:
                    0b:61:4b:eb:23:74:74:8d:63:cc:68:ad:8d:08:dd:
                    9e:ba:d0:c4:e0:69:3d:6e:e3:f3:40:89:a8:3a:02:
                    a9:e2
                Curve: P-256
        X509v3 extensions:
            X509v3 Key Usage: critical
                Certificate Sign, CRL Sign
            X509v3 Basic Constraints: critical
                CA:TRUE, pathlen:1
            X509v3 Subject Key Identifier:
                5D:6F:1A:11:16:1B:1A:C6:FD:D0:06:E5:0A:64:D9:FE:8E:B2:74:01
    Signature Algorithm: ECDSA-SHA256
         30:45:02:21:00:db:f8:16:ad:4c:e3:7d:87:e5:9d:d6:97:08:
         54:94:89:2c:50:46:82:1f:f3:63:92:06:1d:7e:eb:79:22:d3:
         6e:02:20:65:b0:14:3e:39:60:7e:04:e4:63:e1:83:c2:72:75:
         f2:d8:30:9b:70:ae:87:e6:9e:82:4b:ba:cd:7a:45:2b:60

I0917 14:11:38.817086 1359517 client.go:974] Issued Certificate: 
Certificate:
    Data:
        Version: 3 (0x2)
        Serial Number: 33606931744527285064901955453127966387 (0x194875a99021dee1cc9751bad30b3eb3)
        Signature Algorithm: ECDSA-SHA256
        Issuer: O=mTLS ACME CA,CN=mTLS ACME CA Intermediate CA
        Validity
            Not Before: Sep 17 18:10:35 2026 UTC
            Not After : Sep 18 18:11:35 2026 UTC
        Subject: CN=c17933d176922964
        Subject Public Key Info:
            Public Key Algorithm: ECDSA
                Public-Key: (256 bit)
                X:
                    51:cd:a0:39:59:10:ec:da:db:f1:1f:f4:65:16:46:
                    7e:de:59:4a:56:a4:45:fe:3b:99:1b:bd:cd:ef:1e:
                    cc:7a
                Y:
                    3b:88:95:8f:ed:c4:0f:40:57:73:a8:41:f2:24:93:
                    b6:83:e3:f0:5e:e8:e5:9c:88:48:35:71:db:b3:f9:
                    57:f2
                Curve: P-256
        X509v3 extensions:
            X509v3 Key Usage: critical
                Digital Signature
            X509v3 Extended Key Usage:
                Client Authentication
            X509v3 Subject Key Identifier:
                5A:D1:D9:D8:1A:89:FE:0C:EE:F8:2A:F0:AF:36:40:EF:53:31:BB:73
            X509v3 Authority Key Identifier:
                29:11:5A:03:4A:C8:42:18:B4:03:39:88:25:10:67:ED:E3:28:7E:E7
            X509v3 Subject Alternative Name:
                Permanent Identifier: c17933d176922964
            X509v3 Step Provisioner:
                Type: ACME
                Name: acme-da
    Signature Algorithm: ECDSA-SHA256
         30:44:02:20:3a:fe:ed:d1:ea:7d:07:ca:d1:d1:2e:a7:24:ab:
         76:36:1f:5a:6a:8b:03:9a:ec:6f:8c:ef:30:ce:81:62:a0:cd:
         02:20:0b:e0:c5:de:13:6e:b5:b7:61:82:66:93:3c:92:34:d3:
         3b:27:73:6c:69:d7:b6:d7:d9:6b:e1:ea:28:19:7f:3d

I0917 14:11:38.817477 1359517 client.go:971] Intermediate Certificate: 
Certificate:
    Data:
        Version: 3 (0x2)
        Serial Number: 222106212658667457791856453932283118771 (0xa71819bf91ed93c38070ab54bcd4c4b3)
        Signature Algorithm: ECDSA-SHA256
        Issuer: O=mTLS ACME CA,CN=mTLS ACME CA Root CA
        Validity
            Not Before: Sep 16 16:50:04 2026 UTC
            Not After : Sep 13 16:50:04 2036 UTC
        Subject: O=mTLS ACME CA,CN=mTLS ACME CA Intermediate CA
        Subject Public Key Info:
            Public Key Algorithm: ECDSA
                Public-Key: (256 bit)
                X:
                    72:25:5c:f4:67:59:d3:1e:87:ac:bf:bc:04:f6:91:
                    65:7e:b8:8b:13:c6:12:db:f0:0a:7f:4d:ee:45:64:
                    58:28
                Y:
                    ac:37:b0:54:f5:14:04:1a:83:44:93:a9:19:6a:a1:
                    1c:7d:ee:c8:8e:6e:0d:76:2d:36:3b:25:74:ee:64:
                    b2:51
                Curve: P-256
        X509v3 extensions:
            X509v3 Key Usage: critical
                Certificate Sign, CRL Sign
            X509v3 Basic Constraints: critical
                CA:TRUE, pathlen:0
            X509v3 Subject Key Identifier:
                29:11:5A:03:4A:C8:42:18:B4:03:39:88:25:10:67:ED:E3:28:7E:E7
            X509v3 Authority Key Identifier:
                5D:6F:1A:11:16:1B:1A:C6:FD:D0:06:E5:0A:64:D9:FE:8E:B2:74:01
    Signature Algorithm: ECDSA-SHA256
         30:45:02:21:00:9a:2a:05:79:be:70:2f:40:84:93:a0:f4:bd:
         2c:e0:66:f6:dd:ab:e7:3d:e7:fc:ae:a4:e9:e1:06:50:3f:ae:
         3a:02:20:6f:17:bf:6c:0d:4c:a0:84:b4:6c:69:69:6e:8a:f1:
         b3:a6:ae:45:38:16:d6:30:cf:33:29:57:ec:f5:b9:18:de
```

#### Server Logs

The server output will just show it doing TPM remote attestation and issuing a cert

```log
$ go run attestation_server/attestaion_server.go     
     --ekrootCA swtpm/config/var/lib/swtpm-localca/issuercert.pem    \
     --expectedPCRMapSHA256=0:a0b5ff3383a1116bd7dc6df177c0c2d433b9ee1813ea958fa5d166a202cb2a85         --v=40 -alsologtostderr

I0917 14:11:24.333843 1359145 attestaion_server.go:968] Starting gRPC server on port :50051
I0917 14:11:35.542020 1359145 attestaion_server.go:158] ======= OfferEK ========
I0917 14:11:35.542761 1359145 attestaion_server.go:181] EK Certificate: 
Certificate:
    Data:
        Version: 3 (0x2)
        Serial Number: 1237 (0x04d5)
        Signature Algorithm: SHA256-RSA
        Issuer: CN=swtpm-localca
        Validity
            Not Before: Sep 2 13:15:23 2026 UTC
            Not After : Dec 31 23:59:59 9999 UTC
        Subject: CN=unknown
        Subject Public Key Info:
            Public Key Algorithm: RSA
                Public-Key: (2048 bit)
                Modulus:
                    c0:97:9e:ae:1d:72:07:d6:d8:6c:8f:d0:66:57:64:
                    0e:5d:1e:b1:55:1c:a2:87:50:02:8c:7c:b1:b1:84:
                    69:62:26:79:a1:fb:e5:7c:37:22:40:5e:81:e5:3e:
                    4a:65:c8:5a:5e:56:7b:2c:98:49:9f:71:df:ae:a9:
                    28:68:69:cc:40:10:8c:b9:c0:76:a2:3f:98:b5:3a:
                    98:d0:9d:1a:8d:a7:2f:8d:b1:99:d8:ae:c9:36:0e:
                    8d:c6:e4:11:b6:b8:d1:00:3a:96:f9:68:29:58:88:
                    aa:78:41:33:58:ef:9b:71:b9:c8:af:b1:74:90:ec:
                    92:dc:e0:df:6a:2f:c7:a2:a0:dd:a0:04:42:4d:8f:
                    18:19:68:ad:58:b6:1d:ad:59:a1:e6:9f:1e:cb:77:
                    0d:c7:18:c5:05:83:de:aa:10:a8:8a:05:e8:41:8f:
                    af:50:0d:0d:ad:6b:4e:94:f1:66:09:4a:3e:f3:41:
                    ee:cb:a4:35:16:7d:ae:92:22:be:e5:d0:86:ec:c5:
                    b3:36:77:60:f2:69:b5:fd:c8:e1:da:4f:f9:73:3e:
                    cd:df:cb:8b:39:d6:69:bb:f8:b1:31:93:84:77:e4:
                    70:9a:b4:99:3c:61:bc:f0:3a:b9:64:15:ab:ff:3b:
                    09:7a:88:00:cd:dc:03:b6:8b:c4:a2:7c:2e:d6:cb:
                    59
                Exponent: 65537 (0x10001)
        X509v3 extensions:
            X509v3 Extended Key Usage:
                EK Certificate
            X509v3 Subject Alternative Name: critical
                TPM Manufacturer: id:00001014
                TPM Model: swtpm
                TPM Version: id:20240125
            X509v3 Basic Constraints: critical
                CA:FALSE
            X509v3 Subject Directory Attributes:
                TPM Specification: Family: 2.0, Level: 0, Revision: 183
            X509v3 Authority Key Identifier:
                2F:6D:51:DB:77:36:EC:B9:2D:CD:E2:27:80:31:C8:B1:EC:C3:87:B4
            X509v3 Key Usage: critical
                Key Encipherment
    Signature Algorithm: SHA256-RSA
         3a:bb:38:d7:3b:31:2f:c4:55:7e:9f:3f:59:71:ba:d8:ec:44:
         6f:46:fc:f0:47:1d:ac:36:de:18:e2:99:ef:7c:de:c3:f7:80:
         9b:46:e3:2c:0f:74:85:cd:ef:93:c6:ba:de:7a:39:ef:d2:7b:
         2c:60:72:23:87:41:0d:fa:d4:7f:74:9d:8e:32:e4:d3:29:da:
         de:db:90:ef:fd:20:44:91:12:5d:ba:55:46:46:ae:df:71:27:
         0b:08:44:97:f2:db:0d:c4:2a:dc:ca:93:6a:a7:e6:49:b9:d9:
         e1:14:fc:c5:d3:ff:07:31:3a:95:8f:6a:c9:ad:48:32:9f:6d:
         78:73:f6:61:cd:ae:9b:fd:bd:de:65:f4:83:ef:f0:48:ba:73:
         92:9a:c5:c7:cc:51:03:2b:00:05:db:99:be:4d:3a:cc:24:a9:
         50:fb:43:47:ea:ca:d8:15:4d:c9:70:d7:7e:eb:f5:06:08:b0:
         ce:4f:cf:38:81:ee:b3:f2:04:7e:cf:b5:93:cb:ff:ba:ae:a4:
         51:37:e9:f5:52:2b:7c:52:b9:4c:f2:dd:af:45:62:03:06:71:
         2e:91:ef:90:58:c8:5b:4f:20:1b:bc:dd:e5:a7:95:aa:da:38:
         a1:75:5f:57:7f:f9:cc:48:2b:af:a3:fb:5c:65:08:43:cd:2f:
         18:76:e4:77:0d:41:b9:c1:26:71:69:1c:ce:4e:10:d6:fa:93:
         6a:85:80:cf:1f:31:76:0f:1e:15:d4:50:6a:ab:09:7e:ab:3a:
         03:6a:d5:42:07:ca:7e:98:12:f4:52:dc:28:f6:57:9c:2b:1a:
         a5:f1:9c:92:39:ac:fc:7e:7c:b8:f8:47:e9:dd:81:c1:b9:64:
         ee:3b:56:6c:52:69:64:01:5f:e0:d8:00:cb:98:e7:54:b3:7b:
         b5:34:ef:df:d4:20:af:34:63:80:c3:f3:b0:05:72:32:ef:50:
         12:b6:7f:bd:a0:ce:d1:30:8c:31:82:53:56:7b:7b:91:53:8e:
         4b:5b:45:b6:6a:7e

I0917 14:11:35.542879 1359145 attestaion_server.go:212]      TPM Manufacturer id:00001014
I0917 14:11:35.542896 1359145 attestaion_server.go:215]      TPM Model swtpm
I0917 14:11:35.542910 1359145 attestaion_server.go:219]      TPM Version id:20240125
I0917 14:11:35.542931 1359145 attestaion_server.go:251]      TPM Family 2.0
I0917 14:11:35.542948 1359145 attestaion_server.go:252]      TPM Level 0
I0917 14:11:35.542965 1359145 attestaion_server.go:253]      TPM Revision 183
I0917 14:11:35.542996 1359145 attestaion_server.go:268]         EKCertificate ========
-----BEGIN CERTIFICATE-----
MIID9TCCAl2gAwIBAgICBNUwDQYJKoZIhvcNAQELBQAwGDEWMBQGA1UEAxMNc3d0
cG0tbG9jYWxjYTAgFw0yNjA5MDIxMzE1MjNaGA85OTk5MTIzMTIzNTk1OVowEjEQ
MA4GA1UEAxMHdW5rbm93bjCCASIwDQYJKoZIhvcNAQEBBQADggEPADCCAQoCggEB
AMCXnq4dcgfW2GyP0GZXZA5dHrFVHKKHUAKMfLGxhGliJnmh++V8NyJAXoHlPkpl
yFpeVnssmEmfcd+uqShoacxAEIy5wHaiP5i1OpjQnRqNpy+NsZnYrsk2Do3G5BG2
uNEAOpb5aClYiKp4QTNY75txucivsXSQ7JLc4N9qL8eioN2gBEJNjxgZaK1Yth2t
WaHmnx7Ldw3HGMUFg96qEKiKBehBj69QDQ2ta06U8WYJSj7zQe7LpDUWfa6SIr7l
0IbsxbM2d2DyabX9yOHaT/lzPs3fy4s51mm7+LExk4R35HCatJk8YbzwOrlkFav/
Owl6iADN3AO2i8SifC7Wy1kCAwEAAaOBzDCByTAQBgNVHSUECTAHBgVngQUIATBS
BgNVHREBAf8ESDBGpEQwQjEWMBQGBWeBBQIBDAtpZDowMDAwMTAxNDEQMA4GBWeB
BQICDAVzd3RwbTEWMBQGBWeBBQIDDAtpZDoyMDI0MDEyNTAMBgNVHRMBAf8EAjAA
MCIGA1UdCQQbMBkwFwYFZ4EFAhAxDjAMDAMyLjACAQACAgC3MB8GA1UdIwQYMBaA
FC9tUdt3Nuy5Lc3iJ4AxyLHsw4e0MA4GA1UdDwEB/wQEAwIFIDANBgkqhkiG9w0B
AQsFAAOCAYEAOrs41zsxL8RVfp8/WXG62OxEb0b88EcdrDbeGOKZ73zew/eAm0bj
LA90hc3vk8a63no579J7LGByI4dBDfrUf3SdjjLk0yna3tuQ7/0gRJESXbpVRkau
33EnCwhEl/LbDcQq3MqTaqfmSbnZ4RT8xdP/BzE6lY9qya1IMp9teHP2Yc2um/29
3mX0g+/wSLpzkprFx8xRAysABduZvk06zCSpUPtDR+rK2BVNyXDXfuv1Bgiwzk/P
OIHus/IEfs+1k8v/uq6kUTfp9VIrfFK5TPLdr0ViAwZxLpHvkFjIW08gG7zd5aeV
qto4oXVfV3/5zEgrr6P7XGUIQ80vGHbkdw1BucEmcWkczk4Q1vqTaoWAzx8xdg8e
FdRQaqsJfqs6A2rVQgfKfpgS9FLcKPZXnCsapfGckjms/H58uPhH6d2Bwblk7jtW
bFJpZAFf4NgAy5jnVLN7tTTv39QgrzRjgMPzsAVyMu9QErZ/vaDO0TCMMYJTVnt7
kVOOS1tFtmp+
-----END CERTIFICATE-----

I0917 14:11:35.543072 1359145 attestaion_server.go:284]      EKCert  Issuer CN=swtpm-localca
I0917 14:11:35.543116 1359145 attestaion_server.go:285]      EKCert  IssuingCertificateURL []
I0917 14:11:35.543145 1359145 attestaion_server.go:286]      EKCert  SerialNumber 1237
I0917 14:11:35.543167 1359145 attestaion_server.go:288]     EkCert Public Key 
-----BEGIN PUBLIC KEY-----
MIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEAwJeerh1yB9bYbI/QZldk
Dl0esVUcoodQAox8sbGEaWImeaH75Xw3IkBegeU+SmXIWl5WeyyYSZ9x366pKGhp
zEAQjLnAdqI/mLU6mNCdGo2nL42xmdiuyTYOjcbkEba40QA6lvloKViIqnhBM1jv
m3G5yK+xdJDsktzg32ovx6Kg3aAEQk2PGBlorVi2Ha1ZoeafHst3DccYxQWD3qoQ
qIoF6EGPr1ANDa1rTpTxZglKPvNB7sukNRZ9rpIivuXQhuzFszZ3YPJptf3I4dpP
+XM+zd/LiznWabv4sTGThHfkcJq0mTxhvPA6uWQVq/87CXqIAM3cA7aLxKJ8LtbL
WQIDAQAB
-----END PUBLIC KEY-----

I0917 14:11:35.543195 1359145 attestaion_server.go:291]     Verifying EKCert
I0917 14:11:35.543389 1359145 attestaion_server.go:319]      EKCert Includes tcg-kp-EKCertificate ExtendedKeyUsage 2.23.133.8.1
I0917 14:11:35.543772 1359145 attestaion_server.go:348]     EKCert Verified
I0917 14:11:35.543798 1359145 attestaion_server.go:362] =============== end OfferEK ===============
I0917 14:11:35.641788 1359145 attestaion_server.go:367] ======= OfferAK ========
I0917 14:11:35.642078 1359145 attestaion_server.go:411]       ak public 
-----BEGIN PUBLIC KEY-----
MIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEAoN+ertupPI7VXMLkCg6H
iFnfi0onrr7y2Jh7FFQZNjVCH7s73t3EW2ONycfoarhaX4Ml2M8LEPfle1POg0eV
9LJe2hEPvggECikfqF0VCrcWV26FgZYzpsNHdgL8N1dmneaT5iG4qDAIghp2TNwU
xyz9wkbUrc1asoag4IwteFAiMVC2XM85satoSTt2aX0BIXRT7wdQCSfQBTtLP3A6
l80DtrqYjQIB6GoRxIXVX5O9fU+uY543avgFWBrREhmjxVgjLW/hZQD6OzCBS+w9
m/4cihVI/qJ/389inT3PC2XZFo5nPWg3ZGY1izuW3DLlWcawoZV51hL57TW1zAlr
9wIDAQAB
-----END PUBLIC KEY-----

I0917 14:11:35.642335 1359145 attestaion_server.go:425] =============== end GetAK ===============
I0917 14:11:35.643070 1359145 attestaion_server.go:431] ======= GetMakeCredential ========
I0917 14:11:35.643093 1359145 attestaion_server.go:448] =============== end GetMakeCredential ===============
I0917 14:11:35.643613 1359145 attestaion_server.go:462]       Outbound Secret: 21xqc8dTBMLi+kzJgLOxjElRcy6jYi7Y1TKIvz14Rxo=
I0917 14:11:35.685870 1359145 attestaion_server.go:480] ======= SetActivateCredential ========
I0917 14:11:35.685924 1359145 attestaion_server.go:513] =============== end SetActivateCredential ===============
I0917 14:11:35.687005 1359145 attestaion_server.go:518] ======= OfferQuote ========
I0917 14:11:35.687057 1359145 attestaion_server.go:543] =============== end OfferQuote ===============
I0917 14:11:35.697193 1359145 attestaion_server.go:550] ======= SetQuote ========
I0917 14:11:35.698824 1359145 attestaion_server.go:602]       quote-attested public 
-----BEGIN PUBLIC KEY-----
MIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEAoN+ertupPI7VXMLkCg6H
iFnfi0onrr7y2Jh7FFQZNjVCH7s73t3EW2ONycfoarhaX4Ml2M8LEPfle1POg0eV
9LJe2hEPvggECikfqF0VCrcWV26FgZYzpsNHdgL8N1dmneaT5iG4qDAIghp2TNwU
xyz9wkbUrc1asoag4IwteFAiMVC2XM85satoSTt2aX0BIXRT7wdQCSfQBTtLP3A6
l80DtrqYjQIB6GoRxIXVX5O9fU+uY543avgFWBrREhmjxVgjLW/hZQD6OzCBS+w9
m/4cihVI/qJ/389inT3PC2XZFo5nPWg3ZGY1izuW3DLlWcawoZV51hL57TW1zAlr
9wIDAQAB
-----END PUBLIC KEY-----

I0917 14:11:35.699118 1359145 attestaion_server.go:632]      PCR: 0, verified: true value: a0b5ff3383a1116bd7dc6df177c0c2d433b9ee1813ea958fa5d166a202cb2a85
I0917 14:11:35.699156 1359145 attestaion_server.go:632]      PCR: 1, verified: true value: e50edb964f66a7417954b1506f78a49d62062228ce84ee0b4e7e3b0e19b64a69
I0917 14:11:35.699167 1359145 attestaion_server.go:632]      PCR: 2, verified: true value: 3d458cfe55cc03ea1f443f1562beec8df51c75e14a9fcf9a7234a13f198e7969
I0917 14:11:35.699176 1359145 attestaion_server.go:632]      PCR: 3, verified: true value: 3d458cfe55cc03ea1f443f1562beec8df51c75e14a9fcf9a7234a13f198e7969
I0917 14:11:35.699185 1359145 attestaion_server.go:632]      PCR: 4, verified: true value: a3358453a5148b4e3f4b96b006ae1761a2ce4aea75f6a13e10eb3e0903dfd6e2
I0917 14:11:35.699193 1359145 attestaion_server.go:632]      PCR: 5, verified: true value: 098a2ae2d1aabed3e346b9fef96ec64056ea4043514672243bbf40b7d0972302
I0917 14:11:35.699201 1359145 attestaion_server.go:632]      PCR: 6, verified: true value: 3d458cfe55cc03ea1f443f1562beec8df51c75e14a9fcf9a7234a13f198e7969
I0917 14:11:35.699211 1359145 attestaion_server.go:632]      PCR: 7, verified: true value: 0a3f60cea411388b09eac782999f5e62246ab5469f9047eb508aa22c4dcd2237
I0917 14:11:35.699219 1359145 attestaion_server.go:632]      PCR: 8, verified: true value: a775d521739876ecde2c17d0e856c584ec513e8758d9199a3d5c735836ba0ebe
I0917 14:11:35.699232 1359145 attestaion_server.go:632]      PCR: 9, verified: true value: 4a7254a1740444f04ec61cf3f8eb8ffb5dae2069b44ad900e894b34a07626b36
I0917 14:11:35.699241 1359145 attestaion_server.go:632]      PCR: 10, verified: true value: 0000000000000000000000000000000000000000000000000000000000000000
I0917 14:11:35.699251 1359145 attestaion_server.go:632]      PCR: 11, verified: true value: 0000000000000000000000000000000000000000000000000000000000000000
I0917 14:11:35.699262 1359145 attestaion_server.go:632]      PCR: 12, verified: true value: 0000000000000000000000000000000000000000000000000000000000000000
I0917 14:11:35.699273 1359145 attestaion_server.go:632]      PCR: 13, verified: true value: 0000000000000000000000000000000000000000000000000000000000000000
I0917 14:11:35.699282 1359145 attestaion_server.go:632]      PCR: 14, verified: true value: 306f9d8b94f17d93dc6e7cf8f5c79d652eb4c6c4d13de2dddc24af416e13ecaf
I0917 14:11:35.699292 1359145 attestaion_server.go:632]      PCR: 15, verified: true value: 0000000000000000000000000000000000000000000000000000000000000000
I0917 14:11:35.699301 1359145 attestaion_server.go:632]      PCR: 16, verified: true value: 0000000000000000000000000000000000000000000000000000000000000000
I0917 14:11:35.699311 1359145 attestaion_server.go:632]      PCR: 17, verified: true value: ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff
I0917 14:11:35.699320 1359145 attestaion_server.go:632]      PCR: 18, verified: true value: ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff
I0917 14:11:35.699332 1359145 attestaion_server.go:632]      PCR: 19, verified: true value: ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff
I0917 14:11:35.699340 1359145 attestaion_server.go:632]      PCR: 20, verified: true value: ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff
I0917 14:11:35.699347 1359145 attestaion_server.go:632]      PCR: 21, verified: true value: ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff
I0917 14:11:35.699354 1359145 attestaion_server.go:632]      PCR: 22, verified: true value: ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff
I0917 14:11:35.699362 1359145 attestaion_server.go:632]      PCR: 23, verified: true value: 0000000000000000000000000000000000000000000000000000000000000000
I0917 14:11:35.699369 1359145 attestaion_server.go:644]      quotes verified
I0917 14:11:35.700680 1359145 attestaion_server.go:673]      secureBoot State enabled: [true]
I0917 14:11:35.701224 1359145 attestaion_server.go:735] >>>>>>>>  DeviceSerial Number [c17933d176922964]
I0917 14:11:35.701267 1359145 attestaion_server.go:737]       verify quote, PCRs and secureBootState
I0917 14:11:35.702516 1359145 attestaion_server.go:893] Issued AK Certificate: 
-----BEGIN CERTIFICATE-----
MIIDXTCCAwOgAwIBAgIFAMDYOUgwCgYIKoZIzj0EAwIwWzELMAkGA1UEBhMCVVMx
DzANBgNVBAoMBkdvb2dsZTEdMBsGA1UECwwUQXR0ZXN0YXRpb24gVmVyaWZpZXIx
HDAaBgNVBAMME0F0dGVzdGF0aW9uIFJvb3QgQ0EwHhcNMjYwOTE3MTgxMTM1WhcN
MjcwOTE3MTgxMTM1WjAAMIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEA
oN+ertupPI7VXMLkCg6HiFnfi0onrr7y2Jh7FFQZNjVCH7s73t3EW2ONycfoarha
X4Ml2M8LEPfle1POg0eV9LJe2hEPvggECikfqF0VCrcWV26FgZYzpsNHdgL8N1dm
neaT5iG4qDAIghp2TNwUxyz9wkbUrc1asoag4IwteFAiMVC2XM85satoSTt2aX0B
IXRT7wdQCSfQBTtLP3A6l80DtrqYjQIB6GoRxIXVX5O9fU+uY543avgFWBrREhmj
xVgjLW/hZQD6OzCBS+w9m/4cihVI/qJ/389inT3PC2XZFo5nPWg3ZGY1izuW3DLl
WcawoZV51hL57TW1zAlr9wIDAQABo4IBQjCCAT4wDgYDVR0PAQH/BAQDAgeAMBAG
A1UdJQQJMAcGBWeBBQgDMAwGA1UdEwEB/wQCMAAwHwYDVR0jBBgwFoAUUA0oLf1M
FqjzMvQhFZys3Xnv4jkwJwYDVR0gBCAwHjAIBgZngQULAQEwCAYGZ4EFCwECMAgG
BmeBBQsBAzCBwQYDVR0RBIG5MIG2oEwGCCsGAQUFBwgEoEAwPgYFZ4EFAQKENTAw
MDAxMDE0OjJmNmQ1MWRiNzczNmVjYjkyZGNkZTIyNzgwMzFjOGIxZWNjMzg3YjQ6
NGQ1oCAGCCsGAQUFBwgDoBQwEgwQYzE3OTMzZDE3NjkyMjk2NKREMEIxFjAUBgVn
gQUCARMLaWQ6MDAwMDEwMTQxEDAOBgVngQUCAhMFc3d0cG0xFjAUBgVngQUCAxML
aWQ6MjAyNDAxMjUwCgYIKoZIzj0EAwIDSAAwRQIhAP/yN+V/EvNdYuRv3Yf6DvI7
H5Ox5Qkb/A59hpq4q+H/AiBEVtqbgNwblkEdkx9RBTw8qu3iloExZBXfFLFAjCvM
eQ==
-----END CERTIFICATE-----

Certificate:
    Data:
        Version: 3 (0x2)
        Serial Number: 3235395912 (0xc0d83948)
        Signature Algorithm: ECDSA-SHA256
        Issuer: C=US,O=Google,OU=Attestation Verifier,CN=Attestation Root CA
        Validity
            Not Before: Sep 17 18:11:35 2026 UTC
            Not After : Sep 17 18:11:35 2027 UTC
        Subject:
        Subject Public Key Info:
            Public Key Algorithm: RSA
                Public-Key: (2048 bit)
                Modulus:
                    a0:df:9e:ae:db:a9:3c:8e:d5:5c:c2:e4:0a:0e:87:
                    88:59:df:8b:4a:27:ae:be:f2:d8:98:7b:14:54:19:
                    36:35:42:1f:bb:3b:de:dd:c4:5b:63:8d:c9:c7:e8:
                    6a:b8:5a:5f:83:25:d8:cf:0b:10:f7:e5:7b:53:ce:
                    83:47:95:f4:b2:5e:da:11:0f:be:08:04:0a:29:1f:
                    a8:5d:15:0a:b7:16:57:6e:85:81:96:33:a6:c3:47:
                    76:02:fc:37:57:66:9d:e6:93:e6:21:b8:a8:30:08:
                    82:1a:76:4c:dc:14:c7:2c:fd:c2:46:d4:ad:cd:5a:
                    b2:86:a0:e0:8c:2d:78:50:22:31:50:b6:5c:cf:39:
                    b1:ab:68:49:3b:76:69:7d:01:21:74:53:ef:07:50:
                    09:27:d0:05:3b:4b:3f:70:3a:97:cd:03:b6:ba:98:
                    8d:02:01:e8:6a:11:c4:85:d5:5f:93:bd:7d:4f:ae:
                    63:9e:37:6a:f8:05:58:1a:d1:12:19:a3:c5:58:23:
                    2d:6f:e1:65:00:fa:3b:30:81:4b:ec:3d:9b:fe:1c:
                    8a:15:48:fe:a2:7f:df:cf:62:9d:3d:cf:0b:65:d9:
                    16:8e:67:3d:68:37:64:66:35:8b:3b:96:dc:32:e5:
                    59:c6:b0:a1:95:79:d6:12:f9:ed:35:b5:cc:09:6b:
                    f7
                Exponent: 65537 (0x10001)
        X509v3 extensions:
            X509v3 Key Usage: critical
                Digital Signature
            X509v3 Extended Key Usage:
                2.23.133.8.3
            X509v3 Basic Constraints: critical
                CA:FALSE
            X509v3 Authority Key Identifier:
                50:0D:28:2D:FD:4C:16:A8:F3:32:F4:21:15:9C:AC:DD:79:EF:E2:39
            X509v3 Certificate Policies:
                Policy: 2.23.133.11.1.1
                Policy: 2.23.133.11.1.2
                Policy: 2.23.133.11.1.3
            X509v3 Subject Alternative Name:
                Hardware Module Name: Type: 2.23.133.1.2, Serial Number: 00001014:2f6d51db7736ecb92dcde2278031c8b1ecc387b4:4d5
                Permanent Identifier: c17933d176922964
                TPM Manufacturer: id:00001014
                TPM Model: swtpm
                TPM Version: id:20240125
    Signature Algorithm: ECDSA-SHA256
         30:45:02:21:00:ff:f2:37:e5:7f:12:f3:5d:62:e4:6f:dd:87:
         fa:0e:f2:3b:1f:93:b1:e5:09:1b:fc:0e:7d:86:9a:b8:ab:e1:
         ff:02:20:44:56:da:9b:80:dc:1b:96:41:1d:93:1f:51:05:3c:
         3c:aa:ed:e2:96:81:31:64:15:df:14:b1:40:8c:2b:cc:79

I0917 14:11:35.702645 1359145 attestaion_server.go:899] =============== Attestation x509 Sent ===============
```

Note the server generates and sets the permanent-identfier (`DeviceSerial Number [c17933d176922964]`) which gets encoded into the AK and eventually the issued x509

The step-ca logs also chronicles the provisioning flows

#### ACME Server

```bash
$ step-ca

INFO[0063]                                               duration="374.308µs" duration-ns=374308 fields.time="2026-09-17T14:11:35-04:00" method=GET name=ca path=/acme/acme-da/directory protocol=HTTP/1.1 referer= remote-address=127.0.0.1 request-id=1d09ff5d-fe88-494e-aced-0a26f4b373a8 response="{\"newNonce\":\"https://ca.domain.com:8443/acme/acme-da/new-nonce\",\"newAccount\":\"https://ca.domain.com:8443/acme/acme-da/new-account\",\"newOrder\":\"https://ca.domain.com:8443/acme/acme-da/new-order\",\"revokeCert\":\"https://ca.domain.com:8443/acme/acme-da/revoke-cert\",\"keyChange\":\"https://ca.domain.com:8443/acme/acme-da/key-change\"}" size=327 status=200 user-agent=golang.org/x/crypto/acme@v0.50.0 user-id=
INFO[0063]                                               duration=8.9186ms duration-ns=8918600 fields.time="2026-09-17T14:11:35-04:00" method=HEAD name=ca nonce=bzY2a0F4UWJTUW9yMVVNM21TTWpQbjBGenI4d3lnOGk path=/acme/acme-da/new-nonce protocol=HTTP/1.1 referer= remote-address=127.0.0.1 request-id=3b9ef684-5669-44ba-b8c3-8d962a660344 size=0 status=200 user-agent=golang.org/x/crypto/acme@v0.50.0 user-id=
INFO[0063]                                               duration=6.308908ms duration-ns=6308908 fields.time="2026-09-17T14:11:35-04:00" method=POST name=ca nonce=ck5JM0hJQlU5UGJSZ1V1aXBHbXRKNkszN29KcFFSaWk path=/acme/acme-da/new-account protocol=HTTP/1.1 referer= remote-address=127.0.0.1 request-id=6f7c29d8-2880-4c41-a8a4-3d3616148fd9 response="{\"contact\":[\"mailto:admin@example.local\"],\"status\":\"valid\",\"orders\":\"https://ca.domain.com:8443/acme/acme-da/account/aPRFJWLQZTuE5V6aArjhTliMKD3CB18f/orders\"}" size=159 status=201 user-agent=golang.org/x/crypto/acme@v0.50.0 user-id=
INFO[0063]                                               duration=10.36764ms duration-ns=10367640 fields.time="2026-09-17T14:11:35-04:00" method=POST name=ca nonce=U0NqdXgyd1dNdmpSTGtTU3lDU3d0Q0J5dWRtNnVjR3Q path=/acme/acme-da/new-order protocol=HTTP/1.1 referer= remote-address=127.0.0.1 request-id=acb1449a-5540-4433-931b-c4d01ba172e2 response="{\"id\":\"MVuKEeQUEoEdBKQ4OsUu7YYq0kilZEv8\",\"status\":\"pending\",\"expires\":\"2026-09-18T18:11:35Z\",\"identifiers\":[{\"type\":\"permanent-identifier\",\"value\":\"c17933d176922964\"}],\"notBefore\":\"2026-09-17T18:10:35Z\",\"notAfter\":\"2026-09-18T18:11:35Z\",\"authorizations\":[\"https://ca.domain.com:8443/acme/acme-da/authz/HHFZRcvLgMTNBiHRWgObuGEp72j5X1GD\"],\"finalize\":\"https://ca.domain.com:8443/acme/acme-da/order/MVuKEeQUEoEdBKQ4OsUu7YYq0kilZEv8/finalize\"}" size=439 status=201 user-agent=golang.org/x/crypto/acme@v0.50.0 user-id=
INFO[0063]                                               duration=4.300021ms duration-ns=4300021 fields.time="2026-09-17T14:11:35-04:00" method=POST name=ca nonce=Vk9ER3FpRjV0T01vM3hvdFlBYVpydW94WHF6V1NwR3c path=/acme/acme-da/authz/HHFZRcvLgMTNBiHRWgObuGEp72j5X1GD protocol=HTTP/1.1 referer= remote-address=127.0.0.1 request-id=37589c65-9e53-49ac-9cf8-aef30b8be573 response="{\"identifier\":{\"type\":\"permanent-identifier\",\"value\":\"c17933d176922964\"},\"status\":\"pending\",\"challenges\":[{\"type\":\"device-attest-01\",\"status\":\"pending\",\"token\":\"Zj81KIuk2YxowWclagoqCf3mC3sO9Uyz\",\"url\":\"https://ca.domain.com:8443/acme/acme-da/challenge/HHFZRcvLgMTNBiHRWgObuGEp72j5X1GD/eGVHvNRyCgHZn6PlcDSr2mIE09RvZzt5\"}],\"wildcard\":false,\"expires\":\"2026-09-18T18:11:35Z\"}" size=372 status=200 user-agent=golang.org/x/crypto/acme@v0.50.0 user-id=
INFO[0066]                                               duration=15.693747ms duration-ns=15693747 fields.time="2026-09-17T14:11:38-04:00" method=POST name=ca nonce=Zm5tQ3lhb3NFVDJjVVJIRmFaWjFaWVhVc3o1NlJraTc path=/acme/acme-da/challenge/HHFZRcvLgMTNBiHRWgObuGEp72j5X1GD/eGVHvNRyCgHZn6PlcDSr2mIE09RvZzt5 protocol=HTTP/1.1 referer= remote-address=127.0.0.1 request-id=67e33b4a-abda-47ac-b98a-ac7e6b1b0a26 response="{\"type\":\"device-attest-01\",\"status\":\"valid\",\"token\":\"Zj81KIuk2YxowWclagoqCf3mC3sO9Uyz\",\"validated\":\"2026-09-17T18:11:38Z\",\"url\":\"https://ca.domain.com:8443/acme/acme-da/challenge/HHFZRcvLgMTNBiHRWgObuGEp72j5X1GD/eGVHvNRyCgHZn6PlcDSr2mIE09RvZzt5\"}" size=247 status=200 user-agent=golang.org/x/crypto/acme@v0.50.0 user-id=
INFO[0066]                                               duration=8.201372ms duration-ns=8201372 fields.time="2026-09-17T14:11:38-04:00" method=POST name=ca nonce=ejRtaGs4Q0Rpb0QxYmtldEM3NGdSOEZoTkxPeEJwRFk path=/acme/acme-da/order/MVuKEeQUEoEdBKQ4OsUu7YYq0kilZEv8 protocol=HTTP/1.1 referer= remote-address=127.0.0.1 request-id=82d53570-6117-4637-8810-1552f9fa03af response="{\"id\":\"MVuKEeQUEoEdBKQ4OsUu7YYq0kilZEv8\",\"status\":\"ready\",\"expires\":\"2026-09-18T18:11:35Z\",\"identifiers\":[{\"type\":\"permanent-identifier\",\"value\":\"c17933d176922964\"}],\"notBefore\":\"2026-09-17T18:10:35Z\",\"notAfter\":\"2026-09-18T18:11:35Z\",\"authorizations\":[\"https://ca.domain.com:8443/acme/acme-da/authz/HHFZRcvLgMTNBiHRWgObuGEp72j5X1GD\"],\"finalize\":\"https://ca.domain.com:8443/acme/acme-da/order/MVuKEeQUEoEdBKQ4OsUu7YYq0kilZEv8/finalize\"}" size=437 status=200 user-agent=golang.org/x/crypto/acme@v0.50.0 user-id=
INFO[0066]                                               duration=12.662188ms duration-ns=12662188 fields.time="2026-09-17T14:11:38-04:00" method=POST name=ca nonce=ZUdPNzByOG1xeVVLcURYNFB0UnBYRzF3d1NiOGVhZnY path=/acme/acme-da/order/MVuKEeQUEoEdBKQ4OsUu7YYq0kilZEv8/finalize protocol=HTTP/1.1 referer= remote-address=127.0.0.1 request-id=bfaed9d9-1a70-4ba9-ac22-9fc18de58be6 response="{\"id\":\"MVuKEeQUEoEdBKQ4OsUu7YYq0kilZEv8\",\"status\":\"valid\",\"expires\":\"2026-09-18T18:11:35Z\",\"identifiers\":[{\"type\":\"permanent-identifier\",\"value\":\"c17933d176922964\"}],\"notBefore\":\"2026-09-17T18:10:35Z\",\"notAfter\":\"2026-09-18T18:11:35Z\",\"authorizations\":[\"https://ca.domain.com:8443/acme/acme-da/authz/HHFZRcvLgMTNBiHRWgObuGEp72j5X1GD\"],\"finalize\":\"https://ca.domain.com:8443/acme/acme-da/order/MVuKEeQUEoEdBKQ4OsUu7YYq0kilZEv8/finalize\",\"certificate\":\"https://ca.domain.com:8443/acme/acme-da/certificate/IFNYEvbR7WHjUhY0ffzMJBkADHtx37lG\"}" size=538 status=200 user-agent=golang.org/x/crypto/acme@v0.50.0 user-id=
INFO[0066]                                               certificate="MIICDjCCAbWgAwIBAgIQGUh1qZAh3uHMl1G60ws+szAKBggqhkjOPQQDAjA+MRUwEwYDVQQKEwxtVExTIEFDTUUgQ0ExJTAjBgNVBAMTHG1UTFMgQUNNRSBDQSBJbnRlcm1lZGlhdGUgQ0EwHhcNMjYwOTE3MTgxMDM1WhcNMjYwOTE4MTgxMTM1WjAbMRkwFwYDVQQDExBjMTc5MzNkMTc2OTIyOTY0MFkwEwYHKoZIzj0CAQYIKoZIzj0DAQcDQgAEUc2gOVkQ7Nrb8R/0ZRZGft5ZSlakRf47mRu9ze8ezHo7iJWP7cQPQFdzqEHyJJO2g+PwXujlnIhINXHbs/lX8qOBtzCBtDAOBgNVHQ8BAf8EBAMCB4AwEwYDVR0lBAwwCgYIKwYBBQUHAwIwHQYDVR0OBBYEFFrR2dgaif4M7vgq8K82QO9TMbtzMB8GA1UdIwQYMBaAFCkRWgNKyEIYtAM5iCUQZ+3jKH7nMCsGA1UdEQQkMCKgIAYIKwYBBQUHCAOgFDASDBBjMTc5MzNkMTc2OTIyOTY0MCAGDCsGAQQBgqRkxihAAQQQMA4CAQYEB2FjbWUtZGEEADAKBggqhkjOPQQDAgNHADBEAiA6/u3R6n0HytHRLqckq3Y2H1pqiwOa7G+M7zDOgWKgzQIgC+DF3hNutbdhgmaTPJI00zsnc2xp17bX2Wvh6igZfz0=" duration=4.091549ms duration-ns=4091549 fields.time="2026-09-17T14:11:38-04:00" issuer="mTLS ACME CA Intermediate CA" method=POST name=ca nonce=UERxNHBOeFp5S29hank0UFA5WWFVY3Z4eENLT3EyclI path=/acme/acme-da/certificate/IFNYEvbR7WHjUhY0ffzMJBkADHtx37lG protocol=HTTP/1.1 provisioner=acme-da public-key="ECDSA P-256" referer= remote-address=127.0.0.1 request-id=889a5dc4-b368-412e-9b42-d7bc235201be sans="map[]" serial=33606931744527285064901955453127966387 size=1474 status=200 subject=c17933d176922964 user-agent=golang.org/x/crypto/acme@v0.50.0 user-id= valid-from="2026-09-17T18:10:35Z" valid-to="2026-09-18T18:11:35Z"
```

### Testing

#### Start HTTPS mTLS Server

```bash
cd tpm/testing/server

$ go run server.go -stepCACertPath $HOME/.step/certs/intermediate_ca.crt -testTLSServerCert=../../certs/server.crt -testTLSServerKey=../../certs/server.key 

Starting mTLS Server
Starting Test TLS Server.. on port :18081

Server connected with client certificate Issuer CN=mTLS ACME CA Intermediate CA,O=mTLS ACME CA  <<<<<<<<<<<<
Server connected with client certificate Subject CN=dc14c7c890d58712                   <<<<<<<<<<<<<<<<<<
```


#### Go Client

There are two varients in golang which implement a required `crypto.Signer` interface needed for the TLS Connection from the client.  You can use either though the keyfile is generally easier for keys wihout complex policies.  For keys with [complex TPM policies](https://github.com/salrashid123/tpmsigner#keys-with-auth-policy), you could use [https://pkg.go.dev/github.com/salrashid123/tpmsigner](github.com/salrashid123/tpmsigner)


```bash
## using crypto.Signer from github.com/foxboron/go-tpm-keyfiles
cd tpm/testing/go_client/

## using crypto.Signer from github.com/salrashid123/tpmsigner
# cd tpm/testing/go_client_tpmsigner

$ go run main.go -issuedCertFile=../../certs/cert.pem -tlsTestServerCA=../../certs/tls-root-ca.crt -tpmKeyFilePEM=../../certs/tpmkey.pem

Using mTLS certificate to make mTLS call
client connected to server with cn CN=server.domain.com,OU=Enterprise,O=Google,C=US
client connected with server Issuer: CN=TLS Root CA,OU=Enterprise,O=Google,C=US 
client successfully verified server certificate.server Response: ok
```

#### Openssl Client

If you want to test the ouput TPM based client and server using openssl:

first you'll need the [openssl tpm2 provider](https://github.com/tpm2-software/tpm2-openssl) installed:

```bash
$ cd tpm/testing/openssl_client

export TPM2TOOLS_TCTI="swtpm:port=2321"
export TPM2OPENSSL_TCTI="swtpm:port=2321"
export TPM2TSSENGINE_TCTI="swtpm:port=2321"
export OPENSSL_MODULES=/usr/lib/x86_64-linux-gnu/ossl-modules/
export TPM2TOOLS_AUTOFLUSH=yes

tpm2_flushcontext -t && tpm2_flushcontext -s && tpm2_flushcontext -l

$ openssl list  -provider tpm2  -provider default  --providers
Providers:
  default
    name: OpenSSL Default Provider
    version: 3.6.3
    status: active
  tpm2
    name: TPM 2.0 Provider
    version: 1.3.0
    status: active

$ gcc main.c -lcrypto -lssl -o client

$ ./client 
Default Provider name: default
Custom Provider name: tpm2
Connected via TLS_AES_128_GCM_SHA256 encryption
HTTP/1.0 200 OK
Date: Tue, 08 Sep 2026 13:21:01 GMT
Content-Length: 3
Content-Type: text/plain; charset=utf-8

ok
```

The output you'll see on the server debug will demonstrate mTLS was used

Note that while i'd like to use `openssl s_client` in my tests instead of c client, since i'm using a `swtpm` which does not have a built-in resource manger, the s_client command will run into `out of memory for object contexts` error as such below.  If you're using a real TPM, this should work better.

```bash
# openssl s_client -connect localhost:18081 \
#        -provider tpm2  -provider default \
#        -servername server.domain.com \
#        -CAfile certs/tls-root-ca.crt \
#        -cert certs/cert.pem \
#        -key certs/tpmkey.pem \
#        -tls1_3 \
#        -tlsextdebug \
#        --verify 5 \
#        -trace
# 4057AE10D97F0000:error:4000000E:tpm2::cannot hash::-1:2306 tpm:warn(2.0): out of memory for object contexts
# 4057AE10D97F0000:error:0A00007B:SSL routines:tls_process_cert_verify:bad signature:../ssl/statem/statem_lib.c:591:
```

also see: [mTLS with TPM bound private key](https://github.com/salrashid123/go_tpm_https_embed#appendix) and [OpenSSL 3 docker with TLS trace enabled (enable-ssl-trace) and FIPS](https://github.com/salrashid123/openssl_trace)

Finally, you can use the tpm PEM key and openssl directly too:

```bash
export TPM2TOOLS_TCTI="swtpm:port=2321"
export TPM2OPENSSL_TCTI="swtpm:port=2321"
echo -n "foo" > /tmp/file.txt
openssl dgst  -provider tpm2 -provider default -sha256 -sign tpmkey.pem -out /tmp/signature.bin /tmp/file.txt
openssl ec -provider tpm2 -provider default  -in tpmkey.pem -pubout -out /tmp/tpmpub.pem
openssl dgst  -provider tpm2 -provider default -sha256 -verify /tmp/tpmpub.pem -signature /tmp/signature.bin /tmp/file.txt
```


#### Python Client

Python requests can use openssl as the backend and 'understands' the PEM format TPM key as well.

So if you setup openssl correctly, it should 'just work'

```python
import requests

response = requests.get('https://server.domain.com:18081/', verify='../../certs/tls-root-ca.crt', cert=('../../certs/cert.pem', '../../certs/tpmkey.pem'))

print("Status Code: %s" % response.status_code)
print(response.text)
```

in use, you have to startup the `openss s_server` from the previous section and use the certs provided by the ACME client

```bash
$ cd tpm/testing/python_client

export OPENSSL_CONF=`pwd`/openssl.cnf
export OPENSSL_MODULES=/usr/lib/x86_64-linux-gnu/ossl-modules/
export TPM2TOOLS_TCTI="swtpm:port=2321"
export TPM2OPENSSL_TCTI="swtpm:port=2321"
export TPM2TOOLS_AUTOFLUSH=yes

$ python3 main.py 
Status Code: 200
ok

```

Also see [Python mTLS client/server with TPM based key](https://gist.github.com/salrashid123/4cb714d800c9e8777dfbcd93ff076100)

## HTTP ACME

This repo also has a small demo of HTTP-01 ACME challenge protocol which i just threw in.

What this does is launches a client which conteacts the ACME CA and server and requests a certificate.

The client awaits the response back from ACME and when it gets a `token`, it launches a new HTTP server which the ACME server can contact and validate.

After validation, the acme server issues the client x509.

```bash
$ cd http/

$ sudo /apps/go/bin/go run main.go 

2026/09/02 15:01:58 Successfully registered ACME account.
2026/09/02 15:01:58 Order created. URI: https://ca.domain.com:8443/acme/myacme/order/M1kuYib0JUsG0Gk84PgmlWIX998Q0AtV
2026/09/02 15:01:58 Fulfill challenge token: GhxLzZEMzSOhhyB5aFjWfJyqalOWttxp
2026/09/02 15:01:58 Starting HTTP Server
2026/09/02 15:02:01 started server accepting challenge
2026/09/02 15:02:01 Got Acme challenge on HTTP server endpoint GhxLzZEMzSOhhyB5aFjWfJyqalOWttxp
2026/09/02 15:02:01 Acme responseBody GhxLzZEMzSOhhyB5aFjWfJyqalOWttxp.UPBNg0Sa0tNF4IucG6xk6Y5S-xMqx9h01QIsaXGTrRQ
2026/09/02 15:02:01 Waiting for order readiness validation...
--- HTTPS Server Private Key---
-----BEGIN EC PRIVATE KEY-----
MHcCAQEEIM9CWV4OI3eTFrXre1dXqoweJsDsGzp1oIKxxD+hsJ/GoAoGCCqGSM49
AwEHoUQDQgAE7+RJ17i5T3JNpZc4Cg1PDWIcWr8C0mYWKDQishj+RkCIxM94DAwg
fLfdynccKj6WtuR0IapxKPL8WEXXmGzKog==
-----END EC PRIVATE KEY-----
2026/09/02 15:02:01 Finalizing order with CSR...
2026/09/02 15:02:01 Certificate: 
Certificate:
    Data:
        Version: 3 (0x2)
        Serial Number: 147234251207323011223787919387797236628 (0x6ec4490afdbbcd8baaabf19411482b94)
        Signature Algorithm: ECDSA-SHA256
        Issuer: O=mTLS ACME CA,CN=mTLS ACME CA Intermediate CA
        Validity
            Not Before: Sep 2 19:00:58 2026 UTC
            Not After : Sep 3 19:01:58 2026 UTC
        Subject: CN=server.domain.com
        Subject Public Key Info:
            Public Key Algorithm: ECDSA
                Public-Key: (256 bit)
                X:
                    ef:e4:49:d7:b8:b9:4f:72:4d:a5:97:38:0a:0d:4f:
                    0d:62:1c:5a:bf:02:d2:66:16:28:34:22:b2:18:fe:
                    46:40
                Y:
                    88:c4:cf:78:0c:0c:20:7c:b7:dd:ca:77:1c:2a:3e:
                    96:b6:e4:74:21:aa:71:28:f2:fc:58:45:d7:98:6c:
                    ca:a2
                Curve: P-256
        X509v3 extensions:
            X509v3 Key Usage: critical
                Digital Signature
            X509v3 Extended Key Usage:
                Server Authentication, Client Authentication
            X509v3 Subject Key Identifier:
                61:51:57:62:D9:67:D1:C8:34:74:DA:19:E9:7E:68:FE:AB:CA:94:5A
            X509v3 Authority Key Identifier:
                04:E5:5E:AB:D3:45:18:D8:5A:B2:71:AD:9E:C1:71:C5:0D:E7:8A:5D
            X509v3 Subject Alternative Name:
                DNS:server.domain.com
            X509v3 Step Provisioner:
                Type: ACME
                Name: myacme
    Signature Algorithm: ECDSA-SHA256
         30:45:02:20:7b:f2:e1:3b:a8:d3:33:3f:0e:09:9f:56:78:b0:
         fd:fd:d1:0a:b3:9f:35:4f:b7:a8:41:80:5a:be:18:61:a7:94:
         02:21:00:cb:7b:ff:d3:55:bb:b6:d5:2e:ef:07:08:45:1c:2f:
         71:8f:de:7c:3d:76:a3:87:75:32:58:aa:73:a8:0d:27:bd

2026/09/02 15:02:01 Certificate: 
Certificate:
    Data:
        Version: 3 (0x2)
        Serial Number: 311750628441962997850387629041105283503 (0xea88fcaf4b8e48c34f6b1af09cdca9af)
        Signature Algorithm: ECDSA-SHA256
        Issuer: O=mTLS ACME CA,CN=mTLS ACME CA Root CA
        Validity
            Not Before: Sep 2 18:55:39 2026 UTC
            Not After : Aug 30 18:55:39 2036 UTC
        Subject: O=mTLS ACME CA,CN=mTLS ACME CA Intermediate CA
        Subject Public Key Info:
            Public Key Algorithm: ECDSA
                Public-Key: (256 bit)
                X:
                    98:6b:f0:1c:4d:3a:b9:97:5f:05:f7:ca:3d:71:09:
                    e0:82:94:18:d9:8f:6d:8c:75:b0:2c:8c:01:ee:13:
                    6b:73
                Y:
                    f2:51:99:04:88:ba:a8:61:b2:29:fd:1c:3a:48:f5:
                    d0:c8:2a:99:31:32:56:97:59:ea:f9:ef:6e:63:90:
                    84:27
                Curve: P-256
        X509v3 extensions:
            X509v3 Key Usage: critical
                Certificate Sign, CRL Sign
            X509v3 Basic Constraints: critical
                CA:TRUE, pathlen:0
            X509v3 Subject Key Identifier:
                04:E5:5E:AB:D3:45:18:D8:5A:B2:71:AD:9E:C1:71:C5:0D:E7:8A:5D
            X509v3 Authority Key Identifier:
                FC:0B:9F:E7:87:AC:1B:B0:52:47:E5:BA:8B:E2:FE:D0:6B:A0:0C:1D
    Signature Algorithm: ECDSA-SHA256
         30:45:02:20:26:2a:d9:44:46:04:da:dd:51:8d:ab:d4:40:ad:
         98:b8:40:7e:f2:b0:2c:ae:e3:15:77:a2:49:20:d0:ff:d0:19:
         02:21:00:b7:d6:ba:ec:96:90:d3:be:8f:05:da:9e:88:35:3e:
         7b:3b:7d:0d:45:b4:ce:9f:b7:a2:bc:3c:a0:a6:c1:e7:3e
```


#### References

- [TPM 2.0 Keys for Device Identity and Attestation](https://trustedcomputinggroup.org/wp-content/uploads/TPM-2p0-Keys-for-Device-Identity-and-Attestation_v1_r12_pub10082021.pdf)
- [TCG EK Credential Profile](https://trustedcomputinggroup.org/wp-content/uploads/TCG-EK-Credential-Profile-for-TPM-Family-2.0-Level-0-Version-2.7_Pub.pdf)
- [Smallstep: Run your own private CA & ACME server using step-ca](https://smallstep.com/blog/private-acme-server/)
- [Attestation Identity Key (AIK) Certificate Enrollment Specification FAQ](https://trustedcomputinggroup.org/wp-content/uploads/IWG-AIK-CMC-enrollment-FAQ.pdf)
- [mTLS with TPM bound private key](https://github.com/salrashid123/go_tpm_https_embed)
- [TPM Remote Attestation protocol using go-tpm and gRPC](https://github.com/salrashid123/go_tpm_remote_attestation)
- [TPM based TLS using Attested Keys](https://github.com/salrashid123/tls_ak)
- [Trusted Platform Module (TPM) recipes with tpm2_tools and go-tpm](https://github.com/salrashid123/tpm2)
- [Web Authentication: TPM Attestation](https://www.w3.org/TR/webauthn-2/#sctn-tpm-attestation)
