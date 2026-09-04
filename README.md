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


and also described here:

- [Managed Device Attestation: ACME as the Bottom Turtle in Mobile Device Management](https://smallstep.com/blog/managed-device-attestation/)
- [ACME Device Attestation: The Modern Zero Trust Alternative to SCEP](https://www.bastionxp.com/blog/acme-device-attestation-vs-scep-zero-trust/)

In this specific setup, there are actually two distinct Certificate Authorities.

- `a.` Attestation CA on the Attestation server which verifies the client's TPM and issues an Attestation certificate to that device

- `b.` ACME server which runs its own CA to issue client certificates and is configured to accept device attestations signed by the Attestation CA.

For more general reading, see

- [ACME device attestation, smallstep and pkcs11: attezt](https://linderud.dev/blog/acme-device-attestation-smallstep-and-pkcs11-attezt/)
- [ACME Device Attestation: The Modern Zero Trust Alternative to SCEP](https://www.bastionxp.com/blog/acme-device-attestation-vs-scep-zero-trust/)


>> NOTE: this repo is *not* supported by google

### Step-CA Setup

To get started, you'll need golang and `smallstep-ca`, `smallstep-cli`


```bash
# First clear any existing smallstep config (if don't want to do this, make a backup of the `$HOME/.step` folder)
rm -rf $HOME/.step

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

#### Attestation Server

The remote attestation flow between the client and server are done over gRPC (you can use any other mechanism).  The specific flow and code is taken from:

* [TPM Remote Attestation protocol using go-tpm and gRPC](https://github.com/salrashid123/go_tpm_remote_attestation)

```bash
$ go run attestation_server/attestaion_server.go  \
        --ekrootCA swtpm/config/var/lib/swtpm-localca/issuercert.pem  \
        --expectedPCRMapSHA256=0:a0b5ff3383a1116bd7dc6df177c0c2d433b9ee1813ea958fa5d166a202cb2a85 \
        --v=40 -alsologtostderr

I0903 07:06:34.461035 1329350 attestaion_server.go:1013] Starting gRPC server on port :50051
I0903 07:06:53.807233 1329350 attestaion_server.go:172] ======= HealthCheck ========
I0903 07:06:53.810421 1329350 attestaion_server.go:206] ======= OfferEK ========
I0903 07:06:53.811045 1329350 attestaion_server.go:227] EK Certificate: 
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

I0903 07:06:53.811166 1329350 attestaion_server.go:258]      TPM Manufacturer id:00001014
I0903 07:06:53.811187 1329350 attestaion_server.go:261]      TPM Model swtpm
I0903 07:06:53.811205 1329350 attestaion_server.go:265]      TPM Version id:20240125
I0903 07:06:53.811230 1329350 attestaion_server.go:297]      TPM Family 2.0
I0903 07:06:53.811252 1329350 attestaion_server.go:298]      TPM Level 0
I0903 07:06:53.811272 1329350 attestaion_server.go:299]      TPM Revision 183
I0903 07:06:53.811308 1329350 attestaion_server.go:314]         EKCertificate ========
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

I0903 07:06:53.811390 1329350 attestaion_server.go:330]      EKCert  Issuer CN=swtpm-localca
I0903 07:06:53.811447 1329350 attestaion_server.go:331]      EKCert  IssuingCertificateURL []
I0903 07:06:53.811477 1329350 attestaion_server.go:332]      EKCert  SerialNumber 1237
I0903 07:06:53.811504 1329350 attestaion_server.go:334]     EkCert Public Key 
-----BEGIN PUBLIC KEY-----
MIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEAwJeerh1yB9bYbI/QZldk
Dl0esVUcoodQAox8sbGEaWImeaH75Xw3IkBegeU+SmXIWl5WeyyYSZ9x366pKGhp
zEAQjLnAdqI/mLU6mNCdGo2nL42xmdiuyTYOjcbkEba40QA6lvloKViIqnhBM1jv
m3G5yK+xdJDsktzg32ovx6Kg3aAEQk2PGBlorVi2Ha1ZoeafHst3DccYxQWD3qoQ
qIoF6EGPr1ANDa1rTpTxZglKPvNB7sukNRZ9rpIivuXQhuzFszZ3YPJptf3I4dpP
+XM+zd/LiznWabv4sTGThHfkcJq0mTxhvPA6uWQVq/87CXqIAM3cA7aLxKJ8LtbL
WQIDAQAB
-----END PUBLIC KEY-----

I0903 07:06:53.811545 1329350 attestaion_server.go:337]     Verifying EKCert
I0903 07:06:53.811789 1329350 attestaion_server.go:365]      EKCert Includes tcg-kp-EKCertificate ExtendedKeyUsage 2.23.133.8.1
I0903 07:06:53.812237 1329350 attestaion_server.go:394]     EKCert Verified
I0903 07:06:53.812271 1329350 attestaion_server.go:408] =============== end OfferEK ===============
I0903 07:06:53.863465 1329350 attestaion_server.go:413] ======= OfferAK ========
I0903 07:06:53.863736 1329350 attestaion_server.go:457]       ak public 
-----BEGIN PUBLIC KEY-----
MIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEAvWrjKuK4x68j+OndhvvR
kTC8pyVHGmh5FPdUNAa6mseof9psB5/WMQsQwtGTs9naXflR5ghMI1TnBl/l+qDf
J4vhaT4NtkDS7xBV9vI8hG5oj5J+o24zTMrU9DbBHrY4q651Opd8iMRa9vYSl4ZD
8pX7DlRPuqNdZhPNaALi2jquLc5JSIRT5spTtt7UYlomWvyT3VRCcrQKhI+O//U7
ZM0nWHEqpLNw/YV2kzskW9olpPbqVl65DB0DLMtqYJLfip1kLI7URv3cihzEFTN8
ARwOwclOvU0TOwyR3CjsnIDItv35SfFBTVM7Arl5CcuhrYya2t6ene4V2QCLkBhD
TQIDAQAB
-----END PUBLIC KEY-----

I0903 07:06:53.863870 1329350 attestaion_server.go:471] =============== end GetAK ===============
I0903 07:06:53.864727 1329350 attestaion_server.go:477] ======= GetMakeCredential ========
I0903 07:06:53.864764 1329350 attestaion_server.go:494] =============== end GetMakeCredential ===============
I0903 07:06:53.865392 1329350 attestaion_server.go:508]       Outbound Secret: uJqGf+nkk04Qhg1dtEj1njeyY9rY/r6QdRi6bK238rE=
I0903 07:06:53.876562 1329350 attestaion_server.go:526] ======= SetActivateCredential ========
I0903 07:06:53.876608 1329350 attestaion_server.go:556] =============== end SetActivateCredential ===============
I0903 07:06:53.877226 1329350 attestaion_server.go:561] ======= OfferQuote ========
I0903 07:06:53.877269 1329350 attestaion_server.go:586] =============== end OfferQuote ===============
I0903 07:06:53.887633 1329350 attestaion_server.go:593] ======= SetQuote ========
I0903 07:06:53.890621 1329350 attestaion_server.go:645]       quote-attested public 
-----BEGIN PUBLIC KEY-----
MIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEAvWrjKuK4x68j+OndhvvR
kTC8pyVHGmh5FPdUNAa6mseof9psB5/WMQsQwtGTs9naXflR5ghMI1TnBl/l+qDf
J4vhaT4NtkDS7xBV9vI8hG5oj5J+o24zTMrU9DbBHrY4q651Opd8iMRa9vYSl4ZD
8pX7DlRPuqNdZhPNaALi2jquLc5JSIRT5spTtt7UYlomWvyT3VRCcrQKhI+O//U7
ZM0nWHEqpLNw/YV2kzskW9olpPbqVl65DB0DLMtqYJLfip1kLI7URv3cihzEFTN8
ARwOwclOvU0TOwyR3CjsnIDItv35SfFBTVM7Arl5CcuhrYya2t6ene4V2QCLkBhD
TQIDAQAB
-----END PUBLIC KEY-----

I0903 07:06:53.890906 1329350 attestaion_server.go:675]      PCR: 0, verified: true value: a0b5ff3383a1116bd7dc6df177c0c2d433b9ee1813ea958fa5d166a202cb2a85
I0903 07:06:53.890937 1329350 attestaion_server.go:675]      PCR: 1, verified: true value: e50edb964f66a7417954b1506f78a49d62062228ce84ee0b4e7e3b0e19b64a69
I0903 07:06:53.890954 1329350 attestaion_server.go:675]      PCR: 2, verified: true value: 3d458cfe55cc03ea1f443f1562beec8df51c75e14a9fcf9a7234a13f198e7969
I0903 07:06:53.890966 1329350 attestaion_server.go:675]      PCR: 3, verified: true value: 3d458cfe55cc03ea1f443f1562beec8df51c75e14a9fcf9a7234a13f198e7969
I0903 07:06:53.890977 1329350 attestaion_server.go:675]      PCR: 4, verified: true value: a3358453a5148b4e3f4b96b006ae1761a2ce4aea75f6a13e10eb3e0903dfd6e2
I0903 07:06:53.890987 1329350 attestaion_server.go:675]      PCR: 5, verified: true value: 098a2ae2d1aabed3e346b9fef96ec64056ea4043514672243bbf40b7d0972302
I0903 07:06:53.890998 1329350 attestaion_server.go:675]      PCR: 6, verified: true value: 3d458cfe55cc03ea1f443f1562beec8df51c75e14a9fcf9a7234a13f198e7969
I0903 07:06:53.891008 1329350 attestaion_server.go:675]      PCR: 7, verified: true value: 0a3f60cea411388b09eac782999f5e62246ab5469f9047eb508aa22c4dcd2237
I0903 07:06:53.891019 1329350 attestaion_server.go:675]      PCR: 8, verified: true value: a775d521739876ecde2c17d0e856c584ec513e8758d9199a3d5c735836ba0ebe
I0903 07:06:53.891029 1329350 attestaion_server.go:675]      PCR: 9, verified: true value: 4a7254a1740444f04ec61cf3f8eb8ffb5dae2069b44ad900e894b34a07626b36
I0903 07:06:53.891041 1329350 attestaion_server.go:675]      PCR: 10, verified: true value: 0000000000000000000000000000000000000000000000000000000000000000
I0903 07:06:53.891052 1329350 attestaion_server.go:675]      PCR: 11, verified: true value: 0000000000000000000000000000000000000000000000000000000000000000
I0903 07:06:53.891066 1329350 attestaion_server.go:675]      PCR: 12, verified: true value: 0000000000000000000000000000000000000000000000000000000000000000
I0903 07:06:53.891079 1329350 attestaion_server.go:675]      PCR: 13, verified: true value: 0000000000000000000000000000000000000000000000000000000000000000
I0903 07:06:53.891088 1329350 attestaion_server.go:675]      PCR: 14, verified: true value: 306f9d8b94f17d93dc6e7cf8f5c79d652eb4c6c4d13de2dddc24af416e13ecaf
I0903 07:06:53.891101 1329350 attestaion_server.go:675]      PCR: 15, verified: true value: 0000000000000000000000000000000000000000000000000000000000000000
I0903 07:06:53.891111 1329350 attestaion_server.go:675]      PCR: 16, verified: true value: 0000000000000000000000000000000000000000000000000000000000000000
I0903 07:06:53.891122 1329350 attestaion_server.go:675]      PCR: 17, verified: true value: ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff
I0903 07:06:53.891134 1329350 attestaion_server.go:675]      PCR: 18, verified: true value: ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff
I0903 07:06:53.891151 1329350 attestaion_server.go:675]      PCR: 19, verified: true value: ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff
I0903 07:06:53.891165 1329350 attestaion_server.go:675]      PCR: 20, verified: true value: ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff
I0903 07:06:53.891176 1329350 attestaion_server.go:675]      PCR: 21, verified: true value: ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff
I0903 07:06:53.891187 1329350 attestaion_server.go:675]      PCR: 22, verified: true value: ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff
I0903 07:06:53.891196 1329350 attestaion_server.go:675]      PCR: 23, verified: true value: 0000000000000000000000000000000000000000000000000000000000000000
I0903 07:06:53.891207 1329350 attestaion_server.go:687]      quotes verified
I0903 07:06:53.892513 1329350 attestaion_server.go:716]      secureBoot State enabled: [true]
I0903 07:06:53.893064 1329350 attestaion_server.go:778] >>>>>>>>  DeviceSerial Number [78306e4e22081c08]
I0903 07:06:53.893108 1329350 attestaion_server.go:780]       verify quote, PCRs and secureBootState
I0903 07:06:53.894351 1329350 attestaion_server.go:936] Issued AK Certificate: 
-----BEGIN CERTIFICATE-----
MIIDXjCCAwOgAwIBAgIFAMcgsrEwCgYIKoZIzj0EAwIwWzELMAkGA1UEBhMCVVMx
DzANBgNVBAoMBkdvb2dsZTEdMBsGA1UECwwUQXR0ZXN0YXRpb24gVmVyaWZpZXIx
HDAaBgNVBAMME0F0dGVzdGF0aW9uIFJvb3QgQ0EwHhcNMjYwOTAzMTEwNjUzWhcN
MjcwOTAzMTEwNjUzWjAAMIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEA
vWrjKuK4x68j+OndhvvRkTC8pyVHGmh5FPdUNAa6mseof9psB5/WMQsQwtGTs9na
XflR5ghMI1TnBl/l+qDfJ4vhaT4NtkDS7xBV9vI8hG5oj5J+o24zTMrU9DbBHrY4
q651Opd8iMRa9vYSl4ZD8pX7DlRPuqNdZhPNaALi2jquLc5JSIRT5spTtt7UYlom
WvyT3VRCcrQKhI+O//U7ZM0nWHEqpLNw/YV2kzskW9olpPbqVl65DB0DLMtqYJLf
ip1kLI7URv3cihzEFTN8ARwOwclOvU0TOwyR3CjsnIDItv35SfFBTVM7Arl5Ccuh
rYya2t6ene4V2QCLkBhDTQIDAQABo4IBQjCCAT4wDgYDVR0PAQH/BAQDAgeAMBAG
A1UdJQQJMAcGBWeBBQgDMAwGA1UdEwEB/wQCMAAwHwYDVR0jBBgwFoAUUA0oLf1M
FqjzMvQhFZys3Xnv4jkwJwYDVR0gBCAwHjAIBgZngQULAQEwCAYGZ4EFCwECMAgG
BmeBBQsBAzCBwQYDVR0RBIG5MIG2oEwGCCsGAQUFBwgEoEAwPgYFZ4EFAQKENTAw
MDAxMDE0OjJmNmQ1MWRiNzczNmVjYjkyZGNkZTIyNzgwMzFjOGIxZWNjMzg3YjQ6
NGQ1oCAGCCsGAQUFBwgDoBQwEgwQNzgzMDZlNGUyMjA4MWMwOKREMEIxFjAUBgVn
gQUCARMLaWQ6MDAwMDEwMTQxEDAOBgVngQUCAhMFc3d0cG0xFjAUBgVngQUCAxML
aWQ6MjAyNDAxMjUwCgYIKoZIzj0EAwIDSQAwRgIhAL/Ewu59xyNAGZbAc0FXP8Rb
VdTYRKne8s8f7FONv568AiEAz8uI1GRAtmZW4ZWAQ4+yDBG83Jk1U5N7e6VyXufI
BsA=
-----END CERTIFICATE-----

Certificate:
    Data:
        Version: 3 (0x2)
        Serial Number: 3340808881 (0xc720b2b1)
        Signature Algorithm: ECDSA-SHA256
        Issuer: C=US,O=Google,OU=Attestation Verifier,CN=Attestation Root CA
        Validity
            Not Before: Sep 3 11:06:53 2026 UTC
            Not After : Sep 3 11:06:53 2027 UTC
        Subject:
        Subject Public Key Info:
            Public Key Algorithm: RSA
                Public-Key: (2048 bit)
                Modulus:
                    bd:6a:e3:2a:e2:b8:c7:af:23:f8:e9:dd:86:fb:d1:
                    91:30:bc:a7:25:47:1a:68:79:14:f7:54:34:06:ba:
                    9a:c7:a8:7f:da:6c:07:9f:d6:31:0b:10:c2:d1:93:
                    b3:d9:da:5d:f9:51:e6:08:4c:23:54:e7:06:5f:e5:
                    fa:a0:df:27:8b:e1:69:3e:0d:b6:40:d2:ef:10:55:
                    f6:f2:3c:84:6e:68:8f:92:7e:a3:6e:33:4c:ca:d4:
                    f4:36:c1:1e:b6:38:ab:ae:75:3a:97:7c:88:c4:5a:
                    f6:f6:12:97:86:43:f2:95:fb:0e:54:4f:ba:a3:5d:
                    66:13:cd:68:02:e2:da:3a:ae:2d:ce:49:48:84:53:
                    e6:ca:53:b6:de:d4:62:5a:26:5a:fc:93:dd:54:42:
                    72:b4:0a:84:8f:8e:ff:f5:3b:64:cd:27:58:71:2a:
                    a4:b3:70:fd:85:76:93:3b:24:5b:da:25:a4:f6:ea:
                    56:5e:b9:0c:1d:03:2c:cb:6a:60:92:df:8a:9d:64:
                    2c:8e:d4:46:fd:dc:8a:1c:c4:15:33:7c:01:1c:0e:
                    c1:c9:4e:bd:4d:13:3b:0c:91:dc:28:ec:9c:80:c8:
                    b6:fd:f9:49:f1:41:4d:53:3b:02:b9:79:09:cb:a1:
                    ad:8c:9a:da:de:9e:9d:ee:15:d9:00:8b:90:18:43:
                    4d
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
                Permanent Identifier: 78306e4e22081c08
                TPM Manufacturer: id:00001014
                TPM Model: swtpm
                TPM Version: id:20240125
    Signature Algorithm: ECDSA-SHA256
         30:46:02:21:00:bf:c4:c2:ee:7d:c7:23:40:19:96:c0:73:41:
         57:3f:c4:5b:55:d4:d8:44:a9:de:f2:cf:1f:ec:53:8d:bf:9e:
         bc:02:21:00:cf:cb:88:d4:64:40:b6:66:56:e1:95:80:43:8f:
         b2:0c:11:bc:dc:99:35:53:93:7b:7b:a5:72:5e:e7:c8:06:c0

I0903 07:06:53.894512 1329350 attestaion_server.go:942] =============== Attestation x509 Sent ===============
```

### Client

Start the Client

```bash
$ go run client/client.go -host 127.0.0.1:50051  \
  --tpm-path="127.0.0.1:2321" --stepCACertPath=$HOME/.step/certs/root_ca.crt \
  --eventLogPath=swtpm/binary_bios_measurements  \
  --v=10 -alsologtostderr


I0903 07:06:53.799401 1329799 client.go:123] =============== HealthCheck ===============
I0903 07:06:53.807652 1329799 client.go:139] RPC HealthChekStatus: SERVING
I0903 07:06:53.807711 1329799 client.go:155] Opening swtpm socket
I0903 07:06:53.808877 1329799 client.go:197] Manufacturer: IBM
I0903 07:06:53.808935 1329799 client.go:198] VendorInfo: SW   TPM
I0903 07:06:53.808958 1329799 client.go:199] FirmwareVersionMajor: 8228
I0903 07:06:53.808981 1329799 client.go:200] FirmwareVersionMinor: 293
I0903 07:06:53.809848 1329799 client.go:210] EKCert Issuer: CN=swtpm-localca
I0903 07:06:53.809896 1329799 client.go:227] EKCert SerialNumber: 1237
I0903 07:06:53.809932 1329799 client.go:231] =============== OfferEK ===============
I0903 07:06:53.812529 1329799 client.go:240] Verified EK Cert
I0903 07:06:53.812570 1329799 client.go:242] =============== OfferAK ===============
I0903 07:06:53.858675 1329799 client.go:267] Creating AK CSR
I0903 07:06:53.862656 1329799 client.go:302] AK CSR 
-----BEGIN CERTIFICATE REQUEST-----
MIIC9TCCAd0CAQAwfzELMAkGA1UEBhMCVVMxEzARBgNVBAgTCkNhbGlmb3JuaWEx
FjAUBgNVBAcTDU1vdW50YWluIFZpZXcxEDAOBgNVBAoTB0FjbWUgQ28xEzARBgNV
BAsTCkVudGVycHJpc2UxHDAaBgNVBAMTE2F0dGVzdG9yLmRvbWFpbi5jb20wggEi
MA0GCSqGSIb3DQEBAQUAA4IBDwAwggEKAoIBAQC9auMq4rjHryP46d2G+9GRMLyn
JUcaaHkU91Q0Brqax6h/2mwHn9YxCxDC0ZOz2dpd+VHmCEwjVOcGX+X6oN8ni+Fp
Pg22QNLvEFX28jyEbmiPkn6jbjNMytT0NsEetjirrnU6l3yIxFr29hKXhkPylfsO
VE+6o11mE81oAuLaOq4tzklIhFPmylO23tRiWiZa/JPdVEJytAqEj47/9TtkzSdY
cSqks3D9hXaTOyRb2iWk9upWXrkMHQMsy2pgkt+KnWQsjtRG/dyKHMQVM3wBHA7B
yU69TRM7DJHcKOycgMi2/flJ8UFNUzsCuXkJy6GtjJra3p6d7hXZAIuQGENNAgMB
AAGgMTAvBgkqhkiG9w0BCQ4xIjAgMB4GA1UdEQQXMBWCE2F0dGVzdG9yLmRvbWFp
bi5jb20wDQYJKoZIhvcNAQELBQADggEBALMFVPyi0bn4bnQ4qrBF59vJyoWR8/cC
lWxQ0klvtpic9d4piMFBtVCykSlL72UByDoUg3kv14/TZp8v7jnUkAnNnrAsnTOm
uMb/wmPcfObyHSxN+14I298H9WEhn86Sorkrx/8eMAWCAB/K+sQG5BbXRLwxWZrM
fzRe7x/dSBVW0oeSfIBBL52eO6/7CHGp6/M9+Bn+a86g6qv/IMD6rv/Uc0v1m3EF
gYbk8kK0M7Knn8XQtz/QGYMEKEztpXfNkUYWXUc4RcrmxSyYjAzDc6gi1/7GM5Q9
yMYU4zr3nhkNwFGThpde+r5agLmClCgSMaoQsQEJKKNXyUcrcbSqSik=
-----END CERTIFICATE REQUEST-----

I0903 07:06:53.864191 1329799 client.go:313] Verified AK 
I0903 07:06:53.864258 1329799 client.go:315] =============== GetMakeCredential ===============
I0903 07:06:53.875832 1329799 client.go:344] EncryptedCredentials Secret uJqGf+nkk04Qhg1dtEj1njeyY9rY/r6QdRi6bK238rE=
I0903 07:06:53.875918 1329799 client.go:346] =============== SetActivateCredential ===============
I0903 07:06:53.876824 1329799 client.go:355] SetActivateCredential complete 
I0903 07:06:53.876890 1329799 client.go:357] =============== OfferQuote ===============
I0903 07:06:53.877785 1329799 client.go:364] OfferQuote complete 
I0903 07:06:53.877852 1329799 client.go:366] =============== SetQuote ===============

I0903 07:06:53.895897 1329799 client.go:412] Issued AK Certificate: 
-----BEGIN CERTIFICATE-----
MIIDXjCCAwOgAwIBAgIFAMcgsrEwCgYIKoZIzj0EAwIwWzELMAkGA1UEBhMCVVMx
DzANBgNVBAoMBkdvb2dsZTEdMBsGA1UECwwUQXR0ZXN0YXRpb24gVmVyaWZpZXIx
HDAaBgNVBAMME0F0dGVzdGF0aW9uIFJvb3QgQ0EwHhcNMjYwOTAzMTEwNjUzWhcN
MjcwOTAzMTEwNjUzWjAAMIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEA
vWrjKuK4x68j+OndhvvRkTC8pyVHGmh5FPdUNAa6mseof9psB5/WMQsQwtGTs9na
XflR5ghMI1TnBl/l+qDfJ4vhaT4NtkDS7xBV9vI8hG5oj5J+o24zTMrU9DbBHrY4
q651Opd8iMRa9vYSl4ZD8pX7DlRPuqNdZhPNaALi2jquLc5JSIRT5spTtt7UYlom
WvyT3VRCcrQKhI+O//U7ZM0nWHEqpLNw/YV2kzskW9olpPbqVl65DB0DLMtqYJLf
ip1kLI7URv3cihzEFTN8ARwOwclOvU0TOwyR3CjsnIDItv35SfFBTVM7Arl5Ccuh
rYya2t6ene4V2QCLkBhDTQIDAQABo4IBQjCCAT4wDgYDVR0PAQH/BAQDAgeAMBAG
A1UdJQQJMAcGBWeBBQgDMAwGA1UdEwEB/wQCMAAwHwYDVR0jBBgwFoAUUA0oLf1M
FqjzMvQhFZys3Xnv4jkwJwYDVR0gBCAwHjAIBgZngQULAQEwCAYGZ4EFCwECMAgG
BmeBBQsBAzCBwQYDVR0RBIG5MIG2oEwGCCsGAQUFBwgEoEAwPgYFZ4EFAQKENTAw
MDAxMDE0OjJmNmQ1MWRiNzczNmVjYjkyZGNkZTIyNzgwMzFjOGIxZWNjMzg3YjQ6
NGQ1oCAGCCsGAQUFBwgDoBQwEgwQNzgzMDZlNGUyMjA4MWMwOKREMEIxFjAUBgVn
gQUCARMLaWQ6MDAwMDEwMTQxEDAOBgVngQUCAhMFc3d0cG0xFjAUBgVngQUCAxML
aWQ6MjAyNDAxMjUwCgYIKoZIzj0EAwIDSQAwRgIhAL/Ewu59xyNAGZbAc0FXP8Rb
VdTYRKne8s8f7FONv568AiEAz8uI1GRAtmZW4ZWAQ4+yDBG83Jk1U5N7e6VyXufI
BsA=
-----END CERTIFICATE-----

Certificate:
    Data:
        Version: 3 (0x2)
        Serial Number: 3340808881 (0xc720b2b1)
        Signature Algorithm: ECDSA-SHA256
        Issuer: C=US,O=Google,OU=Attestation Verifier,CN=Attestation Root CA
        Validity
            Not Before: Sep 3 11:06:53 2026 UTC
            Not After : Sep 3 11:06:53 2027 UTC
        Subject:
        Subject Public Key Info:
            Public Key Algorithm: RSA
                Public-Key: (2048 bit)
                Modulus:
                    bd:6a:e3:2a:e2:b8:c7:af:23:f8:e9:dd:86:fb:d1:
                    91:30:bc:a7:25:47:1a:68:79:14:f7:54:34:06:ba:
                    9a:c7:a8:7f:da:6c:07:9f:d6:31:0b:10:c2:d1:93:
                    b3:d9:da:5d:f9:51:e6:08:4c:23:54:e7:06:5f:e5:
                    fa:a0:df:27:8b:e1:69:3e:0d:b6:40:d2:ef:10:55:
                    f6:f2:3c:84:6e:68:8f:92:7e:a3:6e:33:4c:ca:d4:
                    f4:36:c1:1e:b6:38:ab:ae:75:3a:97:7c:88:c4:5a:
                    f6:f6:12:97:86:43:f2:95:fb:0e:54:4f:ba:a3:5d:
                    66:13:cd:68:02:e2:da:3a:ae:2d:ce:49:48:84:53:
                    e6:ca:53:b6:de:d4:62:5a:26:5a:fc:93:dd:54:42:
                    72:b4:0a:84:8f:8e:ff:f5:3b:64:cd:27:58:71:2a:
                    a4:b3:70:fd:85:76:93:3b:24:5b:da:25:a4:f6:ea:
                    56:5e:b9:0c:1d:03:2c:cb:6a:60:92:df:8a:9d:64:
                    2c:8e:d4:46:fd:dc:8a:1c:c4:15:33:7c:01:1c:0e:
                    c1:c9:4e:bd:4d:13:3b:0c:91:dc:28:ec:9c:80:c8:
                    b6:fd:f9:49:f1:41:4d:53:3b:02:b9:79:09:cb:a1:
                    ad:8c:9a:da:de:9e:9d:ee:15:d9:00:8b:90:18:43:
                    4d
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
                Permanent Identifier: 78306e4e22081c08
                TPM Manufacturer: id:00001014
                TPM Model: swtpm
                TPM Version: id:20240125
    Signature Algorithm: ECDSA-SHA256
         30:46:02:21:00:bf:c4:c2:ee:7d:c7:23:40:19:96:c0:73:41:
         57:3f:c4:5b:55:d4:d8:44:a9:de:f2:cf:1f:ec:53:8d:bf:9e:
         bc:02:21:00:cf:cb:88:d4:64:40:b6:66:56:e1:95:80:43:8f:
         b2:0c:11:bc:dc:99:35:53:93:7b:7b:a5:72:5e:e7:c8:06:c0

I0903 07:06:53.896016 1329799 client.go:414] =============== Create new Key ===============
I0903 07:06:53.896076 1329799 client.go:429] Extracted Permanent Identfier: 78306e4e22081c08
I0903 07:06:53.896136 1329799 client.go:443] Extracted HardwareSerialNumber: 00001014:2f6d51db7736ecb92dcde2278031c8b1ecc387b4:4d5
I0903 07:06:53.896195 1329799 client.go:445]      Starting ACME Key generation
I0903 07:06:53.919026 1329799 client.go:492] Successfully registered ACME account.
I0903 07:06:53.926661 1329799 client.go:501] Order created. URI: https://ca.domain.com:8443/acme/acme-da/order/94i1OzPjZ1XYFmOJFm1TOWw6oFxA048z
I0903 07:06:53.930222 1329799 client.go:526] Fulfill challenge token: 4Wej4rVMvvpJ6dKBapQxINjKk7A1YMhO
I0903 07:06:53.930313 1329799 client.go:528] =============== Create New Key and set challengToken ===============
I0903 07:06:53.930392 1329799 client.go:538] KEYAUTH: 4Wej4rVMvvpJ6dKBapQxINjKk7A1YMhO.jwH0WiMyKtVXT_mPwZNvXvIAXOlkMuoa22kIHHXgAKM
I0903 07:06:53.930456 1329799 client.go:540] Create a TPM based key
I0903 07:06:53.939715 1329799 client.go:583] Generated ECC Public 
-----BEGIN PUBLIC KEY-----
MFkwEwYHKoZIzj0CAQYIKoZIzj0DAQcDQgAEdA6gq/dsMl5WFjSOz7fLqPscUaaj
mYqeA7stphEoV7F3J6158OjQoCIpkjGHDMj/I5kkWa6st89p/ENd15Fg/Q==
-----END PUBLIC KEY-----

I0903 07:06:56.941416 1329799 client.go:640] started server accepting challenge
I0903 07:06:56.960297 1329799 client.go:653] Waiting for order readiness validation...
I0903 07:06:56.971294 1329799 client.go:707] Finalizing order with CSR...

I0903 07:06:56.991955 1329799 client.go:778] Acme Root Certificate: 
Certificate:
    Data:
        Version: 3 (0x2)
        Serial Number: 125972208405043382334007578134251341686 (0x5ec55d65ae75724727e5874d8a548776)
        Signature Algorithm: ECDSA-SHA256
        Issuer: O=mTLS ACME CA,CN=mTLS ACME CA Root CA
        Validity
            Not Before: Sep 2 18:55:38 2026 UTC
            Not After : Aug 30 18:55:38 2036 UTC
        Subject: O=mTLS ACME CA,CN=mTLS ACME CA Root CA
        Subject Public Key Info:
            Public Key Algorithm: ECDSA
                Public-Key: (256 bit)
                X:
                    93:b6:2f:da:e9:93:c9:85:53:98:e6:03:a7:c8:29:
                    fd:b9:99:e7:b5:6d:85:a5:e7:8f:ff:97:e1:09:a8:
                    07:b8
                Y:
                    72:63:51:f1:0d:38:76:2a:bb:7c:d3:80:1a:cf:25:
                    ec:ab:8b:9d:80:1e:2a:49:ef:a5:22:6a:02:ac:2a:
                    31:3a
                Curve: P-256
        X509v3 extensions:
            X509v3 Key Usage: critical
                Certificate Sign, CRL Sign
            X509v3 Basic Constraints: critical
                CA:TRUE, pathlen:1
            X509v3 Subject Key Identifier:
                FC:0B:9F:E7:87:AC:1B:B0:52:47:E5:BA:8B:E2:FE:D0:6B:A0:0C:1D
    Signature Algorithm: ECDSA-SHA256
         30:46:02:21:00:d9:4c:45:29:74:14:a1:5a:a8:d0:ca:70:6f:
         ac:12:f9:f5:ee:71:c3:86:d5:07:51:72:99:84:3d:d9:ce:ee:
         17:02:21:00:c2:7c:ae:f2:d9:fd:9b:cf:25:4a:32:ef:e0:4f:
         cb:b0:ff:d3:0e:fc:de:48:eb:56:e2:d5:c6:92:5e:dd:89:07

I0903 07:06:56.992089 1329799 client.go:780] Printing issued certificate chain
I0903 07:06:56.992675 1329799 client.go:796] Certificate: 
Certificate:
    Data:
        Version: 3 (0x2)
        Serial Number: 328973937267744361549152244269556867569 (0xf77e135f278a14996fa9aeb04f1aa1f1)
        Signature Algorithm: ECDSA-SHA256
        Issuer: O=mTLS ACME CA,CN=mTLS ACME CA Intermediate CA
        Validity
            Not Before: Sep 3 11:05:53 2026 UTC
            Not After : Sep 4 11:06:53 2026 UTC
        Subject: CN=78306e4e22081c08    <<<<<<<<<<<<<<<<<<<<<<<<<<<<<<<<<<<<<<<<<<<<<<<<<<
        Subject Public Key Info:
            Public Key Algorithm: ECDSA
                Public-Key: (256 bit)
                X:
                    74:0e:a0:ab:f7:6c:32:5e:56:16:34:8e:cf:b7:cb:
                    a8:fb:1c:51:a6:a3:99:8a:9e:03:bb:2d:a6:11:28:
                    57:b1
                Y:
                    77:27:ad:79:f0:e8:d0:a0:22:29:92:31:87:0c:c8:
                    ff:23:99:24:59:ae:ac:b7:cf:69:fc:43:5d:d7:91:
                    60:fd
                Curve: P-256
        X509v3 extensions:
            X509v3 Key Usage: critical
                Digital Signature
            X509v3 Extended Key Usage:
                Client Authentication   <<<<<<<<<<<<<<<<<<<<<<<<<<<<<<<<<<<<<<<<<<<<<<<<<<
            X509v3 Subject Key Identifier:
                9B:9C:04:FE:EF:5D:B2:B7:9B:18:A4:6A:6A:62:26:20:26:97:77:29
            X509v3 Authority Key Identifier:
                04:E5:5E:AB:D3:45:18:D8:5A:B2:71:AD:9E:C1:71:C5:0D:E7:8A:5D
            X509v3 Subject Alternative Name:
                Permanent Identifier: 78306e4e22081c08   <<<<<<<<<<<<<<<<<<<<<<<<<<<<<<<<<<<<<<<<<<<<<<<<<<
            X509v3 Step Provisioner:     <<<<<<<<<<<<<<<<<<<<<<<<<<<<<<<<<<<<<<<<<<<<<<<<<<
                Type: ACME
                Name: acme-da
    Signature Algorithm: ECDSA-SHA256
         30:45:02:20:4b:54:a9:8b:dd:1c:ab:27:e2:6c:0e:08:97:f9:
         1f:d4:11:5e:13:dc:9d:61:1a:e9:4b:a9:2a:06:56:4f:8f:4d:
         02:21:00:d7:01:05:e9:4c:e6:75:37:4f:1f:81:63:11:c0:79:
         8f:a2:4c:ce:67:6a:f2:ca:d7:90:27:81:e3:b0:ab:55:25

I0903 07:06:56.993236 1329799 client.go:796] Certificate: 
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

I0903 07:06:56.993339 1329799 client.go:799] Successfully downloaded and saved cert and key
```


The step-ca logs also chronicles the provisioning flows

### ACME Server

```bash
$ step-ca

2026/09/03 07:04:58 Building new tls configuration using step-ca x509 Signer Interface
2026/09/03 07:04:58 Starting Smallstep CA/0.30.2 (linux/amd64)
2026/09/03 07:04:58 Documentation: https://u.step.sm/docs/ca
2026/09/03 07:04:58 Community Discord: https://u.step.sm/discord
2026/09/03 07:04:58 Config file: /home/srashid/.step/config/ca.json
2026/09/03 07:04:58 The primary server URL is https://ca.domain.com:8443
2026/09/03 07:04:58 Root certificates are available at https://ca.domain.com:8443/roots.pem
2026/09/03 07:04:58 X.509 Root Fingerprint: 66731a27366f96349726fbdc6c060dd0ee3867444f1e31484c94918f5e324c98
2026/09/03 07:04:58 Serving HTTPS on 127.0.0.1:8443 ...
INFO[0118]                                               duration="301.998µs" duration-ns=301998 fields.time="2026-09-03T07:06:53-04:00" method=GET name=ca path=/acme/acme-da/directory protocol=HTTP/1.1 referer= remote-address=127.0.0.1 request-id=a275412f-d829-41d6-89dc-6e787ee17d05 response="{\"newNonce\":\"https://ca.domain.com:8443/acme/acme-da/new-nonce\",\"newAccount\":\"https://ca.domain.com:8443/acme/acme-da/new-account\",\"newOrder\":\"https://ca.domain.com:8443/acme/acme-da/new-order\",\"revokeCert\":\"https://ca.domain.com:8443/acme/acme-da/revoke-cert\",\"keyChange\":\"https://ca.domain.com:8443/acme/acme-da/key-change\"}" size=327 status=200 user-agent=golang.org/x/crypto/acme@v0.50.0 user-id=
INFO[0118]                                               duration=9.635594ms duration-ns=9635594 fields.time="2026-09-03T07:06:53-04:00" method=HEAD name=ca nonce=MlBOS2Y5clZHYzBzZVJsUzV3SHlFSGdyVmN6UXFtN3o path=/acme/acme-da/new-nonce protocol=HTTP/1.1 referer= remote-address=127.0.0.1 request-id=b6fdf1f7-e54d-4fed-8f88-a2a50756b0b1 size=0 status=200 user-agent=golang.org/x/crypto/acme@v0.50.0 user-id=
INFO[0118]                                               duration=5.653016ms duration-ns=5653016 fields.time="2026-09-03T07:06:53-04:00" method=POST name=ca nonce=YkJFbktrYmo4bWxsUkZqS2lwSDhQZGYxS1dKOWJWcE4 path=/acme/acme-da/new-account protocol=HTTP/1.1 referer= remote-address=127.0.0.1 request-id=dd5ba362-d720-4f64-94b6-3a50f0ae7f02 response="{\"contact\":[\"mailto:admin@example.local\"],\"status\":\"valid\",\"orders\":\"https://ca.domain.com:8443/acme/acme-da/account/Tc0m5fJS5B6i8XTHTZSih6rQ6UKmKkn3/orders\"}" size=159 status=201 user-agent=golang.org/x/crypto/acme@v0.50.0 user-id=
INFO[0118]                                               duration=6.458617ms duration-ns=6458617 fields.time="2026-09-03T07:06:53-04:00" method=POST name=ca nonce=VGNsNHdYR3YzSXVIVnpDcjhKd0JlSGlIend6UmFpYmo path=/acme/acme-da/new-order protocol=HTTP/1.1 referer= remote-address=127.0.0.1 request-id=0dbd4680-c7b8-4421-af9b-949b4e7766ea response="{\"id\":\"94i1OzPjZ1XYFmOJFm1TOWw6oFxA048z\",\"status\":\"pending\",\"expires\":\"2026-09-04T11:06:53Z\",\"identifiers\":[{\"type\":\"permanent-identifier\",\"value\":\"78306e4e22081c08\"}],\"notBefore\":\"2026-09-03T11:05:53Z\",\"notAfter\":\"2026-09-04T11:06:53Z\",\"authorizations\":[\"https://ca.domain.com:8443/acme/acme-da/authz/x1LnsEAZBlvD4hKQm2gitpfGQgTkpWR7\"],\"finalize\":\"https://ca.domain.com:8443/acme/acme-da/order/94i1OzPjZ1XYFmOJFm1TOWw6oFxA048z/finalize\"}" size=439 status=201 user-agent=golang.org/x/crypto/acme@v0.50.0 user-id=
INFO[0118]                                               duration=2.565984ms duration-ns=2565984 fields.time="2026-09-03T07:06:53-04:00" method=POST name=ca nonce=TnJXRXVZVlFiOGJjbW9KMVBTZGRvdGdjYVB3dm5vTDM path=/acme/acme-da/authz/x1LnsEAZBlvD4hKQm2gitpfGQgTkpWR7 protocol=HTTP/1.1 referer= remote-address=127.0.0.1 request-id=926dac93-3fac-4da4-abbc-ef846007674c response="{\"identifier\":{\"type\":\"permanent-identifier\",\"value\":\"78306e4e22081c08\"},\"status\":\"pending\",\"challenges\":[{\"type\":\"device-attest-01\",\"status\":\"pending\",\"token\":\"4Wej4rVMvvpJ6dKBapQxINjKk7A1YMhO\",\"url\":\"https://ca.domain.com:8443/acme/acme-da/challenge/x1LnsEAZBlvD4hKQm2gitpfGQgTkpWR7/Gdhfa6qhKYDzm9zzoL4df2tHgWFbfGT6\"}],\"wildcard\":false,\"expires\":\"2026-09-04T11:06:53Z\"}" size=372 status=200 user-agent=golang.org/x/crypto/acme@v0.50.0 user-id=
INFO[0121]                                               duration=16.913999ms duration-ns=16913999 fields.time="2026-09-03T07:06:56-04:00" method=POST name=ca nonce=N3NFQ1VZSHZiejVWajcySlJ4TFZncG4xZlhoUUswQ3E path=/acme/acme-da/challenge/x1LnsEAZBlvD4hKQm2gitpfGQgTkpWR7/Gdhfa6qhKYDzm9zzoL4df2tHgWFbfGT6 protocol=HTTP/1.1 referer= remote-address=127.0.0.1 request-id=8644bbe0-fc5e-42ef-b45a-21b34e733ead response="{\"type\":\"device-attest-01\",\"status\":\"valid\",\"token\":\"4Wej4rVMvvpJ6dKBapQxINjKk7A1YMhO\",\"validated\":\"2026-09-03T11:06:56Z\",\"url\":\"https://ca.domain.com:8443/acme/acme-da/challenge/x1LnsEAZBlvD4hKQm2gitpfGQgTkpWR7/Gdhfa6qhKYDzm9zzoL4df2tHgWFbfGT6\"}" size=247 status=200 user-agent=golang.org/x/crypto/acme@v0.50.0 user-id=
INFO[0121]                                               duration=6.980882ms duration-ns=6980882 fields.time="2026-09-03T07:06:56-04:00" method=POST name=ca nonce=M3JUZ1JCZU9PY0gzSWRtUEt2eWxjaHNKeVR1bTcxeks path=/acme/acme-da/order/94i1OzPjZ1XYFmOJFm1TOWw6oFxA048z protocol=HTTP/1.1 referer= remote-address=127.0.0.1 request-id=343c78b0-f801-4951-ba00-1bc9c9bd4cf7 response="{\"id\":\"94i1OzPjZ1XYFmOJFm1TOWw6oFxA048z\",\"status\":\"ready\",\"expires\":\"2026-09-04T11:06:53Z\",\"identifiers\":[{\"type\":\"permanent-identifier\",\"value\":\"78306e4e22081c08\"}],\"notBefore\":\"2026-09-03T11:05:53Z\",\"notAfter\":\"2026-09-04T11:06:53Z\",\"authorizations\":[\"https://ca.domain.com:8443/acme/acme-da/authz/x1LnsEAZBlvD4hKQm2gitpfGQgTkpWR7\"],\"finalize\":\"https://ca.domain.com:8443/acme/acme-da/order/94i1OzPjZ1XYFmOJFm1TOWw6oFxA048z/finalize\"}" size=437 status=200 user-agent=golang.org/x/crypto/acme@v0.50.0 user-id=
INFO[0121]                                               duration=14.244591ms duration-ns=14244591 fields.time="2026-09-03T07:06:56-04:00" method=POST name=ca nonce=VGJ6V013YU1yN2ZBWUxhSnhnYWhIZTZBY3pTY29iWWs path=/acme/acme-da/order/94i1OzPjZ1XYFmOJFm1TOWw6oFxA048z/finalize protocol=HTTP/1.1 referer= remote-address=127.0.0.1 request-id=d4428e6f-072e-4f0c-9b53-3cbd76ff11c1 response="{\"id\":\"94i1OzPjZ1XYFmOJFm1TOWw6oFxA048z\",\"status\":\"valid\",\"expires\":\"2026-09-04T11:06:53Z\",\"identifiers\":[{\"type\":\"permanent-identifier\",\"value\":\"78306e4e22081c08\"}],\"notBefore\":\"2026-09-03T11:05:53Z\",\"notAfter\":\"2026-09-04T11:06:53Z\",\"authorizations\":[\"https://ca.domain.com:8443/acme/acme-da/authz/x1LnsEAZBlvD4hKQm2gitpfGQgTkpWR7\"],\"finalize\":\"https://ca.domain.com:8443/acme/acme-da/order/94i1OzPjZ1XYFmOJFm1TOWw6oFxA048z/finalize\",\"certificate\":\"https://ca.domain.com:8443/acme/acme-da/certificate/MkP6gMGb38G5P6kKikXR45MHr2cM8g6r\"}" size=538 status=200 user-agent=golang.org/x/crypto/acme@v0.50.0 user-id=
INFO[0121]                                               certificate="MIICEDCCAbagAwIBAgIRAPd+E18nihSZb6musE8aofEwCgYIKoZIzj0EAwIwPjEVMBMGA1UEChMMbVRMUyBBQ01FIENBMSUwIwYDVQQDExxtVExTIEFDTUUgQ0EgSW50ZXJtZWRpYXRlIENBMB4XDTI2MDkwMzExMDU1M1oXDTI2MDkwNDExMDY1M1owGzEZMBcGA1UEAxMQNzgzMDZlNGUyMjA4MWMwODBZMBMGByqGSM49AgEGCCqGSM49AwEHA0IABHQOoKv3bDJeVhY0js+3y6j7HFGmo5mKngO7LaYRKFexdyetefDo0KAiKZIxhwzI/yOZJFmurLfPafxDXdeRYP2jgbcwgbQwDgYDVR0PAQH/BAQDAgeAMBMGA1UdJQQMMAoGCCsGAQUFBwMCMB0GA1UdDgQWBBSbnAT+712yt5sYpGpqYiYgJpd3KTAfBgNVHSMEGDAWgBQE5V6r00UY2Fqyca2ewXHFDeeKXTArBgNVHREEJDAioCAGCCsGAQUFBwgDoBQwEgwQNzgzMDZlNGUyMjA4MWMwODAgBgwrBgEEAYKkZMYoQAEEEDAOAgEGBAdhY21lLWRhBAAwCgYIKoZIzj0EAwIDSAAwRQIgS1Spi90cqyfibA4Il/kf1BFeE9ydYRrpS6kqBlZPj00CIQDXAQXpTOZ1N08fgWMRwHmPokzOZ2ryyteQJ4HjsKtVJQ==" duration=3.324895ms duration-ns=3324895 fields.time="2026-09-03T07:06:56-04:00" issuer="mTLS ACME CA Intermediate CA" method=POST name=ca nonce=aVRvSkNkQXlEZWdEb0VGUGhNd2IzYkhWamZOT1JvNUU path=/acme/acme-da/certificate/MkP6gMGb38G5P6kKikXR45MHr2cM8g6r protocol=HTTP/1.1 provisioner=acme-da public-key="ECDSA P-256" referer= remote-address=127.0.0.1 request-id=2d17be85-eb54-4088-a29b-19734efa1b8d sans="map[]" serial=328973937267744361549152244269556867569 size=1478 status=200 subject=78306e4e22081c08 user-agent=golang.org/x/crypto/acme@v0.50.0 user-id= valid-from="2026-09-03T11:05:53Z" valid-to="2026-09-04T11:06:53Z"

```


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
