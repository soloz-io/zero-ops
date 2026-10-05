// Package service implements the Kubernetes KMS v2 provider interface.
package service

import (
	"context"
	"fmt"
	"strconv"

	kmsv2 "k8s.io/kms/apis/v2"

	"github.com/soloz-io/zero-ops/operators/kms-plugin/internal/active"
	"github.com/soloz-io/zero-ops/operators/kms-plugin/internal/keyid"
	"github.com/soloz-io/zero-ops/operators/kms-plugin/internal/keystore"
)

// annotationVersion carries the key VERSION a data encryption key was wrapped under,
// stored beside the ciphertext by the API server and handed back on Decrypt.
//
// IT IS WHY UNWRAPPING OLD DATA KEEPS WORKING. The active version is constrained --
// monotonic, never reused -- but data written months ago was wrapped under an earlier
// one, and the store retains those. Without the version recorded per object, Decrypt
// would have to guess, and guessing wrong on a Secret the cluster needs to start is
// not a recoverable error.
//
// The key must be a fully qualified domain name per the interface's annotation rules.
const annotationVersion = "version.kms.soloz.io"

// Service is the KMS v2 server. It holds a snapshot of the active key version and
// never consults the store on the request path.
type Service struct {
	kmsv2.UnimplementedKeyManagementServiceServer

	cluster string
	store   keystore.Store
	held    *active.Holder
}

func New(cluster string, store keystore.Store, held *active.Holder) *Service {
	return &Service{cluster: cluster, store: store, held: held}
}

// Refresh observes the key store and advances the snapshot.
//
// Called on an interval by the process, NOT from Status or Encrypt. Both of those read
// the snapshot, which is what keeps them from observing different versions during a
// rotation (ADR-100 acceptance criterion 2).
func (s *Service) Refresh(ctx context.Context) error {
	st, err := s.store.Active(ctx)
	if err != nil {
		return fmt.Errorf("reading the active key version: %w", err)
	}
	return s.held.Advance(keyid.Ref{Cluster: s.cluster, Key: st.Key, Version: st.Version})
}

// Status reports the plugin's health and the identifier of the EFFECTIVE key.
//
// `healthz: ok` is reported only when a snapshot exists. A plugin that has not observed
// the key store does not know its effective key, and claiming health with an empty
// key id tells the API server everything is fine while the identifier is meaningless.
func (s *Service) Status(_ context.Context, _ *kmsv2.StatusRequest) (*kmsv2.StatusResponse, error) {
	snap, err := s.held.Current()
	if err != nil {
		return nil, err
	}
	return &kmsv2.StatusResponse{
		Version: "v2",
		Healthz: "ok",
		KeyId:   snap.ID,
	}, nil
}

// Encrypt wraps a data encryption key under the key version this plugin is reporting.
//
// THE SAME SNAPSHOT SUPPLIES BOTH THE KEY ID AND THE VERSION USED TO WRAP. Reading the
// id from the snapshot and the version from the store -- or calling the store twice --
// is the disagreement the interface treats as an unhealthy plugin.
func (s *Service) Encrypt(ctx context.Context, req *kmsv2.EncryptRequest) (*kmsv2.EncryptResponse, error) {
	snap, err := s.held.Current()
	if err != nil {
		return nil, err
	}
	wrapped, err := s.store.Wrap(ctx, snap.Ref.Version, req.Plaintext)
	if err != nil {
		return nil, fmt.Errorf("wrapping the data encryption key under %s: %w", snap.ID, err)
	}
	return &kmsv2.EncryptResponse{
		Ciphertext: wrapped,
		KeyId:      snap.ID,
		Annotations: map[string][]byte{
			annotationVersion: []byte(strconv.Itoa(snap.Ref.Version)),
		},
	}, nil
}

// Decrypt unwraps a data encryption key, under the version it was WRAPPED with.
//
// Not the active version. An object written before the last rotation was wrapped under
// an earlier version, the store retains those, and unwrapping must keep working or a
// rotation would make every existing Secret unreadable -- the exact loss this design
// is built to avoid.
func (s *Service) Decrypt(ctx context.Context, req *kmsv2.DecryptRequest) (*kmsv2.DecryptResponse, error) {
	raw, ok := req.Annotations[annotationVersion]
	if !ok {
		return nil, fmt.Errorf("the stored object carries no %s annotation, so the key version it "+
			"was wrapped under is unknown; refusing to guess, because guessing wrong on a Secret "+
			"the cluster needs is not recoverable", annotationVersion)
	}
	version, err := strconv.Atoi(string(raw))
	if err != nil || version < 1 {
		return nil, fmt.Errorf("the %s annotation is %q, which is not a key version",
			annotationVersion, string(raw))
	}
	dek, err := s.store.Unwrap(ctx, version, req.Ciphertext)
	if err != nil {
		return nil, fmt.Errorf("unwrapping a data encryption key wrapped under version %d: %w",
			version, err)
	}
	return &kmsv2.DecryptResponse{Plaintext: dek}, nil
}
