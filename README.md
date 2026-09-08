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

This entiere flow is also described here:

- [Managed Device Attestation: ACME as the Bottom Turtle in Mobile Device Management](https://smallstep.com/blog/managed-device-attestation/)
- [ACME Device Attestation: The Modern Zero Trust Alternative to SCEP](https://www.bastionxp.com/blog/acme-device-attestation-vs-scep-zero-trust/)

In this specific setup, there are actually two distinct Certificate Authorities.

- `a.` Attestation CA on the Attestation server which verifies the client's TPM and issues an Attestation certificate to that device

- `b.` ACME server which runs its own CA to issue client certificates and is configured to accept device attestations signed by the Attestation CA.

For more general reading, see

- [ACME device attestation, smallstep and pkcs11: attezt](https://linderud.dev/blog/acme-device-attestation-smallstep-and-pkcs11-attezt/)
- [ACME Device Attestation: The Modern Zero Trust Alternative to SCEP](https://www.bastionxp.com/blog/acme-device-attestation-vs-scep-zero-trust/)

In this sample, once the x609 is issued, you can skip to the [Testing](#testing) to try out various mtls TPM clients

>> NOTE: this repo is *not* supported by google

### Step-CA Setup

To get started, you'll need golang and `smallstep-ca`, `smallstep-cli`


```bash
# First clear any existing smallstep config (if don't want to do this, make a backup of the `$HOME/.step` folder)
mv $HOME/.step $HOME/.step_backup

### setup some hosts files
$ cat /etc/hosts
127.0.0.1 attestor.domain.com server.domain.com ca.domain.com

$ step ca  init

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
        ✔ Root fingerprint: 66731a27366f96349726fbdc6c060dd0ee3867444f1e31484c94918f5e324c98
        ✔ Intermediate certificate: /home/srashid/.step/certs/intermediate_ca.crt
        ✔ Intermediate private key: /home/srashid/.step/secrets/intermediate_ca_key
        ✔ Database folder: /home/srashid/.step/db
        ✔ Default configuration: /home/srashid/.step/config/defaults.json
        ✔ Certificate Authority configuration: /home/srashid/.step/config/ca.json


### configure the TPM challenge using a trust anchored on `certs/attestation-root-ca.crt` provided in this repo
cd tpm/
step ca provisioner add acme-da --type ACME   --attestation-roots certs/attestation-root-ca.crt   --challenge device-attest-01    --attestation-format tpm


### optionally setup an HTTP challenge (this is used for the optional HTTP demo later)
## step ca provisioner add myacme --type ACME

### now start the ca
export STEPDEBUG=1
$ step-ca 
```

## TPM ACME

For the TPM demo, startup a software tpm `swtpm`:

```bash
cd tpm/swtpm/
# rm -rf myvtpm && mkdir myvtpm && swtpm_setup --tpmstate myvtpm --tpm2 --create-ek-cert
swtpm socket --tpmstate dir=myvtpm --tpm2 --server type=tcp,port=2321 --ctrl type=tcp,port=2322 --flags not-need-init,startup-clear --log level=5

export TPM2TOOLS_TCTI="swtpm:port=2321"

### then populate the PCR values so that the evenlog replay during remote attestation matches these values
####  https://github.com/salrashid123/go_tpm_remote_attestation#setup-using-softwretpm


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
  --eventLogPath=swtpm/binary_bios_measurements  \
  --v=10 -alsologtostderr
```

Note the client will write the issued x509 certificate to `certs/cert.pem` and will write the TPM based private key in two formats:  `go-attestation` key format to: `certs/tpmkey.json` and a PEM formatted TPM key to `certs/tpmkey.pem`.  The PEM format is described [here](https://www.hansenpartnership.com/draft-bottomley-tpm2-keys.html) and is compatible with openssl

---

Once you run the client and server, you'll see the sample output on the client

#### `Client Logs`

```log
$ go run client/client.go -host 127.0.0.1:50051 \
   --tpm-path="127.0.0.1:2321" \
    --stepCACertPath=$HOME/.step/certs/root_ca.crt \
      --eventLogPath=swtpm/binary_bios_measurements    --v=10 -alsologtostderr

I0907 01:55:39.608114   12821 client.go:138] Opening swtpm socket
I0907 01:55:39.610323   12821 client.go:180] Manufacturer: IBM
I0907 01:55:39.610383   12821 client.go:181] VendorInfo: SW   TPM
I0907 01:55:39.610404   12821 client.go:182] FirmwareVersionMajor: 8228
I0907 01:55:39.610424   12821 client.go:183] FirmwareVersionMinor: 293
I0907 01:55:39.611572   12821 client.go:193] EKCert Issuer: CN=swtpm-localca
I0907 01:55:39.611612   12821 client.go:210] EKCert SerialNumber: 1237
I0907 01:55:39.611640   12821 client.go:214] =============== OfferEK ===============
I0907 01:55:39.624014   12821 client.go:223] Verified EK Cert
I0907 01:55:39.624071   12821 client.go:225] =============== OfferAK ===============
I0907 01:55:39.674828   12821 client.go:249] Creating AK CSR
I0907 01:55:39.681108   12821 client.go:281] AK CSR 
-----BEGIN CERTIFICATE REQUEST-----
MIIClDCCAXwCAQAwHjEcMBoGA1UEAxMTYXR0ZXN0b3IuZG9tYWluLmNvbTCCASIw
DQYJKoZIhvcNAQEBBQADggEPADCCAQoCggEBALK3MjX7agZ5/2kXZZi+0vssZGwk
xLit7bEY4qwL8Xk6pBQo9UVmHbM+nOJuFfDMJpsJVYk52lDrwdEbeyJZOWXcDxxu
JfulDm12rvgYdw0D45J+mPJmfUXUasx3oAr9XSZROeCQR49sXtn0IvkflGQ/w/g1
PVbhMW8YzRi5tvvqyNKtJ/f6L62YIRO5KlVxxQ08YJx8bi2+dP5Y1iHB1arVVh7a
J3R3LIiyLa+gnHxEWGn1HnmRi1ktuMdWvVP/ORXjoSoaIDI+nN4ZRT9YCeYjcy/7
ocgJXEVOzuDfvc370B7JIFk30TQczi8giVPWNCLoqF5+ejBPn3qPoUDuhgkCAwEA
AaAxMC8GCSqGSIb3DQEJDjEiMCAwHgYDVR0RBBcwFYITYXR0ZXN0b3IuZG9tYWlu
LmNvbTANBgkqhkiG9w0BAQsFAAOCAQEAChQVv6X9TojUb5t60+gLkKHrRlU5aHq3
HF/PKlFU+PHkYWZ4jxHP1+yfcjVDdKxGnEzBv9olZpJvD11XOzlPtbDd1FYu9DcX
TpfWNIGqkouWGzuKWl8fpu0JMaVWA0YyGXF/QoMfBlQyCnNU0jdbYIa33pYT/Zye
eZ2nGXwiOyHTA1Fuy9EqSgCNhONobfPT0HIVF2vsGmg1zXAA2Fk0CAHMdHICGbwd
Mr2cEQpuQ1JxShPaBEg6/1jvyG99HvYZufBC/WNNqTC7PG3vxTVYfkdSny9Rpgmg
uuuGnYDTLr2G6lqFqi4cHy25uQp3Tr3ZulmBEVb9YM2uoMk3SWsOEg==
-----END CERTIFICATE REQUEST-----

I0907 01:55:39.682403   12821 client.go:292] Verified AK 
I0907 01:55:39.682453   12821 client.go:294] =============== GetMakeCredential ===============
I0907 01:55:39.691242   12821 client.go:323] EncryptedCredentials Secret PBrfFNILY1AJCAF0Ve57KAJyltgXmPYml6njvPbrW0E=
I0907 01:55:39.691289   12821 client.go:325] =============== SetActivateCredential ===============
I0907 01:55:39.691941   12821 client.go:334] SetActivateCredential complete 
I0907 01:55:39.691999   12821 client.go:336] =============== OfferQuote ===============
I0907 01:55:39.692713   12821 client.go:343] OfferQuote complete 
I0907 01:55:39.692776   12821 client.go:345] =============== SetQuote ===============
I0907 01:55:39.708401   12821 client.go:389] Issued AK Certificate: 
-----BEGIN CERTIFICATE-----
MIIDXDCCAwOgAwIBAgIFAJoOQDYwCgYIKoZIzj0EAwIwWzELMAkGA1UEBhMCVVMx
DzANBgNVBAoMBkdvb2dsZTEdMBsGA1UECwwUQXR0ZXN0YXRpb24gVmVyaWZpZXIx
HDAaBgNVBAMME0F0dGVzdGF0aW9uIFJvb3QgQ0EwHhcNMjYwOTA3MDU1NTM5WhcN
MjcwOTA3MDU1NTM5WjAAMIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEA
srcyNftqBnn/aRdlmL7S+yxkbCTEuK3tsRjirAvxeTqkFCj1RWYdsz6c4m4V8Mwm
mwlViTnaUOvB0Rt7Ilk5ZdwPHG4l+6UObXau+Bh3DQPjkn6Y8mZ9RdRqzHegCv1d
JlE54JBHj2xe2fQi+R+UZD/D+DU9VuExbxjNGLm2++rI0q0n9/ovrZghE7kqVXHF
DTxgnHxuLb50/ljWIcHVqtVWHtondHcsiLItr6CcfERYafUeeZGLWS24x1a9U/85
FeOhKhogMj6c3hlFP1gJ5iNzL/uhyAlcRU7O4N+9zfvQHskgWTfRNBzOLyCJU9Y0
IuioXn56ME+feo+hQO6GCQIDAQABo4IBQjCCAT4wDgYDVR0PAQH/BAQDAgeAMBAG
A1UdJQQJMAcGBWeBBQgDMAwGA1UdEwEB/wQCMAAwHwYDVR0jBBgwFoAUUA0oLf1M
FqjzMvQhFZys3Xnv4jkwJwYDVR0gBCAwHjAIBgZngQULAQEwCAYGZ4EFCwECMAgG
BmeBBQsBAzCBwQYDVR0RBIG5MIG2oEwGCCsGAQUFBwgEoEAwPgYFZ4EFAQKENTAw
MDAxMDE0OjJmNmQ1MWRiNzczNmVjYjkyZGNkZTIyNzgwMzFjOGIxZWNjMzg3YjQ6
NGQ1oCAGCCsGAQUFBwgDoBQwEgwQYjFmODExNDY4NWVkYzhjM6REMEIxFjAUBgVn
gQUCARMLaWQ6MDAwMDEwMTQxEDAOBgVngQUCAhMFc3d0cG0xFjAUBgVngQUCAxML
aWQ6MjAyNDAxMjUwCgYIKoZIzj0EAwIDRwAwRAIgAf3L6A7omVK77FekC8JQW4Y0
kpCW512fbKBq/3U9yf0CIDTfHO1RtXHcJSRxyWBShfOHXpycf93ymSf0IbG2sS0B
-----END CERTIFICATE-----

Certificate:
    Data:
        Version: 3 (0x2)
        Serial Number: 2584625206 (0x9a0e4036)
        Signature Algorithm: ECDSA-SHA256
        Issuer: C=US,O=Google,OU=Attestation Verifier,CN=Attestation Root CA
        Validity
            Not Before: Sep 7 05:55:39 2026 UTC
            Not After : Sep 7 05:55:39 2027 UTC
        Subject:
        Subject Public Key Info:
            Public Key Algorithm: RSA
                Public-Key: (2048 bit)
                Modulus:
                    b2:b7:32:35:fb:6a:06:79:ff:69:17:65:98:be:d2:
                    fb:2c:64:6c:24:c4:b8:ad:ed:b1:18:e2:ac:0b:f1:
                    79:3a:a4:14:28:f5:45:66:1d:b3:3e:9c:e2:6e:15:
                    f0:cc:26:9b:09:55:89:39:da:50:eb:c1:d1:1b:7b:
                    22:59:39:65:dc:0f:1c:6e:25:fb:a5:0e:6d:76:ae:
                    f8:18:77:0d:03:e3:92:7e:98:f2:66:7d:45:d4:6a:
                    cc:77:a0:0a:fd:5d:26:51:39:e0:90:47:8f:6c:5e:
                    d9:f4:22:f9:1f:94:64:3f:c3:f8:35:3d:56:e1:31:
                    6f:18:cd:18:b9:b6:fb:ea:c8:d2:ad:27:f7:fa:2f:
                    ad:98:21:13:b9:2a:55:71:c5:0d:3c:60:9c:7c:6e:
                    2d:be:74:fe:58:d6:21:c1:d5:aa:d5:56:1e:da:27:
                    74:77:2c:88:b2:2d:af:a0:9c:7c:44:58:69:f5:1e:
                    79:91:8b:59:2d:b8:c7:56:bd:53:ff:39:15:e3:a1:
                    2a:1a:20:32:3e:9c:de:19:45:3f:58:09:e6:23:73:
                    2f:fb:a1:c8:09:5c:45:4e:ce:e0:df:bd:cd:fb:d0:
                    1e:c9:20:59:37:d1:34:1c:ce:2f:20:89:53:d6:34:
                    22:e8:a8:5e:7e:7a:30:4f:9f:7a:8f:a1:40:ee:86:
                    09
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
                Permanent Identifier: b1f8114685edc8c3
                TPM Manufacturer: id:00001014
                TPM Model: swtpm
                TPM Version: id:20240125
    Signature Algorithm: ECDSA-SHA256
         30:44:02:20:01:fd:cb:e8:0e:e8:99:52:bb:ec:57:a4:0b:c2:
         50:5b:86:34:92:90:96:e7:5d:9f:6c:a0:6a:ff:75:3d:c9:fd:
         02:20:34:df:1c:ed:51:b5:71:dc:25:24:71:c9:60:52:85:f3:
         87:5e:9c:9c:7f:dd:f2:99:27:f4:21:b1:b6:b1:2d:01

I0907 01:55:39.708624   12821 client.go:391] =============== Create new Key ===============
I0907 01:55:39.708684   12821 client.go:406] Extracted Permanent Identfier: b1f8114685edc8c3
I0907 01:55:39.708739   12821 client.go:420] Extracted HardwareSerialNumber: 00001014:2f6d51db7736ecb92dcde2278031c8b1ecc387b4:4d5
I0907 01:55:39.708789   12821 client.go:422]      Starting ACME Key generation
I0907 01:55:39.731259   12821 client.go:469] Successfully registered ACME account.
I0907 01:55:39.740632   12821 client.go:478] Order created. URI: https://ca.domain.com:8443/acme/acme-da/order/M2baOJzsNRsNj4ljPyNxbAPBAaDJ4sDb
I0907 01:55:39.744137   12821 client.go:503] Fulfill challenge token: HsOkD2uQWiQQ9gFLQZ2bzQqDNfbARkba
I0907 01:55:39.744221   12821 client.go:505] =============== Create New Key and set challengToken ===============
I0907 01:55:39.744291   12821 client.go:515] KEYAUTH: HsOkD2uQWiQQ9gFLQZ2bzQqDNfbARkba.8KCuHVeaFHicx-GiM1XeIrb0vzhtoeMeUjsTWo46h2o
I0907 01:55:39.744351   12821 client.go:517] Create a TPM based key
I0907 01:55:39.756658   12821 client.go:589] Generated ECC Public 
-----BEGIN PUBLIC KEY-----
MFkwEwYHKoZIzj0CAQYIKoZIzj0DAQcDQgAEcSUYEK7hPX5DlANvoFuvOrr/az1S
CvSWw9OOo7CtGiMajMxPWJjZ6X51xoBdPfiUb92UcwikiZx3mbxdx0ksjw==
-----END PUBLIC KEY-----
I0907 01:55:42.759216   12821 client.go:646] started server accepting challenge
I0907 01:55:42.769823   12821 client.go:659] Waiting for order readiness validation...
I0907 01:55:42.780289   12821 client.go:713] Finalizing order with CSR...
I0907 01:55:42.801497   12821 client.go:808] Acme Root Certificate: 
Certificate:
    Data:
        Version: 3 (0x2)
        Serial Number: 258497696593010755702957210771951133590 (0xc278d86ee91954f974d5211a6d6b2396)
        Signature Algorithm: ECDSA-SHA256
        Issuer: O=mTLS ACME CA,CN=mTLS ACME CA Root CA
        Validity
            Not Before: Sep 4 19:08:08 2026 UTC
            Not After : Sep 1 19:08:08 2036 UTC
        Subject: O=mTLS ACME CA,CN=mTLS ACME CA Root CA
        Subject Public Key Info:
            Public Key Algorithm: ECDSA
                Public-Key: (256 bit)
                X:
                    af:a2:6b:97:ce:4a:32:8f:a6:c9:4d:b4:2f:c2:55:
                    77:33:b8:9d:68:93:4d:d9:e8:b9:b6:99:43:25:26:
                    11:dd
                Y:
                    2d:01:17:03:4f:a0:35:1e:c0:82:d0:a4:b5:ae:bd:
                    75:85:98:fc:8e:18:66:32:44:07:67:ca:6f:72:ba:
                    db:cf
                Curve: P-256
        X509v3 extensions:
            X509v3 Key Usage: critical
                Certificate Sign, CRL Sign
            X509v3 Basic Constraints: critical
                CA:TRUE, pathlen:1
            X509v3 Subject Key Identifier:
                CE:AD:C2:C8:2F:93:C4:A5:8A:5F:6A:8A:3D:FE:26:48:49:28:6B:80
    Signature Algorithm: ECDSA-SHA256
         30:46:02:21:00:81:91:41:c6:20:06:e8:b0:26:29:44:2f:ad:
         66:70:5c:94:61:25:0d:28:f2:f3:05:65:24:08:8b:ee:a6:11:
         3d:02:21:00:cf:13:e4:02:b4:9a:20:41:28:b8:01:6e:6e:55:
         1b:97:8d:69:1b:93:99:67:06:09:fc:e6:e8:db:cb:13:3c:7f

I0907 01:55:42.802132   12821 client.go:832] Certificate: 
Certificate:
    Data:
        Version: 3 (0x2)
        Serial Number: 76085310278373732917148320914805975329 (0x393d7f6bd7f05b63f9a4a9803f796921)
        Signature Algorithm: ECDSA-SHA256
        Issuer: O=mTLS ACME CA,CN=mTLS ACME CA Intermediate CA
        Validity
            Not Before: Sep 7 05:54:39 2026 UTC
            Not After : Sep 8 05:55:39 2026 UTC
        Subject: CN=b1f8114685edc8c3
        Subject Public Key Info:
            Public Key Algorithm: ECDSA
                Public-Key: (256 bit)
                X:
                    71:25:18:10:ae:e1:3d:7e:43:94:03:6f:a0:5b:af:
                    3a:ba:ff:6b:3d:52:0a:f4:96:c3:d3:8e:a3:b0:ad:
                    1a:23
                Y:
                    1a:8c:cc:4f:58:98:d9:e9:7e:75:c6:80:5d:3d:f8:
                    94:6f:dd:94:73:08:a4:89:9c:77:99:bc:5d:c7:49:
                    2c:8f
                Curve: P-256
        X509v3 extensions:
            X509v3 Key Usage: critical
                Digital Signature
            X509v3 Extended Key Usage:
                Client Authentication
            X509v3 Subject Key Identifier:
                46:D9:01:65:D4:6A:31:74:E0:22:D5:D9:DD:F1:CB:88:06:9F:57:4A
            X509v3 Authority Key Identifier:
                21:B7:0E:DF:16:C6:7B:30:E0:52:05:E9:CE:90:1F:22:1A:7D:47:69
            X509v3 Subject Alternative Name:
                Permanent Identifier: b1f8114685edc8c3
            X509v3 Step Provisioner:
                Type: ACME
                Name: acme-da
    Signature Algorithm: ECDSA-SHA256
         30:46:02:21:00:8c:2c:98:c2:87:fe:98:f2:44:30:40:9c:e8:
         9e:c1:22:48:fb:59:29:28:6f:d3:83:e3:a2:a7:73:13:8f:69:
         5d:02:21:00:e3:fd:b7:aa:5a:f5:bd:26:35:34:98:e8:7d:34:
         e0:c7:38:ee:2d:33:98:64:09:28:68:cc:77:34:48:00:f5:14

I0907 01:55:42.802565   12821 client.go:832] Certificate: 
Certificate:
    Data:
        Version: 3 (0x2)
        Serial Number: 91886573187782576404019451414332059612 (0x4520b5d4d75499b3787b3aaae9742fdc)
        Signature Algorithm: ECDSA-SHA256
        Issuer: O=mTLS ACME CA,CN=mTLS ACME CA Root CA
        Validity
            Not Before: Sep 4 19:08:09 2026 UTC
            Not After : Sep 1 19:08:09 2036 UTC
        Subject: O=mTLS ACME CA,CN=mTLS ACME CA Intermediate CA
        Subject Public Key Info:
            Public Key Algorithm: ECDSA
                Public-Key: (256 bit)
                X:
                    da:8d:db:a1:81:74:32:3f:cf:62:c0:4f:50:a8:27:
                    dd:9b:8c:d3:ae:86:ef:4a:3c:9e:a4:52:c4:ac:a2:
                    5c:35
                Y:
                    df:55:6b:9a:1f:f3:6a:9a:7e:46:f9:3e:89:c1:c0:
                    31:d7:a2:d2:e8:1a:47:b5:02:5c:2d:c5:51:87:c9:
                    d0:96
                Curve: P-256
        X509v3 extensions:
            X509v3 Key Usage: critical
                Certificate Sign, CRL Sign
            X509v3 Basic Constraints: critical
                CA:TRUE, pathlen:0
            X509v3 Subject Key Identifier:
                21:B7:0E:DF:16:C6:7B:30:E0:52:05:E9:CE:90:1F:22:1A:7D:47:69
            X509v3 Authority Key Identifier:
                CE:AD:C2:C8:2F:93:C4:A5:8A:5F:6A:8A:3D:FE:26:48:49:28:6B:80
    Signature Algorithm: ECDSA-SHA256
         30:45:02:21:00:f1:a9:5a:d5:23:28:e7:34:84:36:67:e0:37:
         60:e6:da:83:ce:8d:22:c0:ef:87:1b:fd:be:40:b0:8a:2d:56:
         58:02:20:27:28:96:85:bb:6b:3c:f5:e1:db:b4:b7:81:1c:2e:
         19:ad:59:36:dd:4f:f7:92:85:ea:ce:11:fc:1f:08:af:15
```

#### `Server Logs`

The server output will just show it doing TPM remote attestation and issuing a cert

```log
$ go run attestation_server/attestaion_server.go     
     --ekrootCA swtpm/config/var/lib/swtpm-localca/issuercert.pem    \
     --expectedPCRMapSHA256=0:a0b5ff3383a1116bd7dc6df177c0c2d433b9ee1813ea958fa5d166a202cb2a85         --v=40 -alsologtostderr


I0907 01:55:36.254007   12740 attestaion_server.go:968] Starting gRPC server on port :50051
I0907 01:55:39.621609   12740 attestaion_server.go:158] ======= OfferEK ========
I0907 01:55:39.622242   12740 attestaion_server.go:181] EK Certificate: 
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

I0907 01:55:39.622382   12740 attestaion_server.go:212]      TPM Manufacturer id:00001014
I0907 01:55:39.622403   12740 attestaion_server.go:215]      TPM Model swtpm
I0907 01:55:39.622422   12740 attestaion_server.go:219]      TPM Version id:20240125
I0907 01:55:39.622449   12740 attestaion_server.go:251]      TPM Family 2.0
I0907 01:55:39.622470   12740 attestaion_server.go:252]      TPM Level 0
I0907 01:55:39.622491   12740 attestaion_server.go:253]      TPM Revision 183
I0907 01:55:39.622526   12740 attestaion_server.go:268]         EKCertificate ========
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

I0907 01:55:39.622610   12740 attestaion_server.go:284]      EKCert  Issuer CN=swtpm-localca
I0907 01:55:39.622665   12740 attestaion_server.go:285]      EKCert  IssuingCertificateURL []
I0907 01:55:39.622697   12740 attestaion_server.go:286]      EKCert  SerialNumber 1237
I0907 01:55:39.622723   12740 attestaion_server.go:288]     EkCert Public Key 
-----BEGIN PUBLIC KEY-----
MIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEAwJeerh1yB9bYbI/QZldk
Dl0esVUcoodQAox8sbGEaWImeaH75Xw3IkBegeU+SmXIWl5WeyyYSZ9x366pKGhp
zEAQjLnAdqI/mLU6mNCdGo2nL42xmdiuyTYOjcbkEba40QA6lvloKViIqnhBM1jv
m3G5yK+xdJDsktzg32ovx6Kg3aAEQk2PGBlorVi2Ha1ZoeafHst3DccYxQWD3qoQ
qIoF6EGPr1ANDa1rTpTxZglKPvNB7sukNRZ9rpIivuXQhuzFszZ3YPJptf3I4dpP
+XM+zd/LiznWabv4sTGThHfkcJq0mTxhvPA6uWQVq/87CXqIAM3cA7aLxKJ8LtbL
WQIDAQAB
-----END PUBLIC KEY-----

I0907 01:55:39.622754   12740 attestaion_server.go:291]     Verifying EKCert
I0907 01:55:39.623021   12740 attestaion_server.go:319]      EKCert Includes tcg-kp-EKCertificate ExtendedKeyUsage 2.23.133.8.1
I0907 01:55:39.623472   12740 attestaion_server.go:348]     EKCert Verified
I0907 01:55:39.623507   12740 attestaion_server.go:362] =============== end OfferEK ===============
I0907 01:55:39.681891   12740 attestaion_server.go:367] ======= OfferAK ========
I0907 01:55:39.682079   12740 attestaion_server.go:411]       ak public 
-----BEGIN PUBLIC KEY-----
MIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEAsrcyNftqBnn/aRdlmL7S
+yxkbCTEuK3tsRjirAvxeTqkFCj1RWYdsz6c4m4V8MwmmwlViTnaUOvB0Rt7Ilk5
ZdwPHG4l+6UObXau+Bh3DQPjkn6Y8mZ9RdRqzHegCv1dJlE54JBHj2xe2fQi+R+U
ZD/D+DU9VuExbxjNGLm2++rI0q0n9/ovrZghE7kqVXHFDTxgnHxuLb50/ljWIcHV
qtVWHtondHcsiLItr6CcfERYafUeeZGLWS24x1a9U/85FeOhKhogMj6c3hlFP1gJ
5iNzL/uhyAlcRU7O4N+9zfvQHskgWTfRNBzOLyCJU9Y0IuioXn56ME+feo+hQO6G
CQIDAQAB
-----END PUBLIC KEY-----

I0907 01:55:39.682163   12740 attestaion_server.go:425] =============== end GetAK ===============
I0907 01:55:39.682792   12740 attestaion_server.go:431] ======= GetMakeCredential ========
I0907 01:55:39.682820   12740 attestaion_server.go:448] =============== end GetMakeCredential ===============
I0907 01:55:39.683089   12740 attestaion_server.go:462]       Outbound Secret: PBrfFNILY1AJCAF0Ve57KAJyltgXmPYml6njvPbrW0E=
I0907 01:55:39.691715   12740 attestaion_server.go:480] ======= SetActivateCredential ========
I0907 01:55:39.691739   12740 attestaion_server.go:513] =============== end SetActivateCredential ===============
I0907 01:55:39.692348   12740 attestaion_server.go:518] ======= OfferQuote ========
I0907 01:55:39.692392   12740 attestaion_server.go:543] =============== end OfferQuote ===============
I0907 01:55:39.702229   12740 attestaion_server.go:550] ======= SetQuote ========
I0907 01:55:39.703631   12740 attestaion_server.go:602]       quote-attested public 
-----BEGIN PUBLIC KEY-----
MIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEAsrcyNftqBnn/aRdlmL7S
+yxkbCTEuK3tsRjirAvxeTqkFCj1RWYdsz6c4m4V8MwmmwlViTnaUOvB0Rt7Ilk5
ZdwPHG4l+6UObXau+Bh3DQPjkn6Y8mZ9RdRqzHegCv1dJlE54JBHj2xe2fQi+R+U
ZD/D+DU9VuExbxjNGLm2++rI0q0n9/ovrZghE7kqVXHFDTxgnHxuLb50/ljWIcHV
qtVWHtondHcsiLItr6CcfERYafUeeZGLWS24x1a9U/85FeOhKhogMj6c3hlFP1gJ
5iNzL/uhyAlcRU7O4N+9zfvQHskgWTfRNBzOLyCJU9Y0IuioXn56ME+feo+hQO6G
CQIDAQAB
-----END PUBLIC KEY-----

I0907 01:55:39.703892   12740 attestaion_server.go:632]      PCR: 0, verified: true value: a0b5ff3383a1116bd7dc6df177c0c2d433b9ee1813ea958fa5d166a202cb2a85
I0907 01:55:39.703924   12740 attestaion_server.go:632]      PCR: 1, verified: true value: e50edb964f66a7417954b1506f78a49d62062228ce84ee0b4e7e3b0e19b64a69
I0907 01:55:39.703940   12740 attestaion_server.go:632]      PCR: 2, verified: true value: 3d458cfe55cc03ea1f443f1562beec8df51c75e14a9fcf9a7234a13f198e7969
I0907 01:55:39.703956   12740 attestaion_server.go:632]      PCR: 3, verified: true value: 3d458cfe55cc03ea1f443f1562beec8df51c75e14a9fcf9a7234a13f198e7969
I0907 01:55:39.703968   12740 attestaion_server.go:632]      PCR: 4, verified: true value: a3358453a5148b4e3f4b96b006ae1761a2ce4aea75f6a13e10eb3e0903dfd6e2
I0907 01:55:39.703979   12740 attestaion_server.go:632]      PCR: 5, verified: true value: 098a2ae2d1aabed3e346b9fef96ec64056ea4043514672243bbf40b7d0972302
I0907 01:55:39.703990   12740 attestaion_server.go:632]      PCR: 6, verified: true value: 3d458cfe55cc03ea1f443f1562beec8df51c75e14a9fcf9a7234a13f198e7969
I0907 01:55:39.704000   12740 attestaion_server.go:632]      PCR: 7, verified: true value: 0a3f60cea411388b09eac782999f5e62246ab5469f9047eb508aa22c4dcd2237
I0907 01:55:39.704009   12740 attestaion_server.go:632]      PCR: 8, verified: true value: a775d521739876ecde2c17d0e856c584ec513e8758d9199a3d5c735836ba0ebe
I0907 01:55:39.704018   12740 attestaion_server.go:632]      PCR: 9, verified: true value: 4a7254a1740444f04ec61cf3f8eb8ffb5dae2069b44ad900e894b34a07626b36
I0907 01:55:39.704028   12740 attestaion_server.go:632]      PCR: 10, verified: true value: 0000000000000000000000000000000000000000000000000000000000000000
I0907 01:55:39.704037   12740 attestaion_server.go:632]      PCR: 11, verified: true value: 0000000000000000000000000000000000000000000000000000000000000000
I0907 01:55:39.704046   12740 attestaion_server.go:632]      PCR: 12, verified: true value: 0000000000000000000000000000000000000000000000000000000000000000
I0907 01:55:39.704055   12740 attestaion_server.go:632]      PCR: 13, verified: true value: 0000000000000000000000000000000000000000000000000000000000000000
I0907 01:55:39.704065   12740 attestaion_server.go:632]      PCR: 14, verified: true value: 306f9d8b94f17d93dc6e7cf8f5c79d652eb4c6c4d13de2dddc24af416e13ecaf
I0907 01:55:39.704075   12740 attestaion_server.go:632]      PCR: 15, verified: true value: 0000000000000000000000000000000000000000000000000000000000000000
I0907 01:55:39.704084   12740 attestaion_server.go:632]      PCR: 16, verified: true value: 0000000000000000000000000000000000000000000000000000000000000000
I0907 01:55:39.704094   12740 attestaion_server.go:632]      PCR: 17, verified: true value: ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff
I0907 01:55:39.704103   12740 attestaion_server.go:632]      PCR: 18, verified: true value: ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff
I0907 01:55:39.704112   12740 attestaion_server.go:632]      PCR: 19, verified: true value: ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff
I0907 01:55:39.704122   12740 attestaion_server.go:632]      PCR: 20, verified: true value: ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff
I0907 01:55:39.704131   12740 attestaion_server.go:632]      PCR: 21, verified: true value: ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff
I0907 01:55:39.704141   12740 attestaion_server.go:632]      PCR: 22, verified: true value: ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff
I0907 01:55:39.704150   12740 attestaion_server.go:632]      PCR: 23, verified: true value: 0000000000000000000000000000000000000000000000000000000000000000
I0907 01:55:39.704159   12740 attestaion_server.go:644]      quotes verified
I0907 01:55:39.705456   12740 attestaion_server.go:673]      secureBoot State enabled: [true]
I0907 01:55:39.706046   12740 attestaion_server.go:735] >>>>>>>>  DeviceSerial Number [b1f8114685edc8c3]
I0907 01:55:39.706088   12740 attestaion_server.go:737]       verify quote, PCRs and secureBootState
I0907 01:55:39.707200   12740 attestaion_server.go:893] Issued AK Certificate: 
-----BEGIN CERTIFICATE-----
MIIDXDCCAwOgAwIBAgIFAJoOQDYwCgYIKoZIzj0EAwIwWzELMAkGA1UEBhMCVVMx
DzANBgNVBAoMBkdvb2dsZTEdMBsGA1UECwwUQXR0ZXN0YXRpb24gVmVyaWZpZXIx
HDAaBgNVBAMME0F0dGVzdGF0aW9uIFJvb3QgQ0EwHhcNMjYwOTA3MDU1NTM5WhcN
MjcwOTA3MDU1NTM5WjAAMIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEA
srcyNftqBnn/aRdlmL7S+yxkbCTEuK3tsRjirAvxeTqkFCj1RWYdsz6c4m4V8Mwm
mwlViTnaUOvB0Rt7Ilk5ZdwPHG4l+6UObXau+Bh3DQPjkn6Y8mZ9RdRqzHegCv1d
JlE54JBHj2xe2fQi+R+UZD/D+DU9VuExbxjNGLm2++rI0q0n9/ovrZghE7kqVXHF
DTxgnHxuLb50/ljWIcHVqtVWHtondHcsiLItr6CcfERYafUeeZGLWS24x1a9U/85
FeOhKhogMj6c3hlFP1gJ5iNzL/uhyAlcRU7O4N+9zfvQHskgWTfRNBzOLyCJU9Y0
IuioXn56ME+feo+hQO6GCQIDAQABo4IBQjCCAT4wDgYDVR0PAQH/BAQDAgeAMBAG
A1UdJQQJMAcGBWeBBQgDMAwGA1UdEwEB/wQCMAAwHwYDVR0jBBgwFoAUUA0oLf1M
FqjzMvQhFZys3Xnv4jkwJwYDVR0gBCAwHjAIBgZngQULAQEwCAYGZ4EFCwECMAgG
BmeBBQsBAzCBwQYDVR0RBIG5MIG2oEwGCCsGAQUFBwgEoEAwPgYFZ4EFAQKENTAw
MDAxMDE0OjJmNmQ1MWRiNzczNmVjYjkyZGNkZTIyNzgwMzFjOGIxZWNjMzg3YjQ6
NGQ1oCAGCCsGAQUFBwgDoBQwEgwQYjFmODExNDY4NWVkYzhjM6REMEIxFjAUBgVn
gQUCARMLaWQ6MDAwMDEwMTQxEDAOBgVngQUCAhMFc3d0cG0xFjAUBgVngQUCAxML
aWQ6MjAyNDAxMjUwCgYIKoZIzj0EAwIDRwAwRAIgAf3L6A7omVK77FekC8JQW4Y0
kpCW512fbKBq/3U9yf0CIDTfHO1RtXHcJSRxyWBShfOHXpycf93ymSf0IbG2sS0B
-----END CERTIFICATE-----

Certificate:
    Data:
        Version: 3 (0x2)
        Serial Number: 2584625206 (0x9a0e4036)
        Signature Algorithm: ECDSA-SHA256
        Issuer: C=US,O=Google,OU=Attestation Verifier,CN=Attestation Root CA
        Validity
            Not Before: Sep 7 05:55:39 2026 UTC
            Not After : Sep 7 05:55:39 2027 UTC
        Subject:
        Subject Public Key Info:
            Public Key Algorithm: RSA
                Public-Key: (2048 bit)
                Modulus:
                    b2:b7:32:35:fb:6a:06:79:ff:69:17:65:98:be:d2:
                    fb:2c:64:6c:24:c4:b8:ad:ed:b1:18:e2:ac:0b:f1:
                    79:3a:a4:14:28:f5:45:66:1d:b3:3e:9c:e2:6e:15:
                    f0:cc:26:9b:09:55:89:39:da:50:eb:c1:d1:1b:7b:
                    22:59:39:65:dc:0f:1c:6e:25:fb:a5:0e:6d:76:ae:
                    f8:18:77:0d:03:e3:92:7e:98:f2:66:7d:45:d4:6a:
                    cc:77:a0:0a:fd:5d:26:51:39:e0:90:47:8f:6c:5e:
                    d9:f4:22:f9:1f:94:64:3f:c3:f8:35:3d:56:e1:31:
                    6f:18:cd:18:b9:b6:fb:ea:c8:d2:ad:27:f7:fa:2f:
                    ad:98:21:13:b9:2a:55:71:c5:0d:3c:60:9c:7c:6e:
                    2d:be:74:fe:58:d6:21:c1:d5:aa:d5:56:1e:da:27:
                    74:77:2c:88:b2:2d:af:a0:9c:7c:44:58:69:f5:1e:
                    79:91:8b:59:2d:b8:c7:56:bd:53:ff:39:15:e3:a1:
                    2a:1a:20:32:3e:9c:de:19:45:3f:58:09:e6:23:73:
                    2f:fb:a1:c8:09:5c:45:4e:ce:e0:df:bd:cd:fb:d0:
                    1e:c9:20:59:37:d1:34:1c:ce:2f:20:89:53:d6:34:
                    22:e8:a8:5e:7e:7a:30:4f:9f:7a:8f:a1:40:ee:86:
                    09
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
                Permanent Identifier: b1f8114685edc8c3
                TPM Manufacturer: id:00001014
                TPM Model: swtpm
                TPM Version: id:20240125
    Signature Algorithm: ECDSA-SHA256
         30:44:02:20:01:fd:cb:e8:0e:e8:99:52:bb:ec:57:a4:0b:c2:
         50:5b:86:34:92:90:96:e7:5d:9f:6c:a0:6a:ff:75:3d:c9:fd:
         02:20:34:df:1c:ed:51:b5:71:dc:25:24:71:c9:60:52:85:f3:
         87:5e:9c:9c:7f:dd:f2:99:27:f4:21:b1:b6:b1:2d:01

I0907 01:55:39.707348   12740 attestaion_server.go:899] =============== Attestation x509 Sent ===============
```

The step-ca logs also chronicles the provisioning flows

### ACME Server

```bash
$ step-ca

INFO[0123]                                               duration="102.045µs" duration-ns=102045 fields.time="2026-09-07T01:55:39-04:00" method=GET name=ca path=/acme/acme-da/directory protocol=HTTP/1.1 referer= remote-address=127.0.0.1 request-id=07157607-d311-4306-bb23-47e2beedc10f response="{\"newNonce\":\"https://ca.domain.com:8443/acme/acme-da/new-nonce\",\"newAccount\":\"https://ca.domain.com:8443/acme/acme-da/new-account\",\"newOrder\":\"https://ca.domain.com:8443/acme/acme-da/new-order\",\"revokeCert\":\"https://ca.domain.com:8443/acme/acme-da/revoke-cert\",\"keyChange\":\"https://ca.domain.com:8443/acme/acme-da/key-change\"}" size=327 status=200 user-agent=golang.org/x/crypto/acme@v0.50.0 user-id=
INFO[0123]                                               duration=8.265884ms duration-ns=8265884 fields.time="2026-09-07T01:55:39-04:00" method=HEAD name=ca nonce=ZmVKUll5SFQzSWt2ZFE4YWE4T1MyZmlFaG9yUE52TGo path=/acme/acme-da/new-nonce protocol=HTTP/1.1 referer= remote-address=127.0.0.1 request-id=711bd1f8-890c-43c4-96b1-e89482ef379b size=0 status=200 user-agent=golang.org/x/crypto/acme@v0.50.0 user-id=
INFO[0123]                                               duration=6.615237ms duration-ns=6615237 fields.time="2026-09-07T01:55:39-04:00" method=POST name=ca nonce=bkVDSzdVUjRjenFRS2FDQXRYaEJwUWdRRWhLb1F5aVY path=/acme/acme-da/new-account protocol=HTTP/1.1 referer= remote-address=127.0.0.1 request-id=b7bc9032-64af-4c2b-a179-718cb79ae073 response="{\"contact\":[\"mailto:admin@example.local\"],\"status\":\"valid\",\"orders\":\"https://ca.domain.com:8443/acme/acme-da/account/eLqfI2fmdGjBXoDXolQrdu9PnIhBdLig/orders\"}" size=159 status=201 user-agent=golang.org/x/crypto/acme@v0.50.0 user-id=
INFO[0123]                                               duration=8.211182ms duration-ns=8211182 fields.time="2026-09-07T01:55:39-04:00" method=POST name=ca nonce=ZWZDVHNrczlRemJpYmxpM0Q3R3VIaWRhWUJXak5BYUc path=/acme/acme-da/new-order protocol=HTTP/1.1 referer= remote-address=127.0.0.1 request-id=7f2efbad-8b76-4c29-9f56-1f206dee03c0 response="{\"id\":\"M2baOJzsNRsNj4ljPyNxbAPBAaDJ4sDb\",\"status\":\"pending\",\"expires\":\"2026-09-08T05:55:39Z\",\"identifiers\":[{\"type\":\"permanent-identifier\",\"value\":\"b1f8114685edc8c3\"}],\"notBefore\":\"2026-09-07T05:54:39Z\",\"notAfter\":\"2026-09-08T05:55:39Z\",\"authorizations\":[\"https://ca.domain.com:8443/acme/acme-da/authz/0ucfXQVnSmIlCdS9C0mirdUoWGpUCwPw\"],\"finalize\":\"https://ca.domain.com:8443/acme/acme-da/order/M2baOJzsNRsNj4ljPyNxbAPBAaDJ4sDb/finalize\"}" size=439 status=201 user-agent=golang.org/x/crypto/acme@v0.50.0 user-id=
INFO[0123]                                               duration=2.603309ms duration-ns=2603309 fields.time="2026-09-07T01:55:39-04:00" method=POST name=ca nonce=cG1qeW5MZ3YxOHUwQ1k1SURSY21GbWdySTU2Tk9veDU path=/acme/acme-da/authz/0ucfXQVnSmIlCdS9C0mirdUoWGpUCwPw protocol=HTTP/1.1 referer= remote-address=127.0.0.1 request-id=a5def9a4-e64f-4098-a501-145f722ca97c response="{\"identifier\":{\"type\":\"permanent-identifier\",\"value\":\"b1f8114685edc8c3\"},\"status\":\"pending\",\"challenges\":[{\"type\":\"device-attest-01\",\"status\":\"pending\",\"token\":\"HsOkD2uQWiQQ9gFLQZ2bzQqDNfbARkba\",\"url\":\"https://ca.domain.com:8443/acme/acme-da/challenge/0ucfXQVnSmIlCdS9C0mirdUoWGpUCwPw/4JDt9tGuEPc880wSwsJzj1rV60MFPGaz\"}],\"wildcard\":false,\"expires\":\"2026-09-08T05:55:39Z\"}" size=372 status=200 user-agent=golang.org/x/crypto/acme@v0.50.0 user-id=
INFO[0126]                                               duration=8.856602ms duration-ns=8856602 fields.time="2026-09-07T01:55:42-04:00" method=POST name=ca nonce=NzVlS0NGaXNaSWEwZnZKYk93NnZsVGFLN0RLSExwbXA path=/acme/acme-da/challenge/0ucfXQVnSmIlCdS9C0mirdUoWGpUCwPw/4JDt9tGuEPc880wSwsJzj1rV60MFPGaz protocol=HTTP/1.1 referer= remote-address=127.0.0.1 request-id=e84dd6fa-0fcf-4a3e-b652-9a11074a4ed9 response="{\"type\":\"device-attest-01\",\"status\":\"valid\",\"token\":\"HsOkD2uQWiQQ9gFLQZ2bzQqDNfbARkba\",\"validated\":\"2026-09-07T05:55:42Z\",\"url\":\"https://ca.domain.com:8443/acme/acme-da/challenge/0ucfXQVnSmIlCdS9C0mirdUoWGpUCwPw/4JDt9tGuEPc880wSwsJzj1rV60MFPGaz\"}" size=247 status=200 user-agent=golang.org/x/crypto/acme@v0.50.0 user-id=
INFO[0126]                                               duration=7.251235ms duration-ns=7251235 fields.time="2026-09-07T01:55:42-04:00" method=POST name=ca nonce=Y2FndVB5ak9wWkNucGVnOXNKMFBjaGhjN1IzaWRaZWY path=/acme/acme-da/order/M2baOJzsNRsNj4ljPyNxbAPBAaDJ4sDb protocol=HTTP/1.1 referer= remote-address=127.0.0.1 request-id=c3edbfa0-298d-452e-913f-4bb1ea441935 response="{\"id\":\"M2baOJzsNRsNj4ljPyNxbAPBAaDJ4sDb\",\"status\":\"ready\",\"expires\":\"2026-09-08T05:55:39Z\",\"identifiers\":[{\"type\":\"permanent-identifier\",\"value\":\"b1f8114685edc8c3\"}],\"notBefore\":\"2026-09-07T05:54:39Z\",\"notAfter\":\"2026-09-08T05:55:39Z\",\"authorizations\":[\"https://ca.domain.com:8443/acme/acme-da/authz/0ucfXQVnSmIlCdS9C0mirdUoWGpUCwPw\"],\"finalize\":\"https://ca.domain.com:8443/acme/acme-da/order/M2baOJzsNRsNj4ljPyNxbAPBAaDJ4sDb/finalize\"}" size=437 status=200 user-agent=golang.org/x/crypto/acme@v0.50.0 user-id=
INFO[0126]                                               duration=13.541417ms duration-ns=13541417 fields.time="2026-09-07T01:55:42-04:00" method=POST name=ca nonce=S0tZelp1U00xS3JPc2JJdXNpblRpMW1DbnBpVUM0ZUQ path=/acme/acme-da/order/M2baOJzsNRsNj4ljPyNxbAPBAaDJ4sDb/finalize protocol=HTTP/1.1 referer= remote-address=127.0.0.1 request-id=c9df42ad-04b1-4984-880b-523cfea7a659 response="{\"id\":\"M2baOJzsNRsNj4ljPyNxbAPBAaDJ4sDb\",\"status\":\"valid\",\"expires\":\"2026-09-08T05:55:39Z\",\"identifiers\":[{\"type\":\"permanent-identifier\",\"value\":\"b1f8114685edc8c3\"}],\"notBefore\":\"2026-09-07T05:54:39Z\",\"notAfter\":\"2026-09-08T05:55:39Z\",\"authorizations\":[\"https://ca.domain.com:8443/acme/acme-da/authz/0ucfXQVnSmIlCdS9C0mirdUoWGpUCwPw\"],\"finalize\":\"https://ca.domain.com:8443/acme/acme-da/order/M2baOJzsNRsNj4ljPyNxbAPBAaDJ4sDb/finalize\",\"certificate\":\"https://ca.domain.com:8443/acme/acme-da/certificate/h9YCkdRz1iCZ13FkbClrEBCDhRakl7eO\"}" size=538 status=200 user-agent=golang.org/x/crypto/acme@v0.50.0 user-id=
INFO[0126]                                               certificate="MIICEDCCAbWgAwIBAgIQOT1/a9fwW2P5pKmAP3lpITAKBggqhkjOPQQDAjA+MRUwEwYDVQQKEwxtVExTIEFDTUUgQ0ExJTAjBgNVBAMTHG1UTFMgQUNNRSBDQSBJbnRlcm1lZGlhdGUgQ0EwHhcNMjYwOTA3MDU1NDM5WhcNMjYwOTA4MDU1NTM5WjAbMRkwFwYDVQQDExBiMWY4MTE0Njg1ZWRjOGMzMFkwEwYHKoZIzj0CAQYIKoZIzj0DAQcDQgAEcSUYEK7hPX5DlANvoFuvOrr/az1SCvSWw9OOo7CtGiMajMxPWJjZ6X51xoBdPfiUb92UcwikiZx3mbxdx0ksj6OBtzCBtDAOBgNVHQ8BAf8EBAMCB4AwEwYDVR0lBAwwCgYIKwYBBQUHAwIwHQYDVR0OBBYEFEbZAWXUajF04CLV2d3xy4gGn1dKMB8GA1UdIwQYMBaAFCG3Dt8Wxnsw4FIF6c6QHyIafUdpMCsGA1UdEQQkMCKgIAYIKwYBBQUHCAOgFDASDBBiMWY4MTE0Njg1ZWRjOGMzMCAGDCsGAQQBgqRkxihAAQQQMA4CAQYEB2FjbWUtZGEEADAKBggqhkjOPQQDAgNJADBGAiEAjCyYwof+mPJEMECc6J7BIkj7WSkob9OD46KncxOPaV0CIQDj/beqWvW9JjU0mOh9NODHOO4tM5hkCShozHc0SAD1FA==" duration=3.770787ms duration-ns=3770787 fields.time="2026-09-07T01:55:42-04:00" issuer="mTLS ACME CA Intermediate CA" method=POST name=ca nonce=REptNTlCTTd6QWJtV3pwaVVSWXEyRzJKekx3VTEzM3U path=/acme/acme-da/certificate/h9YCkdRz1iCZ13FkbClrEBCDhRakl7eO protocol=HTTP/1.1 provisioner=acme-da public-key="ECDSA P-256" referer= remote-address=127.0.0.1 request-id=e592149d-45ad-43ee-9f81-6a27fcb94678 sans="map[]" serial=76085310278373732917148320914805975329 size=1478 status=200 subject=b1f8114685edc8c3 user-agent=golang.org/x/crypto/acme@v0.50.0 user-id= valid-from="2026-09-07T05:54:39Z" valid-to="2026-09-08T05:55:39Z"

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

```bash
cd tpm/testing/go_client/

$ go run main.go -issuedCertFile=../../certs/cert.pem -tlsTestServerCA=../../certs/tls-root-ca.crt -tpmKeyFilePEM=../../certs/tpmkey.pem

Using mTLS certificate to make mTLS call
client connected to server with cn CN=server.domain.com,OU=Enterprise,O=Google,C=US
client connected with server Issuer: CN=TLS Root CA,OU=Enterprise,O=Google,C=US 
client successfully verified server certificate.server Response: ok
```

#### Openssl

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


#### Python

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
