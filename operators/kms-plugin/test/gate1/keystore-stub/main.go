// Command keystore-stub serves Cloud KMS's gRPC contract for Gate 1's plumbing half.
//
// WHAT THIS IS AND IS NOT.
//
// Gate 1 (docs/runbooks/kms-v2-gate-1-integration.md) exists to prove the whole chain
// against the REAL key authority. That needs a GCP project, a CryptoKey and a
// federated identity. Until those exist, the half of the gate that unit tests cannot
// reach is still worth running: a real kubelet, a real API server, a real static pod,
// a real unix socket, real etcd, and a real restart.
//
// So this stands in for Cloud KMS and NOTHING ELSE. Every other component in the run
// is the production one — including the production client, which talks to this over
// gRPC exactly as it would to Google.
//
// IT IMPLEMENTS THE GENERATED SERVER INTERFACE, not a hand-rolled approximation. That
// is the point: kmspb.KeyManagementServiceServer is the same contract the client
// compiles against, so a field this stub forgets is a compile error rather than a
// difference discovered on a real project. The previous version of this file served
// Infisical's REST API; the switch to GCP (ADR-100) made it obsolete.
//
// WHAT IT CANNOT PROVE, and what therefore still waits for a real project: IAM —
// key-level encrypt/decrypt, cross-key denial, admin-action denial — Workload Identity
// Federation, Google's rate limits, latency and error shapes. No stand-in can.
package main

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"flag"
	"fmt"
	"hash/crc32"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"cloud.google.com/go/kms/apiv1/kmspb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

func crc32c(b []byte) int64 {
	return int64(crc32.Checksum(b, crc32.MakeTable(crc32.Castagnoli)))
}

// version is one CryptoKeyVersion. PREVIOUS VERSIONS ARE RETAINED, which is the
// behaviour the whole rotation design rests on: Cloud KMS keeps non-destroyed versions
// available for decryption, so data written before a rotation keeps unwrapping.
type version struct {
	n    int
	aead cipher.AEAD
}

type server struct {
	kmspb.UnimplementedKeyManagementServiceServer

	mu        sync.Mutex
	cryptoKey string // the full resource name this stub answers for
	versions  map[int]*version
	primary   int
}

func newServer(cryptoKey string) *server {
	s := &server{cryptoKey: cryptoKey, versions: map[int]*version{}}
	s.rotate()
	return s
}

func (s *server) versionName(n int) string {
	return fmt.Sprintf("%s/cryptoKeyVersions/%d", s.cryptoKey, n)
}

func (s *server) rotate() int {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		panic(err)
	}
	block, err := aes.NewCipher(raw)
	if err != nil {
		panic(err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		panic(err)
	}
	s.primary++
	s.versions[s.primary] = &version{n: s.primary, aead: gcm}
	return s.primary
}

// GetCryptoKey IS DENIED, deliberately, and this is the single most important thing
// this stub refuses to be helpful about.
//
// `cloudkms.cryptoKeys.get` is NOT carried by
// roles/cloudkms.cryptoKeyEncrypterDecrypter, which is the one role the plugin's
// identity holds. So on a correctly scoped key this call is PERMISSION_DENIED, and a
// stub that answered it would let the plugin go back to reading CryptoKey.Primary,
// pass the gate, and then fail on the first real project with an IAM error -- the
// exact class of failure a stub exists to prevent, not to hide.
//
// The plugin discovers the active version by encrypting against the parent key and
// reading EncryptResponse.Name instead. See Encrypt below.
func (s *server) GetCryptoKey(_ context.Context, _ *kmspb.GetCryptoKeyRequest) (*kmspb.CryptoKey, error) {
	log.Printf("DENIED GetCryptoKey: the plugin's identity does not hold " +
		"cloudkms.cryptoKeys.get. If the plugin needs this call, that is the finding.")
	return nil, status.Error(codes.PermissionDenied,
		"cloudkms.cryptoKeys.get denied: roles/cloudkms.cryptoKeyEncrypterDecrypter does not "+
			"carry it")
}

// resolveTarget interprets EncryptRequest.Name, which may be the parent CryptoKey or
// one of its CryptoKeyVersions. It returns 0 for the parent, meaning "server selects".
func (s *server) resolveTarget(name string) (int, error) {
	if name == s.cryptoKey {
		return 0, nil
	}
	prefix := s.cryptoKey + "/cryptoKeyVersions/"
	if !strings.HasPrefix(name, prefix) {
		return 0, status.Errorf(codes.NotFound, "this stub serves only %s and its versions, not %s",
			s.cryptoKey, name)
	}
	n, err := strconv.Atoi(strings.TrimPrefix(name, prefix))
	if err != nil || n < 1 {
		return 0, status.Errorf(codes.InvalidArgument, "%q does not name a version", name)
	}
	return n, nil
}

// Encrypt wraps under the version it is ADDRESSED TO and reports which one that was.
//
// The version is prefixed into the ciphertext so Decrypt can resolve it without being
// told, which is what Cloud KMS does internally and what lets the plugin's Unwrap take
// no version argument.
func (s *server) Encrypt(_ context.Context, req *kmspb.EncryptRequest) (*kmspb.EncryptResponse, error) {
	// THE REQUEST NAMES A CRYPTOKEYVERSION, and this stub honours it exactly.
	//
	// EncryptRequest.Name accepts either the parent CryptoKey or a CryptoKeyVersion, and
	// the plugin names the version deliberately: changing a key's primary is eventually
	// consistent, so letting the server pick lets the status path and the encrypt path
	// observe different versions, which the API server treats as an unhealthy provider.
	//
	// So a stub that quietly encrypted under its own primary regardless of what was
	// asked would let that whole mechanism be wrong and still pass the gate. Both forms
	// are accepted, because the contract permits both -- but the version form is used as
	// given.
	requested, err := s.resolveTarget(req.GetName())
	if err != nil {
		return nil, err
	}
	plaintext := req.GetPlaintext()

	// THE INTEGRITY CONTRACT IS HONOURED, not ignored.
	//
	// A stub that accepted any checksum would let the plugin's CRC32C handling be
	// wrong and still pass the gate — and a silently corrupted wrapped DEK is
	// unrecoverable, so that is the last thing this run should be blind to.
	verified := false
	if c := req.GetPlaintextCrc32C(); c != nil {
		if c.GetValue() != crc32c(plaintext) {
			return nil, status.Error(codes.InvalidArgument,
				"plaintext_crc32c does not match the plaintext")
		}
		verified = true
	}

	s.mu.Lock()
	n := requested
	if n == 0 {
		// The parent key was named, so the server selects: that is what Cloud KMS does,
		// and it is how the plugin discovers the active version without reading metadata.
		n = s.primary
	}
	v, known := s.versions[n]
	s.mu.Unlock()
	if !known {
		return nil, status.Errorf(codes.NotFound, "no CryptoKeyVersion %d exists", n)
	}

	nonce := make([]byte, v.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, status.Error(codes.Internal, "nonce")
	}
	sealed := v.aead.Seal(nil, nonce, plaintext, nil)
	blob := append([]byte(fmt.Sprintf("v%d:", n)), nonce...)
	blob = append(blob, sealed...)

	log.Printf("Encrypt: %d bytes under v%d (plaintext crc verified=%v)", len(plaintext), n, verified)
	return &kmspb.EncryptResponse{
		Name:                    s.versionName(n),
		Ciphertext:              blob,
		CiphertextCrc32C:        wrapperspb.Int64(crc32c(blob)),
		VerifiedPlaintextCrc32C: verified,
		ProtectionLevel:         kmspb.ProtectionLevel_SOFTWARE,
	}, nil
}

func (s *server) Decrypt(_ context.Context, req *kmspb.DecryptRequest) (*kmspb.DecryptResponse, error) {
	if req.GetName() != s.cryptoKey {
		return nil, status.Errorf(codes.NotFound, "this stub serves only %s", s.cryptoKey)
	}
	blob := req.GetCiphertext()
	if c := req.GetCiphertextCrc32C(); c != nil && c.GetValue() != crc32c(blob) {
		return nil, status.Error(codes.InvalidArgument,
			"ciphertext_crc32c does not match the ciphertext")
	}

	i := strings.IndexByte(string(blob), ':')
	if i < 2 || blob[0] != 'v' {
		return nil, status.Error(codes.InvalidArgument, "ciphertext carries no version prefix")
	}
	n, err := strconv.Atoi(string(blob[1:i]))
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "unparseable version prefix")
	}
	s.mu.Lock()
	v, ok := s.versions[n]
	primary := s.primary
	s.mu.Unlock()
	if !ok {
		return nil, status.Errorf(codes.FailedPrecondition,
			"no material retained for version %d", n)
	}

	rest := blob[i+1:]
	ns := v.aead.NonceSize()
	if len(rest) < ns {
		return nil, status.Error(codes.InvalidArgument, "truncated ciphertext")
	}
	plain, err := v.aead.Open(nil, rest[:ns], rest[ns:], nil)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "decryption failed")
	}

	log.Printf("Decrypt: %d bytes under v%d", len(plain), n)
	return &kmspb.DecryptResponse{
		Plaintext:       plain,
		PlaintextCrc32C: wrapperspb.Int64(crc32c(plain)),
		UsedPrimary:     n == primary,
		ProtectionLevel: kmspb.ProtectionLevel_SOFTWARE,
	}, nil
}

func main() {
	addr := flag.String("addr", ":8200", "gRPC listen address")
	admin := flag.String("admin-addr", ":8201", "HTTP address for the rotate hook")
	cryptoKey := flag.String("crypto-key",
		"projects/gate1/locations/global/keyRings/gate1/cryptoKeys/gate1-key",
		"the CryptoKey resource name this stub answers for")
	flag.Parse()

	s := newServer(*cryptoKey)

	// ROTATION IS AN ADMIN ACTION ON A SEPARATE PORT, and that separation is not
	// decoration. ADR-100 forbids the plugin any permission to rotate, so the gate
	// must be able to rotate WITHOUT going through the gRPC surface the plugin talks
	// to. A rotate RPC on the KMS port would let a mistaken plugin trigger one and the
	// gate would not notice.
	go func() {
		mux := http.NewServeMux()
		mux.HandleFunc("/rotate", func(w http.ResponseWriter, _ *http.Request) {
			s.mu.Lock()
			n := s.rotate()
			s.mu.Unlock()
			log.Printf("ROTATED: primary is now v%d; previous versions retained", n)
			fmt.Fprintf(w, "v%d\n", n)
		})
		log.Printf("admin (rotate) on %s", *admin)
		_ = http.ListenAndServe(*admin, mux)
	}()

	lis, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatalf("listening on %s: %v", *addr, err)
	}
	srv := grpc.NewServer()
	kmspb.RegisterKeyManagementServiceServer(srv, s)
	log.Printf("keystore-stub serving Cloud KMS gRPC on %s for %s at v%d",
		*addr, *cryptoKey, s.primary)
	log.Fatal(srv.Serve(lis))
}
