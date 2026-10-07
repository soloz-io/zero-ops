package keystore

import (
	"context"
	"fmt"
	"hash/crc32"
	"log"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	kms "cloud.google.com/go/kms/apiv1"
	"cloud.google.com/go/kms/apiv1/kmspb"
	"google.golang.org/api/option"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/soloz-io/zero-ops/operators/kms-plugin/internal/metrics"
)

// GCPKMS implements Store against Google Cloud KMS.
//
// WHAT THE PLUGIN'S IDENTITY IS ALLOWED TO DO, and nothing more:
//
//	cryptoKeyVersions.useToEncrypt   both granted AT THE CRYPTOKEY, not the project,
//	cryptoKeyVersions.useToDecrypt   and both carried by the single standard role
//	                                 roles/cloudkms.cryptoKeyEncrypterDecrypter
//
// TWO PERMISSIONS, ONE STANDARD ROLE, NO CUSTOM ROLE. That is a deliberate narrowing
// and it was once wider. An earlier version of this client read CryptoKey.Primary with
// GetCryptoKey, which needs `cloudkms.cryptoKeys.get` -- a permission the
// EncrypterDecrypter role does not carry. The comment here listed all three under that
// one role name, which was an IAM claim that did not hold: the plugin would have needed
// a custom role or a second binding, and the ADR's least-privilege claim would have
// been describing a policy nobody had written.
//
// It is resolved by REMOVING THE NEED rather than by documenting the extra grant. The
// active version is now discovered from an Encrypt against the parent key, whose
// response names the version that performed it -- see Active. That is upstream's
// approach (plugin/v2/plugin.go probes by encrypting a fixed "ping") and it is better
// here for a second reason: the version comes from the CRYPTO path itself, so it can
// never name a version that the metadata view knows about and the encrypt backend does
// not. Changing a primary version is eventually consistent, and a metadata read can be
// ahead of it.
//
// Explicitly NOT create, rotate, updatePrimaryVersion, disable, destroy, import or
// export, no IAM administration, not cryptoKeyVersions.list, and -- now -- not
// cryptoKeys.get. The two calls below are the whole surface, which is what makes that
// claim checkable.
//
// The missing `list` is deliberate and was once planned. A guard was designed here that
// would refuse to operate when CryptoKey.Primary was lower than the highest existing
// CryptoKeyVersion, on the reasoning that GCP itself holds the version history durably
// and a regressed primary would therefore be detectable without local state. It is not
// safe: CreateCryptoKeyVersion and UpdateCryptoKeyPrimaryVersion are separate calls
// (key_management_client.go:1049 and :1169), so `a newer version exists but is not yet
// primary` is the NORMAL state in the middle of every rotation -- and for HSM and
// external keys a new version MUST sit un-primary in PENDING_GENERATION
// (resources.pb.go:708) while its material is generated. The guard would have refused a
// state the API requires to exist, turning a routine rotation into a control plane that
// cannot encrypt. ADR-100 records where the no-reactivation invariant lives instead.
type GCPKMS struct {
	client    *kms.KeyManagementClient
	cryptoKey string // projects/P/locations/L/keyRings/R/cryptoKeys/K
	timeout   time.Duration
}

type GCPKMSOptions struct {
	// CryptoKey is the full resource name, and it is the PARENT key rather than a
	// version.
	//
	// The parent is what makes a rotation discoverable: the plugin reads
	// CryptoKey.Primary to learn which version is active. Encryption then names that
	// exact version (see Wrap), so configuring the parent and encrypting a version are
	// not in tension -- one is how rotation is observed, the other is how the
	// observation is used without a propagation race.
	CryptoKey string
	// Timeout bounds a single call. The plugin is in the API server's path, so a hung
	// request is an API server that cannot answer.
	Timeout time.Duration
}

// cryptoKeyPattern is the resource shape, checked rather than trusted. A malformed
// name produces a permission error from the API that reads as an IAM problem, and
// sends the reader to the wrong place.
var cryptoKeyPattern = regexp.MustCompile(
	`^projects/[^/]+/locations/[^/]+/keyRings/[^/]+/cryptoKeys/([^/]+)$`)

// versionFromName extracts the trailing version from a CryptoKeyVersion resource name:
//
//	projects/P/locations/L/keyRings/R/cryptoKeys/K/cryptoKeyVersions/7 -> 7
var versionFromName = regexp.MustCompile(`/cryptoKeyVersions/(\d+)$`)

// keyFromVersionName strips a version off a CryptoKeyVersion resource name, leaving the
// parent CryptoKey. Taken from the upstream plugin (plugin/v2/plugin.go,
// keyResourceRegEx) because decryption is addressed to the parent, not the version.
var keyFromVersionName = regexp.MustCompile(
	`^projects/[^/]+/locations/[^/]+/keyRings/[^/]+/cryptoKeys/[^/:]+`)

const defaultTimeout = 10 * time.Second

// probePlaintext is what Active encrypts to discover the active version.
//
// A FIXED, MEANINGLESS VALUE, as upstream uses. There is no oracle to worry about:
// Cloud KMS generates a fresh IV per Encrypt, so repeatedly encrypting a constant under
// one key reveals nothing. The ciphertext is discarded; only EncryptResponse.Name is
// kept.
var probePlaintext = []byte("kms-plugin probe")

func NewGCPKMS(o GCPKMSOptions) (Store, error) {
	if strings.TrimSpace(o.CryptoKey) == "" {
		return nil, fmt.Errorf("the key authority is not configured: missing KMS_CRYPTO_KEY")
	}
	if cryptoKeyPattern.FindStringSubmatch(o.CryptoKey) == nil {
		return nil, fmt.Errorf("KMS_CRYPTO_KEY=%q is not a CryptoKey resource name. Expected "+
			"projects/P/locations/L/keyRings/R/cryptoKeys/K -- and the PARENT key, not a "+
			"cryptoKeyVersions/N path: the parent is what lets this plugin read "+
			"CryptoKey.Primary and so observe a rotation at all. Encryption then names the "+
			"exact version it read, so nothing is lost by configuring the parent",
			o.CryptoKey)
	}

	// Credentials come from Application Default Credentials, which is how a Workload
	// Identity Federation credential configuration is consumed. ADR-100 prohibits a
	// long-lived service-account key; which external identity provides the short-lived
	// credential on a static pod is a Gate 3 question and is deliberately not decided
	// by this constructor.
	//
	// This is also the first platform delta from the upstream plugin, which obtains
	// credentials from google.DefaultClient and the GCE metadata server
	// (plugin/http_client.go). There is no GCE metadata server on a Hetzner node.
	var opts []option.ClientOption

	// THE ONLY WAY TO REACH ANYTHING BUT CLOUD KMS, and it is named to be unmistakable.
	//
	// Gate 1's plumbing half runs against a local stand-in so the whole chain -- a real
	// kubelet, a real API server, a real socket, real etcd, a real restart -- can be
	// exercised without a GCP project. That stand-in has to be reachable somehow.
	//
	// KMS_INSECURE_TEST_ENDPOINT, not KMS_ENDPOINT: a variable that merely redirects
	// the client is one typo in a ClusterClass away from a production control plane
	// encrypting against something that is not Cloud KMS, and the failure would look
	// like success. The name says what it costs, and the log line below says it out
	// loud on every start so it cannot be set quietly.
	//
	// It also disables authentication, because a stand-in has no Google credentials to
	// present. That is the second reason it must never be set in production: with it
	// set, no credential is required at all.
	if ep := strings.TrimSpace(os.Getenv("KMS_INSECURE_TEST_ENDPOINT")); ep != "" {
		log.Printf("WARNING: KMS_INSECURE_TEST_ENDPOINT=%s is set. This plugin is NOT talking to "+
			"Google Cloud KMS, and authentication is DISABLED. Correct only for ADR-100's Gate 1 "+
			"plumbing run; catastrophic anywhere else.", ep)
		opts = append(opts,
			option.WithEndpoint(ep),
			option.WithoutAuthentication(),
			option.WithGRPCDialOption(grpc.WithTransportCredentials(insecure.NewCredentials())),
		)
	}

	client, err := kms.NewKeyManagementClient(context.Background(), opts...)
	if err != nil {
		return nil, fmt.Errorf("creating the Cloud KMS client: %w", err)
	}

	timeout := o.Timeout
	if timeout == 0 {
		timeout = defaultTimeout
	}
	return &GCPKMS{client: client, cryptoKey: o.CryptoKey, timeout: timeout}, nil
}

// call bounds one request. The API server is waiting on the other side of the socket,
// so a request with no deadline is an API server with no deadline.
func (g *GCPKMS) call(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, g.timeout)
}

func parseVersion(resourceName string) (int, error) {
	m := versionFromName.FindStringSubmatch(resourceName)
	if m == nil {
		return 0, fmt.Errorf("%q does not end in /cryptoKeyVersions/<n>, so the version that "+
			"Cloud KMS used cannot be determined", resourceName)
	}
	v, err := strconv.Atoi(m[1])
	if err != nil || v < 1 {
		return 0, fmt.Errorf("%q names version %q, which is not a positive integer",
			resourceName, m[1])
	}
	return v, nil
}

// observe records one Cloud KMS round trip.
//
// Both the histogram and the failure counter, from one place, so a call cannot be timed
// without its outcome also being counted -- which is how a latency panel ends up looking
// healthy while every request fails fast.
func observe(operation string, started time.Time, err error) {
	result := "ok"
	if err != nil {
		result = "error"
		metrics.Failures.WithLabelValues(operation, metrics.Reason(err)).Inc()
	}
	metrics.Duration.WithLabelValues(operation, result).Observe(time.Since(started).Seconds())
}

func crc32c(b []byte) int64 {
	return int64(crc32.Checksum(b, crc32.MakeTable(crc32.Castagnoli)))
}

// Active reports the version currently primary for encryption.
//
// IT DISCOVERS THAT BY ENCRYPTING, not by reading metadata. EncryptRequest.Name accepts
// the parent CryptoKey -- "If a CryptoKey is specified, the server will use its primary
// version" -- and EncryptResponse.Name then states which version that was. The
// ciphertext is thrown away; the name is the whole point.
//
// TWO REASONS, and the second is the one that is easy to miss.
//
// Least privilege: GetCryptoKey needs `cloudkms.cryptoKeys.get`, which the standard
// EncrypterDecrypter role does not carry, so reading the primary that way would have
// required a custom role or a second binding for a value this call already returns.
//
// Consistency: changing a key's primary version is eventually consistent. A metadata
// read can report version N while the encrypt backend is still on N-1 -- and this
// plugin then pins its writes to the version it was told. Asking the CRYPTO path which
// version is primary means the answer cannot be ahead of the path that has to honour
// it.
//
// Upstream probes the same way, with a fixed "ping" plaintext (plugin/v2/plugin.go).
func (g *GCPKMS) Active(ctx context.Context) (KeyVersion, error) {
	// THE PARENT KEY, so the server selects. This is the one place that is correct.
	_, usedName, err := g.encrypt(ctx, g.cryptoKey, probePlaintext)
	if err != nil {
		return KeyVersion{}, err
	}
	n, err := parseVersion(usedName)
	if err != nil {
		return KeyVersion{}, err
	}
	return KeyVersion{Name: usedName, Number: n}, nil
}

// encrypt is THE ONE ENCRYPT PATH, shared by Wrap and by Active's probe.
//
// Shared deliberately: the probe then exercises the same integrity handling, the same
// timeout and the same retry policy that a real wrap uses, so a break in any of them
// shows up as an unhealthy plugin rather than waiting for the next Secret write. A
// separate, simpler probe would have been a second code path that reports on the first.
//
// ALL FOUR INTEGRITY CHECKS ARE PERFORMED, and this is a requirement rather than
// diligence. A silently corrupted wrapped DEK is UNRECOVERABLE: the object encrypts,
// the write succeeds, and that DEK can never be unwrapped -- so the Secret is lost with
// no remedy afterwards. The upstream plugin performs none of them.
//
// Google's own guidance is to discard the response on a checksum mismatch and retry a
// limited number of times, which is what the bounded loop does. The retry is for
// INTEGRITY only: a transport or permission error returns immediately, because retrying
// a PermissionDenied three times only delays the report, and both the API server and
// the refresh loop retry at their own cadence.
func (g *GCPKMS) encrypt(ctx context.Context, target string, plaintext []byte) ([]byte, string, error) {
	const attempts = 3
	var lastErr error
	for i := 0; i < attempts; i++ {
		cctx, cancel := g.call(ctx)
		// TIMED AROUND THE CALL ITSELF, not the retry loop. An SLO wants the latency of
		// a Cloud KMS round trip; folding three attempts into one observation would
		// report a bimodal distribution that is really two different things.
		started := time.Now()
		resp, err := g.client.Encrypt(cctx, &kmspb.EncryptRequest{
			Name:            target,
			Plaintext:       plaintext,
			PlaintextCrc32C: wrapperspb.Int64(crc32c(plaintext)),
		})
		cancel()
		observe("encrypt", started, err)
		if err != nil {
			// The message names the TARGET and never the plaintext. A plaintext here is
			// a data encryption key, and anything in an error reaches the API server's
			// logs.
			return nil, "", fmt.Errorf("Cloud KMS Encrypt against %s failed: %w", target, err)
		}

		// Did the server actually verify OUR checksum? A false value means the
		// plaintext may have been corrupted in transit and the server did not notice,
		// so the ciphertext cannot be trusted even though the call succeeded.
		if !resp.GetVerifiedPlaintextCrc32C() {
			metrics.Failures.WithLabelValues("encrypt", "integrity").Inc()
			lastErr = fmt.Errorf("Cloud KMS did not verify the plaintext checksum, so the " +
				"plaintext may have been corrupted in transit")
			continue
		}
		// And is the ciphertext we received the ciphertext it sent?
		if got, want := crc32c(resp.GetCiphertext()), resp.GetCiphertextCrc32C().GetValue(); got != want {
			metrics.Failures.WithLabelValues("encrypt", "integrity").Inc()
			lastErr = fmt.Errorf("the returned ciphertext fails its own checksum (%d != %d), so "+
				"it was corrupted in transit. Storing it would produce a Secret that can never "+
				"be decrypted", got, want)
			continue
		}
		if resp.GetName() == "" {
			return nil, "", fmt.Errorf("Cloud KMS did not name the CryptoKeyVersion that "+
				"encrypted under %s, so there is nothing to report as the key identifier", target)
		}
		return resp.GetCiphertext(), resp.GetName(), nil
	}
	return nil, "", fmt.Errorf("Cloud KMS Encrypt against %s failed its integrity check on %d "+
		"attempts, so nothing was stored: %w", target, attempts, lastErr)
}

// Wrap encrypts a data encryption key under the EXACT version it is given.
//
// EncryptRequest.Name takes a CryptoKey or a CryptoKeyVersion -- "If a CryptoKey is
// specified, the server will use its primary version" (service.pb.go). Naming the
// version is what makes Status and Encrypt agree during the primary's propagation
// window; see the Store interface for why the API server treats that disagreement as an
// unhealthy provider.
func (g *GCPKMS) Wrap(ctx context.Context, dek []byte, versionName string) ([]byte, string, error) {
	if _, err := parseVersion(versionName); err != nil {
		return nil, "", fmt.Errorf("refusing to encrypt against %q: %w", versionName, err)
	}

	ciphertext, usedName, err := g.encrypt(ctx, versionName, dek)
	if err != nil {
		return nil, "", err
	}

	// THE REQUESTED VERSION IS THE ONE THAT ENCRYPTED, asserted rather than assumed.
	//
	// Checked HERE and not in encrypt(), because it is only an invariant when an exact
	// version was asked for -- Active deliberately asks for the parent and expects the
	// server to choose.
	//
	// With an exact version requested a mismatch is not a race to tolerate: it means the
	// response does not describe the request, and the key_id this plugin is about to
	// report would not name the material that was actually used. Refusing costs one
	// discarded wrap; accepting writes a Secret labelled with the wrong key.
	if usedName != versionName {
		return nil, "", fmt.Errorf("asked Cloud KMS to encrypt under %s and it reports it "+
			"used %s; the response does not describe the request, so nothing was stored",
			versionName, usedName)
	}
	return ciphertext, usedName, nil
}

// Unwrap decrypts a wrapped data encryption key.
//
// ADDRESSED TO THE PARENT KEY, not a version, and that is not an oversight. The
// ciphertext embeds the version that produced it and Cloud KMS resolves it, which is
// what lets data written before any number of rotations keep unwrapping. Naming a
// version here would make every object written before the last rotation unreadable --
// the exact loss ADR-100 exists to avoid. The upstream plugin does the same thing by
// stripping the version off the stored key_id (plugin/v2/plugin.go, extractKeyName).
//
// The same integrity contract in the other direction. A corrupted ciphertext on the way
// to Cloud KMS produces a decrypt failure rather than silent damage, which is the benign
// case; a corrupted PLAINTEXT on the way back is a wrong DEK, and a wrong DEK decrypts
// the Secret to garbage. So the returned plaintext is checked too.
func (g *GCPKMS) Unwrap(ctx context.Context, wrapped []byte) ([]byte, error) {
	const attempts = 3
	var lastErr error
	for i := 0; i < attempts; i++ {
		cctx, cancel := g.call(ctx)
		started := time.Now()
		resp, err := g.client.Decrypt(cctx, &kmspb.DecryptRequest{
			Name:             g.cryptoKey,
			Ciphertext:       wrapped,
			CiphertextCrc32C: wrapperspb.Int64(crc32c(wrapped)),
		})
		cancel()
		observe("decrypt", started, err)
		if err != nil {
			return nil, fmt.Errorf("decrypting a data encryption key under %s: %w",
				g.cryptoKey, err)
		}
		if got, want := crc32c(resp.GetPlaintext()), resp.GetPlaintextCrc32C().GetValue(); got != want {
			metrics.Failures.WithLabelValues("decrypt", "integrity").Inc()
			lastErr = fmt.Errorf("the returned data encryption key fails its own checksum "+
				"(%d != %d), so it was corrupted in transit. Using it would decrypt the Secret "+
				"to garbage", got, want)
			continue
		}
		if len(resp.GetPlaintext()) == 0 {
			return nil, fmt.Errorf("Cloud KMS returned an empty data encryption key")
		}
		return resp.GetPlaintext(), nil
	}
	return nil, fmt.Errorf("the data encryption key failed its integrity check on %d "+
		"attempts: %w", attempts, lastErr)
}

// ParentKeyOf strips a version off a CryptoKeyVersion resource name.
//
// Exported for the identifier handling in internal/service, which has to recover the
// parent key from a stored key_id the same way the upstream plugin does.
func ParentKeyOf(versionName string) string {
	return keyFromVersionName.FindString(versionName)
}
