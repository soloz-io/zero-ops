// Package store is the content-addressed object layer behind a workspace
// checkpoint (ADR-052 §14, §18), with squashfs archive support for O(1)
// restore (§14.2).
//
// Three kinds of object live under one workspace's prefix:
//
//	<prefix>/objects/<sha256>          immutable file content, shared between
//	                                   checkpoints of THIS workspace
//	<prefix>/checkpoints/<id>.json     a manifest naming the tree
//	<prefix>/archive.sqsh              squashfs image for fast FUSE restore
//
// Keys are scoped per workspace deliberately (§18.6). Dedup applies within a
// workspace, not across them, which gives up cross-workspace sharing and buys
// three things worth more: deleting a workspace is a prefix operation, a
// tenant's data is separable on request, and one workspace's checkpoint can
// never become a load-bearing dependency of another tenant's.
package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// ErrNotConfigured is the one legitimate no-op: a deployment with no object
// store at all. Distinguished from every other failure because treating "no
// bucket configured here" the same as "the upload failed" is how a real error
// becomes invisible.
var ErrNotConfigured = errors.New("object storage is not configured")

// ErrNoArchive means this workspace has no squashfs archive yet — a first-ever
// run, or a workspace whose checkpoints all predate §14.2.
//
// Distinguished from a transport failure on purpose. "Absent" is an ordinary
// first-run state that must restore empty and continue; "unreachable" is a
// fault. Collapsing the two is what turned a brand-new workspace into a pod
// that could not start.
var ErrNoArchive = errors.New("no squashfs archive for this workspace")

// Entry is one file in a manifest.
type Entry struct {
	Path string `json:"path"`
	Hash string `json:"hash"`
	Mode uint32 `json:"mode"`
	Size int64  `json:"size"`
}

// Manifest is a checkpoint: an immutable, point-in-time record of the WHOLE
// workspace tree — tracked, untracked and uncommitted alike (§14).
//
// `.git` is captured as ordinary files inside Entries, never as the payload.
// A design that stored only committed git objects would silently drop every
// edit made since the last commit, which is the loss this exists to prevent.
type Manifest struct {
	ID          string    `json:"id"`
	WorkspaceID string    `json:"workspaceId"`
	Parent      string    `json:"parent,omitempty"`
	Name        string    `json:"name,omitempty"`
	Description string    `json:"description,omitempty"`
	Trigger     string    `json:"trigger"`
	CreatedAt   time.Time `json:"createdAt"`
	Entries     []Entry   `json:"entries"`
}

type Store struct {
	c       *minio.Client
	bucket  string
	prefix  string
	staging string
	keep    int
}

// Config is read from the environment the operator injects into this
// container. The workload container never receives these values (§19.6).
type Config struct {
	Endpoint    string
	Bucket      string
	AccessKey   string
	SecretKey   string
	Region      string
	WorkspaceID string

	// Staging is a writable scratch directory — the `ws-staging` emptyDir the
	// operator mounts. Everything this package writes locally goes here.
	//
	// NOT os.TempDir(). Both containers run with ReadOnlyRootFilesystem and the
	// operator mounts a writable /tmp onto the WORKLOAD container only, so
	// `/tmp` here is read-only. Archive creation therefore failed every time —
	// and because it is best-effort, it logged a warning and carried on, so the
	// squashfs fast path silently never existed while everything looked healthy.
	Staging string

	// KeepCheckpoints is the retention bound: how many checkpoints survive a
	// prune. Each one owns a full compressed archive, so this is what makes
	// storage O(bounded) rather than O(number of checkpoints ever taken).
	KeepCheckpoints int
}

// DefaultKeepCheckpoints bounds per-workspace storage (§14.2 retention).
//
// Five is a product decision, not a technical limit: it is how far back "undo
// to a checkpoint" can reach. The cost of raising it is linear and easy to
// predict — one compressed copy of the whole workspace per retained
// checkpoint — because squashfs archives do NOT share blocks with each other.
const DefaultKeepCheckpoints = 5

func FromEnv() (Config, error) {
	c := Config{
		Endpoint:        os.Getenv("S3_ENDPOINT_URL"),
		Bucket:          os.Getenv("S3_BUCKET_NAME"),
		AccessKey:       os.Getenv("S3_ACCESS_KEY_ID"),
		SecretKey:       os.Getenv("S3_SECRET_ACCESS_KEY"),
		Region:          os.Getenv("S3_REGION"),
		WorkspaceID:     os.Getenv("WORKSPACE_ID"),
		Staging:         os.Getenv("STAGING_ROOT"),
		KeepCheckpoints: DefaultKeepCheckpoints,
	}
	if c.Region == "" {
		c.Region = "us-east-1"
	}
	if c.Staging == "" {
		c.Staging = "/ws-staging"
	}
	if v := os.Getenv("KEEP_CHECKPOINTS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			c.KeepCheckpoints = n
		}
	}
	if c.WorkspaceID == "" {
		return c, fmt.Errorf("WORKSPACE_ID is required")
	}
	if c.Endpoint == "" || c.Bucket == "" || c.AccessKey == "" || c.SecretKey == "" {
		return c, ErrNotConfigured
	}
	return c, nil
}

func New(cfg Config) (*Store, error) {
	ep := strings.TrimPrefix(strings.TrimPrefix(cfg.Endpoint, "https://"), "http://")
	secure := !strings.HasPrefix(cfg.Endpoint, "http://")
	c, err := minio.New(ep, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
		Secure: secure,
		Region: cfg.Region,
	})
	if err != nil {
		return nil, fmt.Errorf("object store client: %w", err)
	}
	staging := cfg.Staging
	if staging == "" {
		staging = "/ws-staging"
	}
	keep := cfg.KeepCheckpoints
	if keep <= 0 {
		keep = DefaultKeepCheckpoints
	}
	return &Store{
		c:       c,
		bucket:  cfg.Bucket,
		prefix:  "workspaces/" + cfg.WorkspaceID,
		staging: staging,
		keep:    keep,
	}, nil
}

func (s *Store) objectKey(hash string) string  { return s.prefix + "/objects/" + hash }
func (s *Store) manifestKey(id string) string  { return s.prefix + "/checkpoints/" + id + ".json" }
func (s *Store) archiveKey() string            { return s.prefix + "/archive.sqsh" }
func (s *Store) checkpointArchiveKey(id string) string { return s.prefix + "/checkpoints/" + id + ".sqsh" }

// hashFile returns the content address of a file. This is the whole basis of
// dedup: two checkpoints differing in 3 of 10,000 files upload 3 objects,
// because the other 9,997 hash to keys that already exist.
func hashFile(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

// Snapshot captures root as a new checkpoint.
//
// Crash-consistent, not application-consistent (§18.3): the tree is read while
// the agent may still be writing it. That is acceptable because the on-demand
// trigger fires at agent turn boundaries, when nothing is mid-write; the
// periodic backstop makes no such claim.
//
// The manifest is written LAST and only after every object it references has
// been uploaded, so a partial run leaves an absent checkpoint rather than a
// corrupt one (§18.3).
func (s *Store) Snapshot(ctx context.Context, root, name, desc, trigger, parent string) (*Manifest, error) {
	m := &Manifest{
		ID:          newID(),
		WorkspaceID: strings.TrimPrefix(s.prefix, "workspaces/"),
		Parent:      parent,
		Name:        name,
		Description: desc,
		Trigger:     trigger,
		CreatedAt:   time.Now().UTC(),
	}

	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		// Symlinks are skipped rather than followed: the skills directory is
		// materialised as links into per-session temp paths whose names are
		// random, so capturing them checkpoints paths that cannot exist in any
		// other pod and a restore reinstates dangling links.
		if info.IsDir() || !info.Mode().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		hash, size, err := hashFile(p)
		if err != nil {
			return err
		}
		if err := s.putIfAbsent(ctx, hash, p, size); err != nil {
			return fmt.Errorf("upload %s: %w", rel, err)
		}
		m.Entries = append(m.Entries, Entry{Path: rel, Hash: hash, Mode: uint32(info.Mode().Perm()), Size: size})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(m.Entries, func(i, j int) bool { return m.Entries[i].Path < m.Entries[j].Path })

	// Refuse to checkpoint an empty tree over a workspace that has content.
	//
	// The pairing of "S3 is the only source of truth" with "keep the newest 5"
	// creates a path where the workspace destroys itself: if the tree is empty
	// for a reason that is NOT the user emptying it — a mount that silently
	// failed, a restore that started blank — then the backstop uploads that
	// emptiness, and five intervals later retention has evicted every real
	// checkpoint. There is no third copy to recover from, because §14.2 removed
	// the PVC.
	//
	// Restore already refuses to start on an unreachable bucket, which closes
	// the common case. This closes it at the other end: the last write before
	// data is lost is this one, so this is the last place to stop it.
	//
	// Zero entries only, deliberately. A workspace that shrank to one file is
	// plausibly the user's doing; one that contains literally nothing, in a
	// workspace that previously had checkpoints, is a fault every time — a real
	// agent workspace always holds at least .git.
	if len(m.Entries) == 0 {
		if prior, err := s.Latest(ctx); err == nil && prior != "" {
			return nil, fmt.Errorf(
				"refusing to checkpoint an empty workspace over existing checkpoint %s: "+
					"the tree at %s has no files, which is a restore or mount failure rather "+
					"than a change worth recording", prior, root)
		}
	}

	body, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	if _, err := s.c.PutObject(ctx, s.bucket, s.manifestKey(m.ID),
		strings.NewReader(string(body)), int64(len(body)),
		minio.PutObjectOptions{ContentType: "application/json"}); err != nil {
		return nil, fmt.Errorf("write manifest: %w", err)
	}
	if err := s.setLatest(ctx, m.ID); err != nil {
		return nil, err
	}

	// Create and upload the squashfs archive for O(1) restore (§14.2).
	//
	// Written into the staging emptyDir, never os.TempDir(): this container's
	// root filesystem is read-only, so /tmp is not writable and every archive
	// creation failed there silently.
	//
	// Still best-effort. mksquashfs may be genuinely absent, and a checkpoint
	// whose content-addressed objects and manifest are already durable is a
	// valid checkpoint — it just restores the slow way. Failing the snapshot
	// here would throw away work that is already safely uploaded.
	archivePath := filepath.Join(s.staging, "snapshot-"+m.ID+".sqsh")
	if err := s.CreateArchive(ctx, root, archivePath); err != nil {
		log.Printf("WARNING: squashfs archive creation failed — this checkpoint will restore file-by-file: %v", err)
	} else {
		if err := s.UploadArchive(ctx, archivePath); err != nil {
			log.Printf("WARNING: squashfs archive upload failed: %v", err)
		}
		// Also as a per-checkpoint archive, so undo can target this exact point.
		if err := s.UploadCheckpointArchive(ctx, m.ID, archivePath); err != nil {
			log.Printf("WARNING: checkpoint archive upload failed: %v", err)
		}
		os.Remove(archivePath)
	}

	// Retention, immediately after the checkpoint is durable (§14.2).
	//
	// Here rather than on a timer because this is the only moment a new
	// checkpoint exists, so it is the only moment an old one can become
	// surplus. Failure is logged, not returned: a checkpoint that was written
	// successfully must not be reported as failed because cleaning up an older
	// one did not work.
	if err := s.Prune(ctx); err != nil {
		log.Printf("WARNING: checkpoint retention failed (storage will keep growing): %v", err)
	}

	return m, nil
}

// Prune enforces the retention bound: the newest `keep` checkpoints survive,
// everything older is removed (§14.2).
//
// Two passes, in this order, and the order is the safety property:
//
//  1. delete surplus manifests and their per-checkpoint archives;
//  2. sweep `objects/`, deleting every object no SURVIVING manifest references.
//
// Doing it the other way round — sweeping against manifests that are about to
// be deleted — would retain objects nothing points at any more. Doing the sweep
// against the manifests that remain is what makes it correct, and re-reading
// them from S3 rather than trusting an in-memory set is what makes it correct
// after a restart.
//
// Archives alone would not bound storage. They are the large objects, but
// `objects/` accumulates one entry per distinct file version forever, so
// capping archives without sweeping objects caps half the growth.
//
// Single-writer assumption (§18.1): sessions sharing a workspaceId are
// serialised, so no other process is writing objects this sweep might race. A
// concurrent snapshot from a second pod could upload an object between the
// mark and the sweep and have it deleted underneath — the same assumption the
// rest of this package already depends on.
// planRetention splits checkpoint ids (newest first) into survivors and
// deletions.
//
// Pure, and separated from Prune on purpose: this is the function that decides
// what gets destroyed, and it is the only part of retention that can be tested
// without an object store. A slicing mistake here is unrecoverable — there is
// no other copy of a workspace under §14.2.
//
// keep <= 0 deletes NOTHING. A misconfigured KEEP_CHECKPOINTS must degrade to
// unbounded storage, never to an empty bucket.
func planRetention(newestFirst []string, keep int) (survivors, doomed []string) {
	if keep <= 0 {
		return newestFirst, nil
	}
	if len(newestFirst) <= keep {
		return newestFirst, nil
	}
	return newestFirst[:keep], newestFirst[keep:]
}

func (s *Store) Prune(ctx context.Context) error {
	all, err := s.ListCheckpoints(ctx)
	if err != nil {
		return fmt.Errorf("list checkpoints: %w", err)
	}
	ids, doomed := planRetention(all, s.keep)
	if len(doomed) > 0 {
		for _, id := range doomed {
			if err := s.c.RemoveObject(ctx, s.bucket, s.manifestKey(id),
				minio.RemoveObjectOptions{}); err != nil {
				return fmt.Errorf("remove manifest %s: %w", id, err)
			}
			// The archive may legitimately be absent — archive creation is
			// best-effort — so a failure here is not fatal to the prune.
			if err := s.c.RemoveObject(ctx, s.bucket, s.checkpointArchiveKey(id),
				minio.RemoveObjectOptions{}); err != nil {
				log.Printf("retention: could not remove archive for %s: %v", id, err)
			}
		}
	}

	// Mark: every hash still referenced by a surviving manifest.
	live := make(map[string]struct{})
	for _, id := range ids {
		m, err := s.GetManifest(ctx, id)
		if err != nil {
			// Abort rather than sweep against an incomplete reference set — a
			// manifest we failed to read is one whose objects we would delete.
			return fmt.Errorf("read surviving manifest %s (aborting sweep): %w", id, err)
		}
		for _, e := range m.Entries {
			live[e.Hash] = struct{}{}
		}
	}

	// Sweep.
	objPrefix := s.prefix + "/objects/"
	var removed int
	for obj := range s.c.ListObjects(ctx, s.bucket, minio.ListObjectsOptions{
		Prefix: objPrefix, Recursive: true,
	}) {
		if obj.Err != nil {
			return fmt.Errorf("list objects: %w", obj.Err)
		}
		hash := strings.TrimPrefix(obj.Key, objPrefix)
		if hash == "" {
			continue
		}
		if _, ok := live[hash]; ok {
			continue
		}
		if err := s.c.RemoveObject(ctx, s.bucket, obj.Key, minio.RemoveObjectOptions{}); err != nil {
			return fmt.Errorf("remove object %s: %w", hash, err)
		}
		removed++
	}
	if removed > 0 {
		log.Printf("retention: %d checkpoints kept, %d unreferenced objects swept", len(ids), removed)
	}
	return nil
}

// putIfAbsent uploads only content the store does not already hold.
//
// Existing-and-identical is success, not a conflict: the key IS the content
// hash, so an object that is already there is by definition the same bytes.
func (s *Store) putIfAbsent(ctx context.Context, hash, path string, size int64) error {
	key := s.objectKey(hash)
	if _, err := s.c.StatObject(ctx, s.bucket, key, minio.StatObjectOptions{}); err == nil {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = s.c.PutObject(ctx, s.bucket, key, f, size, minio.PutObjectOptions{})
	return err
}

const latestPointer = "/checkpoints/LATEST"

func (s *Store) setLatest(ctx context.Context, id string) error {
	_, err := s.c.PutObject(ctx, s.bucket, s.prefix+latestPointer,
		strings.NewReader(id), int64(len(id)), minio.PutObjectOptions{ContentType: "text/plain"})
	return err
}

func (s *Store) Latest(ctx context.Context) (string, error) {
	o, err := s.c.GetObject(ctx, s.bucket, s.prefix+latestPointer, minio.GetObjectOptions{})
	if err != nil {
		return "", err
	}
	defer o.Close()
	b, err := io.ReadAll(o)
	if err != nil {
		// A missing pointer is "this workspace has no checkpoint yet", which is
		// a legitimate first-run state and not an error.
		return "", nil
	}
	return strings.TrimSpace(string(b)), nil
}

func (s *Store) GetManifest(ctx context.Context, id string) (*Manifest, error) {
	o, err := s.c.GetObject(ctx, s.bucket, s.manifestKey(id), minio.GetObjectOptions{})
	if err != nil {
		return nil, err
	}
	defer o.Close()
	var m Manifest
	if err := json.NewDecoder(o).Decode(&m); err != nil {
		return nil, fmt.Errorf("decode manifest %s: %w", id, err)
	}
	return &m, nil
}

// Restore materialises a checkpoint into root, verifying every object against
// the hash the manifest names (§18.4).
//
// Verification is close to free given the hashing the upload path already
// does, and it is the difference between "restore failed" and a workspace that
// silently contains something other than what was checkpointed.
func (s *Store) Restore(ctx context.Context, root string, m *Manifest) error {
	for _, e := range m.Entries {
		dest := filepath.Join(root, e.Path)
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return err
		}
		o, err := s.c.GetObject(ctx, s.bucket, s.objectKey(e.Hash), minio.GetObjectOptions{})
		if err != nil {
			return fmt.Errorf("get %s: %w", e.Path, err)
		}
		data, err := io.ReadAll(o)
		o.Close()
		if err != nil {
			return fmt.Errorf("read %s: %w", e.Path, err)
		}
		sum := sha256.Sum256(data)
		if got := hex.EncodeToString(sum[:]); got != e.Hash {
			return fmt.Errorf("checksum mismatch for %s: manifest says %s, object is %s", e.Path, e.Hash, got)
		}
		mode := os.FileMode(e.Mode)
		if mode == 0 {
			mode = 0o644
		}
		// Written via a temp file then renamed: git objects are stored 0444, so
		// writing straight to the destination fails with EPERM on any file that
		// already exists. This is the failure that took down resumed sessions
		// when restore ran against a reattached volume.
		tmp := dest + ".ws-tmp"
		if err := os.WriteFile(tmp, data, mode); err != nil {
			return fmt.Errorf("write %s: %w", e.Path, err)
		}
		if err := os.Rename(tmp, dest); err != nil {
			return fmt.Errorf("rename %s: %w", e.Path, err)
		}
	}
	return nil
}

func newID() string {
	b := make([]byte, 16)
	now := time.Now().UTC().UnixNano()
	for i := 0; i < 8; i++ {
		b[i] = byte(now >> (8 * (7 - i)))
	}
	r := sha256.Sum256([]byte(fmt.Sprint(now, os.Getpid())))
	copy(b[8:], r[:8])
	return hex.EncodeToString(b)
}

// CreateArchive creates a squashfs archive from root at archivePath.
//
// The archive is a compressed, read-only filesystem image suitable for O(1)
// restore via squashfuse + fuse-overlayfs (§14.2). Compression uses zstd for
// speed and ratio; 8 processors are used for parallel compression.
//
// This is called during Snapshot() to ensure every checkpoint has a
// corresponding fast-restore image.
func (s *Store) CreateArchive(ctx context.Context, root, archivePath string) error {
	cmd := exec.CommandContext(ctx, "mksquashfs", root, archivePath,
		"-comp", "zstd",
		"-processors", "8",
		"-no-progress",
		"-noappend",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("mksquashfs: %w (output: %s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// UploadArchive uploads a squashfs archive to S3 at the workspace's archive key.
func (s *Store) UploadArchive(ctx context.Context, archivePath string) error {
	info, err := os.Stat(archivePath)
	if err != nil {
		return fmt.Errorf("stat archive: %w", err)
	}
	f, err := os.Open(archivePath)
	if err != nil {
		return fmt.Errorf("open archive: %w", err)
	}
	defer f.Close()
	_, err = s.c.PutObject(ctx, s.bucket, s.archiveKey(), f, info.Size(),
		minio.PutObjectOptions{ContentType: "application/x-squashfs"})
	return err
}

// UploadCheckpointArchive uploads a squashfs archive for a specific checkpoint,
// enabling undo-to-any-checkpoint (§14.2).
func (s *Store) UploadCheckpointArchive(ctx context.Context, checkpointID, archivePath string) error {
	info, err := os.Stat(archivePath)
	if err != nil {
		return fmt.Errorf("stat archive: %w", err)
	}
	f, err := os.Open(archivePath)
	if err != nil {
		return fmt.Errorf("open archive: %w", err)
	}
	defer f.Close()
	_, err = s.c.PutObject(ctx, s.bucket, s.checkpointArchiveKey(checkpointID), f, info.Size(),
		minio.PutObjectOptions{ContentType: "application/x-squashfs"})
	return err
}

// DownloadArchive downloads the squashfs archive from S3 to archivePath.
//
// Returns an error if the archive does not exist (first-ever checkpoint or
// S3 misconfiguration). The caller should fall back to file-by-file restore.
func (s *Store) DownloadArchive(ctx context.Context, archivePath string) error {
	return s.downloadTo(ctx, s.archiveKey(), archivePath)
}

// downloadTo fetches one object to a local path, reporting a missing object as
// ErrNoArchive rather than as a generic failure.
//
// StatObject first, deliberately. minio-go's GetObject is lazy — it returns a
// non-nil handle and a nil error, and surfaces "key does not exist" only on the
// first Read — so the natural-looking code path reports a missing archive as a
// read error indistinguishable from a truncated download.
func (s *Store) downloadTo(ctx context.Context, key, dest string) error {
	if _, err := s.c.StatObject(ctx, s.bucket, key, minio.StatObjectOptions{}); err != nil {
		if minio.ToErrorResponse(err).Code == "NoSuchKey" {
			return ErrNoArchive
		}
		return fmt.Errorf("stat %s: %w", key, err)
	}
	o, err := s.c.GetObject(ctx, s.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return fmt.Errorf("get %s: %w", key, err)
	}
	defer o.Close()
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	// Downloaded to a temp name and renamed, so a partial transfer can never be
	// mistaken for a complete archive by a later start. squashfuse would fail
	// on a truncated image in a way that reads as corruption rather than as an
	// interrupted download.
	tmp := dest + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return fmt.Errorf("create %s: %w", tmp, err)
	}
	if _, err := io.Copy(f, o); err != nil {
		f.Close()
		os.Remove(tmp)
		return fmt.Errorf("download %s: %w", key, err)
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dest)
}

// DownloadCheckpointArchive downloads a squashfs archive for a specific
// checkpoint, enabling undo-to-any-checkpoint (§14.2).
func (s *Store) DownloadCheckpointArchive(ctx context.Context, checkpointID, archivePath string) error {
	return s.downloadTo(ctx, s.checkpointArchiveKey(checkpointID), archivePath)
}

// StagingDir is where this store writes local scratch (the `ws-staging`
// emptyDir). Exposed so the mount code and the store agree on one location.
func (s *Store) StagingDir() string { return s.staging }

// ArchiveExists checks whether a squashfs archive exists in S3.
func (s *Store) ArchiveExists(ctx context.Context) bool {
	_, err := s.c.StatObject(ctx, s.bucket, s.archiveKey(), minio.StatObjectOptions{})
	return err == nil
}

// ListCheckpoints returns checkpoint IDs in reverse chronological order
// (newest first), for the undo-to-checkpoint UI and for retention.
//
// ONLY `.json` manifests count. The same prefix also holds one `.sqsh` archive
// per checkpoint plus the LATEST pointer, and an earlier version trimmed only
// the `.json` suffix — so every checkpoint was returned twice, once as `<id>`
// and once as `<id>.sqsh`. The UI would have shown phantom entries, and
// retention would have counted five checkpoints where there were two or three.
//
// The ordering is chronological for free: newID() prefixes the id with the
// creation time in big-endian nanoseconds, so lexical order IS time order.
// That is a real coupling between the two functions, and it is why this sorts
// strings rather than reading CreatedAt out of every manifest.
func (s *Store) ListCheckpoints(ctx context.Context) ([]string, error) {
	prefix := s.prefix + "/checkpoints/"
	ch := s.c.ListObjects(ctx, s.bucket, minio.ListObjectsOptions{
		Prefix:    prefix,
		Recursive: true,
	})
	var ids []string
	for obj := range ch {
		if obj.Err != nil {
			return nil, obj.Err
		}
		name := strings.TrimPrefix(obj.Key, prefix)
		if !strings.HasSuffix(name, ".json") {
			continue // .sqsh archives and the LATEST pointer
		}
		name = strings.TrimSuffix(name, ".json")
		if name == "" {
			continue
		}
		ids = append(ids, name)
	}
	sort.Sort(sort.Reverse(sort.StringSlice(ids)))
	return ids, nil
}
