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
    - [Session Encryption](#session-encryption)
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

I0916 09:17:55.125882  537343 client.go:130] Opening swtpm socket
I0916 09:17:55.127701  537343 client.go:172] Manufacturer: IBM
I0916 09:17:55.127784  537343 client.go:173] VendorInfo: SW   TPM
I0916 09:17:55.127805  537343 client.go:174] FirmwareVersionMajor: 8228
I0916 09:17:55.127824  537343 client.go:175] FirmwareVersionMinor: 293
I0916 09:17:55.129369  537343 client.go:185] EKCert Issuer: CN=swtpm-localca
I0916 09:17:55.129422  537343 client.go:202] EKCert SerialNumber: 1237
I0916 09:17:55.129452  537343 client.go:206] =============== OfferEK ===============
I0916 09:17:55.139840  537343 client.go:215] Verified EK Cert
I0916 09:17:55.139904  537343 client.go:217] =============== OfferAK ===============
I0916 09:17:55.278406  537343 client.go:241] Creating AK CSR
I0916 09:17:55.285132  537343 client.go:273] AK CSR 
-----BEGIN CERTIFICATE REQUEST-----
MIIClDCCAXwCAQAwHjEcMBoGA1UEAxMTYXR0ZXN0b3IuZG9tYWluLmNvbTCCASIw
DQYJKoZIhvcNAQEBBQADggEPADCCAQoCggEBALLAX1uPrLKKNXuxOuLIx9+2fhzz
t4+LFWx3WNq/g7DK8tu5YhPjszRbZVgijzK2+u8DZZR18pYNdrcA6Br2nVxDofvy
K8yw+LE65El2EdpVDJrTbNMpECOyV14zmarwgpFn4fLdwrEwSodB4TU11HSkr6I2
7Wf/qq3wz7CjTmknkUFd6nzF5GUYNxOqYZ31eamli4DuCMonnAE9H5eQ29Z9yoUW
TOEOucBlLDOF8JwFWNfAMk5tNXUGpOhmU/vXa3Qi0Ru94a0SfqrRuq0t7AoQrLLq
cWPDZXsQYaq+6FuVnHbfeshEcUBtbtOMiJzU7MLrBmxmTCrKIHdCOqqbyH8CAwEA
AaAxMC8GCSqGSIb3DQEJDjEiMCAwHgYDVR0RBBcwFYITYXR0ZXN0b3IuZG9tYWlu
LmNvbTANBgkqhkiG9w0BAQsFAAOCAQEADsWN7KHisc58XgZ/XPKk1aN6kjC00uT8
VwKk4Oxw+sRpCl3TnOFO5h4vE+7J3ZU9LfNTUZO4GpySFFA7Vo4yFuhX56FnMC42
e5o7PLoYdUy9MVZbLNT5/lbnUVW8vmNqYGa+TmNvfnqP05cMruxhV/OeXXcrfRpI
DPwBCq5WNqd6rFKRqlYUrfBB0fLIidbRKIUKzR2Qe6uVbx9gd3oj50v8arRvWBZL
M529xNB6lG1Xvf76JoFNApzRnKMFQbyCRXaqzFRAqOGIcQmqjuEwL8jdxQgQxP7V
DEUMLBRhEhTHtdVx/E0dbC/Mj8dRrLFH2QDHbmd9r3nXLKbbyiLdCQ==
-----END CERTIFICATE REQUEST-----

I0916 09:17:55.286703  537343 client.go:284] Verified AK 
I0916 09:17:55.286751  537343 client.go:286] =============== GetMakeCredential ===============
I0916 09:17:55.319578  537343 client.go:315] EncryptedCredentials Secret hkxwmm8ffTLRBSfnS2UMKJlPR8BkrKB8hW4x5+uI63E=
I0916 09:17:55.319651  537343 client.go:317] =============== SetActivateCredential ===============
I0916 09:17:55.320605  537343 client.go:326] SetActivateCredential complete 
I0916 09:17:55.320670  537343 client.go:328] =============== OfferQuote ===============
I0916 09:17:55.321384  537343 client.go:335] OfferQuote complete 
I0916 09:17:55.321451  537343 client.go:337] =============== SetQuote ===============
I0916 09:17:55.337583  537343 client.go:381] Issued AK Certificate: 
-----BEGIN CERTIFICATE-----
MIIDWzCCAwKgAwIBAgIEHsAKjzAKBggqhkjOPQQDAjBbMQswCQYDVQQGEwJVUzEP
MA0GA1UECgwGR29vZ2xlMR0wGwYDVQQLDBRBdHRlc3RhdGlvbiBWZXJpZmllcjEc
MBoGA1UEAwwTQXR0ZXN0YXRpb24gUm9vdCBDQTAeFw0yNjA5MTYxMzE3NTVaFw0y
NzA5MTYxMzE3NTVaMAAwggEiMA0GCSqGSIb3DQEBAQUAA4IBDwAwggEKAoIBAQCy
wF9bj6yyijV7sTriyMfftn4c87ePixVsd1jav4OwyvLbuWIT47M0W2VYIo8ytvrv
A2WUdfKWDXa3AOga9p1cQ6H78ivMsPixOuRJdhHaVQya02zTKRAjsldeM5mq8IKR
Z+Hy3cKxMEqHQeE1NdR0pK+iNu1n/6qt8M+wo05pJ5FBXep8xeRlGDcTqmGd9Xmp
pYuA7gjKJ5wBPR+XkNvWfcqFFkzhDrnAZSwzhfCcBVjXwDJObTV1BqToZlP712t0
ItEbveGtEn6q0bqtLewKEKyy6nFjw2V7EGGqvuhblZx233rIRHFAbW7TjIic1OzC
6wZsZkwqyiB3Qjqqm8h/AgMBAAGjggFCMIIBPjAOBgNVHQ8BAf8EBAMCB4AwEAYD
VR0lBAkwBwYFZ4EFCAMwDAYDVR0TAQH/BAIwADAfBgNVHSMEGDAWgBRQDSgt/UwW
qPMy9CEVnKzdee/iOTAnBgNVHSAEIDAeMAgGBmeBBQsBATAIBgZngQULAQIwCAYG
Z4EFCwEDMIHBBgNVHREEgbkwgbagTAYIKwYBBQUHCASgQDA+BgVngQUBAoQ1MDAw
MDEwMTQ6MmY2ZDUxZGI3NzM2ZWNiOTJkY2RlMjI3ODAzMWM4YjFlY2MzODdiNDo0
ZDWgIAYIKwYBBQUHCAOgFDASDBBhOGQyN2ZjMDdhODc0NmUzpEQwQjEWMBQGBWeB
BQIBEwtpZDowMDAwMTAxNDEQMA4GBWeBBQICEwVzd3RwbTEWMBQGBWeBBQIDEwtp
ZDoyMDI0MDEyNTAKBggqhkjOPQQDAgNHADBEAiB5FdEDvlP6AQbWgB7aGQTKRx42
cj0UpBlD0dpxWnhZ6AIgGzeJpiuOXOEJvZZg1hX/DS2X71Bp/bU4PpO2GN/5Dro=
-----END CERTIFICATE-----

Certificate:
    Data:
        Version: 3 (0x2)
        Serial Number: 515902095 (0x1ec00a8f)
        Signature Algorithm: ECDSA-SHA256
        Issuer: C=US,O=Google,OU=Attestation Verifier,CN=Attestation Root CA
        Validity
            Not Before: Sep 16 13:17:55 2026 UTC
            Not After : Sep 16 13:17:55 2027 UTC
        Subject:
        Subject Public Key Info:
            Public Key Algorithm: RSA
                Public-Key: (2048 bit)
                Modulus:
                    b2:c0:5f:5b:8f:ac:b2:8a:35:7b:b1:3a:e2:c8:c7:
                    df:b6:7e:1c:f3:b7:8f:8b:15:6c:77:58:da:bf:83:
                    b0:ca:f2:db:b9:62:13:e3:b3:34:5b:65:58:22:8f:
                    32:b6:fa:ef:03:65:94:75:f2:96:0d:76:b7:00:e8:
                    1a:f6:9d:5c:43:a1:fb:f2:2b:cc:b0:f8:b1:3a:e4:
                    49:76:11:da:55:0c:9a:d3:6c:d3:29:10:23:b2:57:
                    5e:33:99:aa:f0:82:91:67:e1:f2:dd:c2:b1:30:4a:
                    87:41:e1:35:35:d4:74:a4:af:a2:36:ed:67:ff:aa:
                    ad:f0:cf:b0:a3:4e:69:27:91:41:5d:ea:7c:c5:e4:
                    65:18:37:13:aa:61:9d:f5:79:a9:a5:8b:80:ee:08:
                    ca:27:9c:01:3d:1f:97:90:db:d6:7d:ca:85:16:4c:
                    e1:0e:b9:c0:65:2c:33:85:f0:9c:05:58:d7:c0:32:
                    4e:6d:35:75:06:a4:e8:66:53:fb:d7:6b:74:22:d1:
                    1b:bd:e1:ad:12:7e:aa:d1:ba:ad:2d:ec:0a:10:ac:
                    b2:ea:71:63:c3:65:7b:10:61:aa:be:e8:5b:95:9c:
                    76:df:7a:c8:44:71:40:6d:6e:d3:8c:88:9c:d4:ec:
                    c2:eb:06:6c:66:4c:2a:ca:20:77:42:3a:aa:9b:c8:
                    7f
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
                Permanent Identifier: a8d27fc07a8746e3
                TPM Manufacturer: id:00001014
                TPM Model: swtpm
                TPM Version: id:20240125
    Signature Algorithm: ECDSA-SHA256
         30:44:02:20:79:15:d1:03:be:53:fa:01:06:d6:80:1e:da:19:
         04:ca:47:1e:36:72:3d:14:a4:19:43:d1:da:71:5a:78:59:e8:
         02:20:1b:37:89:a6:2b:8e:5c:e1:09:bd:96:60:d6:15:ff:0d:
         2d:97:ef:50:69:fd:b5:38:3e:93:b6:18:df:f9:0e:ba

I0916 09:17:55.337756  537343 client.go:383] =============== Create new Key ===============
I0916 09:17:55.337815  537343 client.go:398] Extracted Permanent Identfier: a8d27fc07a8746e3
I0916 09:17:55.337870  537343 client.go:412] Extracted HardwareSerialNumber: 00001014:2f6d51db7736ecb92dcde2278031c8b1ecc387b4:4d5
I0916 09:17:55.337919  537343 client.go:414]      Starting ACME Key generation
I0916 09:17:55.363713  537343 client.go:461] Successfully registered ACME account.
I0916 09:17:55.373542  537343 client.go:470] Order created. URI: https://ca.domain.com:8443/acme/acme-da/order/DgkFoAQwZySe1zJxdE4RTFA4o3LslMqE
I0916 09:17:55.378761  537343 client.go:495] Fulfill challenge token: TVzfVinuwVz7vmkcJzOItLkCettROVgP
I0916 09:17:55.378865  537343 client.go:497] =============== Create New Key and set challengToken ===============
I0916 09:17:55.378937  537343 client.go:508] KEYAUTH: TVzfVinuwVz7vmkcJzOItLkCettROVgP.GiNdiNewz-QdyhvjUgScgv8cPOidwP9LtTxvusuJmfM
I0916 09:17:55.379000  537343 client.go:510] Create a TPM based key
I0916 09:17:55.393433  537343 client.go:582] Generated ECC Public 
-----BEGIN PUBLIC KEY-----
MFkwEwYHKoZIzj0CAQYIKoZIzj0DAQcDQgAEOgtd9oAGAYwAcGE+y+QD8aI5pKdw
qQr/iZ4DQVqbVnzDunXaMuy2QV9Qu+xH9VnO+DkZ1HcnO/nDv04p7+/vqA==
-----END PUBLIC KEY-----
I0916 09:17:58.394349  537343 client.go:639] started server accepting challenge
I0916 09:17:58.411496  537343 client.go:652] Waiting for order readiness validation...
I0916 09:17:58.422185  537343 client.go:706] Finalizing order with CSR...
I0916 09:17:58.442700  537343 client.go:794] Acme Root Certificate: 
Certificate:
    Data:
        Version: 3 (0x2)
        Serial Number: 296490927670647207643159686201536405872 (0xdf0e1359792d6147f9fef4d860a7e170)
        Signature Algorithm: ECDSA-SHA256
        Issuer: O=mTLS ACME CA,CN=mTLS ACME CA Root CA
        Validity
            Not Before: Sep 16 13:15:31 2026 UTC
            Not After : Sep 13 13:15:31 2036 UTC
        Subject: O=mTLS ACME CA,CN=mTLS ACME CA Root CA
        Subject Public Key Info:
            Public Key Algorithm: ECDSA
                Public-Key: (256 bit)
                X:
                    35:4b:38:4f:c0:7b:a1:e5:01:d2:f5:fa:d5:54:60:
                    40:b5:3e:03:cb:3d:d9:02:d5:ba:66:a6:68:7b:88:
                    45:b5
                Y:
                    ce:e1:62:ca:8b:a3:5c:db:f6:e0:8d:d9:13:c4:99:
                    f8:0a:07:85:29:dc:f9:30:66:44:6b:2c:f4:2c:03:
                    1a:51
                Curve: P-256
        X509v3 extensions:
            X509v3 Key Usage: critical
                Certificate Sign, CRL Sign
            X509v3 Basic Constraints: critical
                CA:TRUE, pathlen:1
            X509v3 Subject Key Identifier:
                1D:1B:85:8F:25:49:EB:3F:DE:45:B4:F4:F7:14:27:61:90:63:FE:7E
    Signature Algorithm: ECDSA-SHA256
         30:44:02:20:14:57:3c:ef:fd:21:c2:5e:f8:82:2c:f2:bf:55:
         6b:14:5e:36:b4:a2:5f:27:31:7b:23:b6:90:67:7d:3a:f9:ff:
         02:20:26:33:18:8f:ca:52:41:a9:b1:ec:c8:6d:d6:46:6f:4a:
         53:38:d2:86:67:bf:d1:13:d3:38:2e:7f:16:97:3a:bf

I0916 09:17:58.443198  537343 client.go:818] Issued Certificate: 
Certificate:
    Data:
        Version: 3 (0x2)
        Serial Number: 205356945151140249204738093954478977559 (0x9a7e4f1a535ab29e79a0c67890e53217)
        Signature Algorithm: ECDSA-SHA256
        Issuer: O=mTLS ACME CA,CN=mTLS ACME CA Intermediate CA
        Validity
            Not Before: Sep 16 13:16:55 2026 UTC
            Not After : Sep 17 13:17:55 2026 UTC
        Subject: CN=a8d27fc07a8746e3
        Subject Public Key Info:
            Public Key Algorithm: ECDSA
                Public-Key: (256 bit)
                X:
                    3a:0b:5d:f6:80:06:01:8c:00:70:61:3e:cb:e4:03:
                    f1:a2:39:a4:a7:70:a9:0a:ff:89:9e:03:41:5a:9b:
                    56:7c
                Y:
                    c3:ba:75:da:32:ec:b6:41:5f:50:bb:ec:47:f5:59:
                    ce:f8:39:19:d4:77:27:3b:f9:c3:bf:4e:29:ef:ef:
                    ef:a8
                Curve: P-256
        X509v3 extensions:
            X509v3 Key Usage: critical
                Digital Signature
            X509v3 Extended Key Usage:
                Client Authentication
            X509v3 Subject Key Identifier:
                BB:AA:FD:68:72:08:6F:EE:66:78:B9:F1:39:B3:23:C8:7C:B1:64:AA
            X509v3 Authority Key Identifier:
                B8:8E:ED:DF:A5:A4:8C:F2:CE:20:5D:6E:72:95:EB:A8:31:DF:36:BC
            X509v3 Subject Alternative Name:
                Permanent Identifier: a8d27fc07a8746e3
            X509v3 Step Provisioner:
                Type: ACME
                Name: acme-da
    Signature Algorithm: ECDSA-SHA256
         30:45:02:20:48:23:ea:1a:1a:27:59:ac:51:81:9b:11:55:42:
         2a:ca:97:1c:fe:69:5a:ba:08:30:6f:04:57:fa:c5:c1:ba:18:
         02:21:00:c5:fe:11:92:dd:40:28:f1:9c:75:62:38:7d:a0:5f:
         4f:9b:17:ac:95:cf:d8:c2:72:e5:8e:25:b9:49:82:4f:56

I0916 09:17:58.443575  537343 client.go:815] Intermediate Certificate: 
Certificate:
    Data:
        Version: 3 (0x2)
        Serial Number: 7567042061018828653366086340575493523 (0x05b15bfa66d01467974eaa1170b3fd93)
        Signature Algorithm: ECDSA-SHA256
        Issuer: O=mTLS ACME CA,CN=mTLS ACME CA Root CA
        Validity
            Not Before: Sep 16 13:15:32 2026 UTC
            Not After : Sep 13 13:15:32 2036 UTC
        Subject: O=mTLS ACME CA,CN=mTLS ACME CA Intermediate CA
        Subject Public Key Info:
            Public Key Algorithm: ECDSA
                Public-Key: (256 bit)
                X:
                    cd:eb:bd:6f:62:f8:af:75:32:22:6d:ae:58:58:ce:
                    2f:b0:99:50:34:23:87:5e:ee:bc:82:7b:58:9b:5a:
                    ed:2f
                Y:
                    a3:04:18:5d:e0:40:92:60:11:d4:93:e2:62:60:ae:
                    eb:a3:aa:e5:8c:bb:d0:c8:91:99:d4:fc:1f:df:0d:
                    b8:c1
                Curve: P-256
        X509v3 extensions:
            X509v3 Key Usage: critical
                Certificate Sign, CRL Sign
            X509v3 Basic Constraints: critical
                CA:TRUE, pathlen:0
            X509v3 Subject Key Identifier:
                B8:8E:ED:DF:A5:A4:8C:F2:CE:20:5D:6E:72:95:EB:A8:31:DF:36:BC
            X509v3 Authority Key Identifier:
                1D:1B:85:8F:25:49:EB:3F:DE:45:B4:F4:F7:14:27:61:90:63:FE:7E
    Signature Algorithm: ECDSA-SHA256
         30:45:02:21:00:db:22:64:bf:7d:ce:e2:8d:8f:58:6a:66:44:
         1e:db:8b:7a:ed:d3:85:9b:f3:60:bf:de:73:2b:a7:fb:46:44:
         2d:02:20:46:80:a5:a7:b4:16:99:8e:4d:3e:06:4a:4e:2b:44:
         70:28:c4:1f:af:06:f9:30:40:68:89:c1:a9:5f:b7:b9:de
```

#### Server Logs

The server output will just show it doing TPM remote attestation and issuing a cert

```log
$ go run attestation_server/attestaion_server.go     
     --ekrootCA swtpm/config/var/lib/swtpm-localca/issuercert.pem    \
     --expectedPCRMapSHA256=0:a0b5ff3383a1116bd7dc6df177c0c2d433b9ee1813ea958fa5d166a202cb2a85         --v=40 -alsologtostderr

I0916 09:17:55.137793  536807 attestaion_server.go:158] ======= OfferEK ========
I0916 09:17:55.138341  536807 attestaion_server.go:181] EK Certificate: 
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

I0916 09:17:55.138431  536807 attestaion_server.go:212]      TPM Manufacturer id:00001014
I0916 09:17:55.138447  536807 attestaion_server.go:215]      TPM Model swtpm
I0916 09:17:55.138463  536807 attestaion_server.go:219]      TPM Version id:20240125
I0916 09:17:55.138483  536807 attestaion_server.go:251]      TPM Family 2.0
I0916 09:17:55.138501  536807 attestaion_server.go:252]      TPM Level 0
I0916 09:17:55.138517  536807 attestaion_server.go:253]      TPM Revision 183
I0916 09:17:55.138548  536807 attestaion_server.go:268]         EKCertificate ========
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

I0916 09:17:55.138635  536807 attestaion_server.go:284]      EKCert  Issuer CN=swtpm-localca
I0916 09:17:55.138676  536807 attestaion_server.go:285]      EKCert  IssuingCertificateURL []
I0916 09:17:55.138700  536807 attestaion_server.go:286]      EKCert  SerialNumber 1237
I0916 09:17:55.138722  536807 attestaion_server.go:288]     EkCert Public Key 
-----BEGIN PUBLIC KEY-----
MIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEAwJeerh1yB9bYbI/QZldk
Dl0esVUcoodQAox8sbGEaWImeaH75Xw3IkBegeU+SmXIWl5WeyyYSZ9x366pKGhp
zEAQjLnAdqI/mLU6mNCdGo2nL42xmdiuyTYOjcbkEba40QA6lvloKViIqnhBM1jv
m3G5yK+xdJDsktzg32ovx6Kg3aAEQk2PGBlorVi2Ha1ZoeafHst3DccYxQWD3qoQ
qIoF6EGPr1ANDa1rTpTxZglKPvNB7sukNRZ9rpIivuXQhuzFszZ3YPJptf3I4dpP
+XM+zd/LiznWabv4sTGThHfkcJq0mTxhvPA6uWQVq/87CXqIAM3cA7aLxKJ8LtbL
WQIDAQAB
-----END PUBLIC KEY-----

I0916 09:17:55.138756  536807 attestaion_server.go:291]     Verifying EKCert
I0916 09:17:55.138927  536807 attestaion_server.go:319]      EKCert Includes tcg-kp-EKCertificate ExtendedKeyUsage 2.23.133.8.1
I0916 09:17:55.139359  536807 attestaion_server.go:348]     EKCert Verified
I0916 09:17:55.139387  536807 attestaion_server.go:362] =============== end OfferEK ===============
I0916 09:17:55.285936  536807 attestaion_server.go:367] ======= OfferAK ========
I0916 09:17:55.286222  536807 attestaion_server.go:411]       ak public 
-----BEGIN PUBLIC KEY-----
MIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEAssBfW4+ssoo1e7E64sjH
37Z+HPO3j4sVbHdY2r+DsMry27liE+OzNFtlWCKPMrb67wNllHXylg12twDoGvad
XEOh+/IrzLD4sTrkSXYR2lUMmtNs0ykQI7JXXjOZqvCCkWfh8t3CsTBKh0HhNTXU
dKSvojbtZ/+qrfDPsKNOaSeRQV3qfMXkZRg3E6phnfV5qaWLgO4IyiecAT0fl5Db
1n3KhRZM4Q65wGUsM4XwnAVY18AyTm01dQak6GZT+9drdCLRG73hrRJ+qtG6rS3s
ChCssupxY8NlexBhqr7oW5Wcdt96yERxQG1u04yInNTswusGbGZMKsogd0I6qpvI
fwIDAQAB
-----END PUBLIC KEY-----

I0916 09:17:55.286358  536807 attestaion_server.go:425] =============== end GetAK ===============
I0916 09:17:55.287092  536807 attestaion_server.go:431] ======= GetMakeCredential ========
I0916 09:17:55.287123  536807 attestaion_server.go:448] =============== end GetMakeCredential ===============
I0916 09:17:55.287433  536807 attestaion_server.go:462]       Outbound Secret: hkxwmm8ffTLRBSfnS2UMKJlPR8BkrKB8hW4x5+uI63E=
I0916 09:17:55.320291  536807 attestaion_server.go:480] ======= SetActivateCredential ========
I0916 09:17:55.320335  536807 attestaion_server.go:513] =============== end SetActivateCredential ===============
I0916 09:17:55.321028  536807 attestaion_server.go:518] ======= OfferQuote ========
I0916 09:17:55.321077  536807 attestaion_server.go:543] =============== end OfferQuote ===============
I0916 09:17:55.330026  536807 attestaion_server.go:550] ======= SetQuote ========
I0916 09:17:55.331993  536807 attestaion_server.go:602]       quote-attested public 
-----BEGIN PUBLIC KEY-----
MIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEAssBfW4+ssoo1e7E64sjH
37Z+HPO3j4sVbHdY2r+DsMry27liE+OzNFtlWCKPMrb67wNllHXylg12twDoGvad
XEOh+/IrzLD4sTrkSXYR2lUMmtNs0ykQI7JXXjOZqvCCkWfh8t3CsTBKh0HhNTXU
dKSvojbtZ/+qrfDPsKNOaSeRQV3qfMXkZRg3E6phnfV5qaWLgO4IyiecAT0fl5Db
1n3KhRZM4Q65wGUsM4XwnAVY18AyTm01dQak6GZT+9drdCLRG73hrRJ+qtG6rS3s
ChCssupxY8NlexBhqr7oW5Wcdt96yERxQG1u04yInNTswusGbGZMKsogd0I6qpvI
fwIDAQAB
-----END PUBLIC KEY-----

I0916 09:17:55.332361  536807 attestaion_server.go:632]      PCR: 0, verified: true value: a0b5ff3383a1116bd7dc6df177c0c2d433b9ee1813ea958fa5d166a202cb2a85
I0916 09:17:55.332406  536807 attestaion_server.go:632]      PCR: 1, verified: true value: e50edb964f66a7417954b1506f78a49d62062228ce84ee0b4e7e3b0e19b64a69
I0916 09:17:55.332420  536807 attestaion_server.go:632]      PCR: 2, verified: true value: 3d458cfe55cc03ea1f443f1562beec8df51c75e14a9fcf9a7234a13f198e7969
I0916 09:17:55.332430  536807 attestaion_server.go:632]      PCR: 3, verified: true value: 3d458cfe55cc03ea1f443f1562beec8df51c75e14a9fcf9a7234a13f198e7969
I0916 09:17:55.332439  536807 attestaion_server.go:632]      PCR: 4, verified: true value: a3358453a5148b4e3f4b96b006ae1761a2ce4aea75f6a13e10eb3e0903dfd6e2
I0916 09:17:55.332463  536807 attestaion_server.go:632]      PCR: 5, verified: true value: 098a2ae2d1aabed3e346b9fef96ec64056ea4043514672243bbf40b7d0972302
I0916 09:17:55.332473  536807 attestaion_server.go:632]      PCR: 6, verified: true value: 3d458cfe55cc03ea1f443f1562beec8df51c75e14a9fcf9a7234a13f198e7969
I0916 09:17:55.332485  536807 attestaion_server.go:632]      PCR: 7, verified: true value: 0a3f60cea411388b09eac782999f5e62246ab5469f9047eb508aa22c4dcd2237
I0916 09:17:55.332498  536807 attestaion_server.go:632]      PCR: 8, verified: true value: a775d521739876ecde2c17d0e856c584ec513e8758d9199a3d5c735836ba0ebe
I0916 09:17:55.332508  536807 attestaion_server.go:632]      PCR: 9, verified: true value: 4a7254a1740444f04ec61cf3f8eb8ffb5dae2069b44ad900e894b34a07626b36
I0916 09:17:55.332518  536807 attestaion_server.go:632]      PCR: 10, verified: true value: 0000000000000000000000000000000000000000000000000000000000000000
I0916 09:17:55.332527  536807 attestaion_server.go:632]      PCR: 11, verified: true value: 0000000000000000000000000000000000000000000000000000000000000000
I0916 09:17:55.332536  536807 attestaion_server.go:632]      PCR: 12, verified: true value: 0000000000000000000000000000000000000000000000000000000000000000
I0916 09:17:55.332544  536807 attestaion_server.go:632]      PCR: 13, verified: true value: 0000000000000000000000000000000000000000000000000000000000000000
I0916 09:17:55.332559  536807 attestaion_server.go:632]      PCR: 14, verified: true value: 306f9d8b94f17d93dc6e7cf8f5c79d652eb4c6c4d13de2dddc24af416e13ecaf
I0916 09:17:55.332567  536807 attestaion_server.go:632]      PCR: 15, verified: true value: 0000000000000000000000000000000000000000000000000000000000000000
I0916 09:17:55.332576  536807 attestaion_server.go:632]      PCR: 16, verified: true value: 0000000000000000000000000000000000000000000000000000000000000000
I0916 09:17:55.332588  536807 attestaion_server.go:632]      PCR: 17, verified: true value: ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff
I0916 09:17:55.332598  536807 attestaion_server.go:632]      PCR: 18, verified: true value: ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff
I0916 09:17:55.332608  536807 attestaion_server.go:632]      PCR: 19, verified: true value: ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff
I0916 09:17:55.332618  536807 attestaion_server.go:632]      PCR: 20, verified: true value: ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff
I0916 09:17:55.332628  536807 attestaion_server.go:632]      PCR: 21, verified: true value: ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff
I0916 09:17:55.332638  536807 attestaion_server.go:632]      PCR: 22, verified: true value: ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff
I0916 09:17:55.332647  536807 attestaion_server.go:632]      PCR: 23, verified: true value: 0000000000000000000000000000000000000000000000000000000000000000
I0916 09:17:55.332663  536807 attestaion_server.go:644]      quotes verified
I0916 09:17:55.334721  536807 attestaion_server.go:673]      secureBoot State enabled: [true]
I0916 09:17:55.335263  536807 attestaion_server.go:735] >>>>>>>>  DeviceSerial Number [a8d27fc07a8746e3]
I0916 09:17:55.335306  536807 attestaion_server.go:737]       verify quote, PCRs and secureBootState
I0916 09:17:55.336439  536807 attestaion_server.go:893] Issued AK Certificate: 
-----BEGIN CERTIFICATE-----
MIIDWzCCAwKgAwIBAgIEHsAKjzAKBggqhkjOPQQDAjBbMQswCQYDVQQGEwJVUzEP
MA0GA1UECgwGR29vZ2xlMR0wGwYDVQQLDBRBdHRlc3RhdGlvbiBWZXJpZmllcjEc
MBoGA1UEAwwTQXR0ZXN0YXRpb24gUm9vdCBDQTAeFw0yNjA5MTYxMzE3NTVaFw0y
NzA5MTYxMzE3NTVaMAAwggEiMA0GCSqGSIb3DQEBAQUAA4IBDwAwggEKAoIBAQCy
wF9bj6yyijV7sTriyMfftn4c87ePixVsd1jav4OwyvLbuWIT47M0W2VYIo8ytvrv
A2WUdfKWDXa3AOga9p1cQ6H78ivMsPixOuRJdhHaVQya02zTKRAjsldeM5mq8IKR
Z+Hy3cKxMEqHQeE1NdR0pK+iNu1n/6qt8M+wo05pJ5FBXep8xeRlGDcTqmGd9Xmp
pYuA7gjKJ5wBPR+XkNvWfcqFFkzhDrnAZSwzhfCcBVjXwDJObTV1BqToZlP712t0
ItEbveGtEn6q0bqtLewKEKyy6nFjw2V7EGGqvuhblZx233rIRHFAbW7TjIic1OzC
6wZsZkwqyiB3Qjqqm8h/AgMBAAGjggFCMIIBPjAOBgNVHQ8BAf8EBAMCB4AwEAYD
VR0lBAkwBwYFZ4EFCAMwDAYDVR0TAQH/BAIwADAfBgNVHSMEGDAWgBRQDSgt/UwW
qPMy9CEVnKzdee/iOTAnBgNVHSAEIDAeMAgGBmeBBQsBATAIBgZngQULAQIwCAYG
Z4EFCwEDMIHBBgNVHREEgbkwgbagTAYIKwYBBQUHCASgQDA+BgVngQUBAoQ1MDAw
MDEwMTQ6MmY2ZDUxZGI3NzM2ZWNiOTJkY2RlMjI3ODAzMWM4YjFlY2MzODdiNDo0
ZDWgIAYIKwYBBQUHCAOgFDASDBBhOGQyN2ZjMDdhODc0NmUzpEQwQjEWMBQGBWeB
BQIBEwtpZDowMDAwMTAxNDEQMA4GBWeBBQICEwVzd3RwbTEWMBQGBWeBBQIDEwtp
ZDoyMDI0MDEyNTAKBggqhkjOPQQDAgNHADBEAiB5FdEDvlP6AQbWgB7aGQTKRx42
cj0UpBlD0dpxWnhZ6AIgGzeJpiuOXOEJvZZg1hX/DS2X71Bp/bU4PpO2GN/5Dro=
-----END CERTIFICATE-----

Certificate:
    Data:
        Version: 3 (0x2)
        Serial Number: 515902095 (0x1ec00a8f)
        Signature Algorithm: ECDSA-SHA256
        Issuer: C=US,O=Google,OU=Attestation Verifier,CN=Attestation Root CA
        Validity
            Not Before: Sep 16 13:17:55 2026 UTC
            Not After : Sep 16 13:17:55 2027 UTC
        Subject:
        Subject Public Key Info:
            Public Key Algorithm: RSA
                Public-Key: (2048 bit)
                Modulus:
                    b2:c0:5f:5b:8f:ac:b2:8a:35:7b:b1:3a:e2:c8:c7:
                    df:b6:7e:1c:f3:b7:8f:8b:15:6c:77:58:da:bf:83:
                    b0:ca:f2:db:b9:62:13:e3:b3:34:5b:65:58:22:8f:
                    32:b6:fa:ef:03:65:94:75:f2:96:0d:76:b7:00:e8:
                    1a:f6:9d:5c:43:a1:fb:f2:2b:cc:b0:f8:b1:3a:e4:
                    49:76:11:da:55:0c:9a:d3:6c:d3:29:10:23:b2:57:
                    5e:33:99:aa:f0:82:91:67:e1:f2:dd:c2:b1:30:4a:
                    87:41:e1:35:35:d4:74:a4:af:a2:36:ed:67:ff:aa:
                    ad:f0:cf:b0:a3:4e:69:27:91:41:5d:ea:7c:c5:e4:
                    65:18:37:13:aa:61:9d:f5:79:a9:a5:8b:80:ee:08:
                    ca:27:9c:01:3d:1f:97:90:db:d6:7d:ca:85:16:4c:
                    e1:0e:b9:c0:65:2c:33:85:f0:9c:05:58:d7:c0:32:
                    4e:6d:35:75:06:a4:e8:66:53:fb:d7:6b:74:22:d1:
                    1b:bd:e1:ad:12:7e:aa:d1:ba:ad:2d:ec:0a:10:ac:
                    b2:ea:71:63:c3:65:7b:10:61:aa:be:e8:5b:95:9c:
                    76:df:7a:c8:44:71:40:6d:6e:d3:8c:88:9c:d4:ec:
                    c2:eb:06:6c:66:4c:2a:ca:20:77:42:3a:aa:9b:c8:
                    7f
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
                Permanent Identifier: a8d27fc07a8746e3
                TPM Manufacturer: id:00001014
                TPM Model: swtpm
                TPM Version: id:20240125
    Signature Algorithm: ECDSA-SHA256
         30:44:02:20:79:15:d1:03:be:53:fa:01:06:d6:80:1e:da:19:
         04:ca:47:1e:36:72:3d:14:a4:19:43:d1:da:71:5a:78:59:e8:
         02:20:1b:37:89:a6:2b:8e:5c:e1:09:bd:96:60:d6:15:ff:0d:
         2d:97:ef:50:69:fd:b5:38:3e:93:b6:18:df:f9:0e:ba

I0916 09:17:55.336548  536807 attestaion_server.go:899] =============== Attestation x509 Sent ===============
```

The step-ca logs also chronicles the provisioning flows

#### ACME Server

```bash
$ step-ca

INFO[0066]                                               duration="230.149µs" duration-ns=230149 fields.time="2026-09-16T09:17:55-04:00" method=GET name=ca path=/acme/acme-da/directory protocol=HTTP/1.1 referer= remote-address=127.0.0.1 request-id=6ce8077e-49d2-4244-abca-9200814fbeff response="{\"newNonce\":\"https://ca.domain.com:8443/acme/acme-da/new-nonce\",\"newAccount\":\"https://ca.domain.com:8443/acme/acme-da/new-account\",\"newOrder\":\"https://ca.domain.com:8443/acme/acme-da/new-order\",\"revokeCert\":\"https://ca.domain.com:8443/acme/acme-da/revoke-cert\",\"keyChange\":\"https://ca.domain.com:8443/acme/acme-da/key-change\"}" size=327 status=200 user-agent=golang.org/x/crypto/acme@v0.50.0 user-id=
INFO[0066]                                               duration=8.849895ms duration-ns=8849895 fields.time="2026-09-16T09:17:55-04:00" method=HEAD name=ca nonce=VUVzQ3l4ZG5NYWZJTlV5cE5UTTBBTWRnZGlLS1ZjYnA path=/acme/acme-da/new-nonce protocol=HTTP/1.1 referer= remote-address=127.0.0.1 request-id=0c200a12-f952-4904-9589-f04074dabf56 size=0 status=200 user-agent=golang.org/x/crypto/acme@v0.50.0 user-id=
INFO[0066]                                               duration=7.059913ms duration-ns=7059913 fields.time="2026-09-16T09:17:55-04:00" method=POST name=ca nonce=aXprQVd4TThYTngwS0dudE1LYlFzalZuSldmSmZRQjg path=/acme/acme-da/new-account protocol=HTTP/1.1 referer= remote-address=127.0.0.1 request-id=2b4348a6-83f2-4e7a-a830-2b24cd4392cf response="{\"contact\":[\"mailto:admin@example.local\"],\"status\":\"valid\",\"orders\":\"https://ca.domain.com:8443/acme/acme-da/account/QIi3peczN9gQzOioEACpAuKCmngLAVcV/orders\"}" size=159 status=201 user-agent=golang.org/x/crypto/acme@v0.50.0 user-id=
INFO[0066]                                               duration=8.573941ms duration-ns=8573941 fields.time="2026-09-16T09:17:55-04:00" method=POST name=ca nonce=cEdsWlI0Y3p6NUZjUEFXOGh1N0pSdENqamxSVnhPZG8 path=/acme/acme-da/new-order protocol=HTTP/1.1 referer= remote-address=127.0.0.1 request-id=c2d5477e-c0b4-44ab-b34b-c878307a8828 response="{\"id\":\"DgkFoAQwZySe1zJxdE4RTFA4o3LslMqE\",\"status\":\"pending\",\"expires\":\"2026-09-17T13:17:55Z\",\"identifiers\":[{\"type\":\"permanent-identifier\",\"value\":\"a8d27fc07a8746e3\"}],\"notBefore\":\"2026-09-16T13:16:55Z\",\"notAfter\":\"2026-09-17T13:17:55Z\",\"authorizations\":[\"https://ca.domain.com:8443/acme/acme-da/authz/aFGCOrwjtoCjcIkrQfROmHI3dRMXxA2U\"],\"finalize\":\"https://ca.domain.com:8443/acme/acme-da/order/DgkFoAQwZySe1zJxdE4RTFA4o3LslMqE/finalize\"}" size=439 status=201 user-agent=golang.org/x/crypto/acme@v0.50.0 user-id=
INFO[0066]                                               duration=4.165823ms duration-ns=4165823 fields.time="2026-09-16T09:17:55-04:00" method=POST name=ca nonce=bTc4aDVGaDVvZE8zdjVFOExoQWN0bEtkRDBoNjR3SGM path=/acme/acme-da/authz/aFGCOrwjtoCjcIkrQfROmHI3dRMXxA2U protocol=HTTP/1.1 referer= remote-address=127.0.0.1 request-id=6c133920-b96d-4154-b086-31b386cc3b53 response="{\"identifier\":{\"type\":\"permanent-identifier\",\"value\":\"a8d27fc07a8746e3\"},\"status\":\"pending\",\"challenges\":[{\"type\":\"device-attest-01\",\"status\":\"pending\",\"token\":\"TVzfVinuwVz7vmkcJzOItLkCettROVgP\",\"url\":\"https://ca.domain.com:8443/acme/acme-da/challenge/aFGCOrwjtoCjcIkrQfROmHI3dRMXxA2U/M3e3bhUcBppFLwu11h2V5JiTUn0Lug29\"}],\"wildcard\":false,\"expires\":\"2026-09-17T13:17:55Z\"}" size=372 status=200 user-agent=golang.org/x/crypto/acme@v0.50.0 user-id=
INFO[0069]                                               duration=15.472418ms duration-ns=15472418 fields.time="2026-09-16T09:17:58-04:00" method=POST name=ca nonce=NzhOb3lMUG4wMEtocDJXYWNEMFRhUWo4WEdhYllLa0s path=/acme/acme-da/challenge/aFGCOrwjtoCjcIkrQfROmHI3dRMXxA2U/M3e3bhUcBppFLwu11h2V5JiTUn0Lug29 protocol=HTTP/1.1 referer= remote-address=127.0.0.1 request-id=84b322a8-f042-4079-8f88-b5545b2a51cb response="{\"type\":\"device-attest-01\",\"status\":\"valid\",\"token\":\"TVzfVinuwVz7vmkcJzOItLkCettROVgP\",\"validated\":\"2026-09-16T13:17:58Z\",\"url\":\"https://ca.domain.com:8443/acme/acme-da/challenge/aFGCOrwjtoCjcIkrQfROmHI3dRMXxA2U/M3e3bhUcBppFLwu11h2V5JiTUn0Lug29\"}" size=247 status=200 user-agent=golang.org/x/crypto/acme@v0.50.0 user-id=
INFO[0069]                                               duration=6.990174ms duration-ns=6990174 fields.time="2026-09-16T09:17:58-04:00" method=POST name=ca nonce=a1BvN2V6QkVGcU96VjlwT3hxY0p6ZFpzWDRRemRLams path=/acme/acme-da/order/DgkFoAQwZySe1zJxdE4RTFA4o3LslMqE protocol=HTTP/1.1 referer= remote-address=127.0.0.1 request-id=7de4e55a-8909-42b3-aa54-6eb83f8f5b21 response="{\"id\":\"DgkFoAQwZySe1zJxdE4RTFA4o3LslMqE\",\"status\":\"ready\",\"expires\":\"2026-09-17T13:17:55Z\",\"identifiers\":[{\"type\":\"permanent-identifier\",\"value\":\"a8d27fc07a8746e3\"}],\"notBefore\":\"2026-09-16T13:16:55Z\",\"notAfter\":\"2026-09-17T13:17:55Z\",\"authorizations\":[\"https://ca.domain.com:8443/acme/acme-da/authz/aFGCOrwjtoCjcIkrQfROmHI3dRMXxA2U\"],\"finalize\":\"https://ca.domain.com:8443/acme/acme-da/order/DgkFoAQwZySe1zJxdE4RTFA4o3LslMqE/finalize\"}" size=437 status=200 user-agent=golang.org/x/crypto/acme@v0.50.0 user-id=
INFO[0069]                                               duration=12.701185ms duration-ns=12701185 fields.time="2026-09-16T09:17:58-04:00" method=POST name=ca nonce=RWRkU1hza200SDU1d1FEWmI4ajdQcElLY0JDbjFnSms path=/acme/acme-da/order/DgkFoAQwZySe1zJxdE4RTFA4o3LslMqE/finalize protocol=HTTP/1.1 referer= remote-address=127.0.0.1 request-id=34af5a08-02f0-430d-b65a-a8e2ad77e5ae response="{\"id\":\"DgkFoAQwZySe1zJxdE4RTFA4o3LslMqE\",\"status\":\"valid\",\"expires\":\"2026-09-17T13:17:55Z\",\"identifiers\":[{\"type\":\"permanent-identifier\",\"value\":\"a8d27fc07a8746e3\"}],\"notBefore\":\"2026-09-16T13:16:55Z\",\"notAfter\":\"2026-09-17T13:17:55Z\",\"authorizations\":[\"https://ca.domain.com:8443/acme/acme-da/authz/aFGCOrwjtoCjcIkrQfROmHI3dRMXxA2U\"],\"finalize\":\"https://ca.domain.com:8443/acme/acme-da/order/DgkFoAQwZySe1zJxdE4RTFA4o3LslMqE/finalize\",\"certificate\":\"https://ca.domain.com:8443/acme/acme-da/certificate/cOBHxVB6PJ5FDdOwIXoW8vCZbfdH82yL\"}" size=538 status=200 user-agent=golang.org/x/crypto/acme@v0.50.0 user-id=
INFO[0069]                                               certificate="MIICEDCCAbagAwIBAgIRAJp+TxpTWrKeeaDGeJDlMhcwCgYIKoZIzj0EAwIwPjEVMBMGA1UEChMMbVRMUyBBQ01FIENBMSUwIwYDVQQDExxtVExTIEFDTUUgQ0EgSW50ZXJtZWRpYXRlIENBMB4XDTI2MDkxNjEzMTY1NVoXDTI2MDkxNzEzMTc1NVowGzEZMBcGA1UEAxMQYThkMjdmYzA3YTg3NDZlMzBZMBMGByqGSM49AgEGCCqGSM49AwEHA0IABDoLXfaABgGMAHBhPsvkA/GiOaSncKkK/4meA0Fam1Z8w7p12jLstkFfULvsR/VZzvg5GdR3Jzv5w79OKe/v76ijgbcwgbQwDgYDVR0PAQH/BAQDAgeAMBMGA1UdJQQMMAoGCCsGAQUFBwMCMB0GA1UdDgQWBBS7qv1ocghv7mZ4ufE5syPIfLFkqjAfBgNVHSMEGDAWgBS4ju3fpaSM8s4gXW5yleuoMd82vDArBgNVHREEJDAioCAGCCsGAQUFBwgDoBQwEgwQYThkMjdmYzA3YTg3NDZlMzAgBgwrBgEEAYKkZMYoQAEEEDAOAgEGBAdhY21lLWRhBAAwCgYIKoZIzj0EAwIDSAAwRQIgSCPqGhonWaxRgZsRVUIqypcc/mlauggwbwRX+sXBuhgCIQDF/hGS3UAo8Zx1Yjh9oF9Pmxeslc/YwnLljiW5SYJPVg==" duration=4.088565ms duration-ns=4088565 fields.time="2026-09-16T09:17:58-04:00" issuer="mTLS ACME CA Intermediate CA" method=POST name=ca nonce=anVZbThMRWlVaWFQekpJUGxiR0NhdEc2cGw5WDl6cTA path=/acme/acme-da/certificate/cOBHxVB6PJ5FDdOwIXoW8vCZbfdH82yL protocol=HTTP/1.1 provisioner=acme-da public-key="ECDSA P-256" referer= remote-address=127.0.0.1 request-id=1f44e338-7cec-4b8a-a1e6-8fa1cfb6c59f sans="map[]" serial=205356945151140249204738093954478977559 size=1478 status=200 subject=a8d27fc07a8746e3 user-agent=golang.org/x/crypto/acme@v0.50.0 user-id= valid-from="2026-09-16T13:16:55Z" valid-to="2026-09-17T13:17:55Z"
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
