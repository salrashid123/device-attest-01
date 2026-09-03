module main

go 1.26.2

require (
	github.com/golang/glog v1.2.5
	github.com/google/uuid v1.6.0
	github.com/salrashid123/go_tpm_registrar/verifier v0.0.0
	golang.org/x/net v0.52.0 // indirect
	golang.org/x/text v0.36.0 // indirect
	google.golang.org/grpc v1.77.0
	google.golang.org/protobuf v1.36.11 // indirect
)

require (
	github.com/fxamacker/cbor/v2 v2.9.3
	github.com/google/go-attestation v0.6.0
	github.com/google/go-tpm v0.9.9-0.20260124013517-8f8f42cba0de
	github.com/google/go-tpm-tools v0.4.8
	github.com/smallstep/certinfo v1.16.0
	golang.org/x/crypto v0.50.0
)

require (
	github.com/GoogleCloudPlatform/confidential-space/server v0.0.0-20260307011055-895ec9019dd7 // indirect
	github.com/containerd/containerd v1.7.30 // indirect
	github.com/google/certificate-transparency-go v1.3.2 // indirect
	github.com/google/go-configfs-tsm v0.3.3 // indirect
	github.com/google/go-eventlog v0.0.3-0.20260305053119-5cd85087f9f9 // indirect
	github.com/google/go-sev-guest v0.14.1 // indirect
	github.com/google/go-tdx-guest v0.3.2-0.20250814004405-ffb0869e6f4d // indirect
	github.com/google/logger v1.1.1 // indirect
	github.com/x448/float16 v0.8.4 // indirect
	go.uber.org/multierr v1.11.0 // indirect
	golang.org/x/sys v0.43.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20251222181119-0a764e51fe1b // indirect
)

replace github.com/salrashid123/go_tpm_registrar/verifier => ./verifier
