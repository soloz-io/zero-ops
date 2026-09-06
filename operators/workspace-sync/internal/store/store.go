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
	"os"
	"os/exec"
	"path/filepath"
	"sort"
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
	c      *minio.Client
	bucket string
	prefix string
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
}

func FromEnv() (Config, error) {
	c := Config{
		Endpoint:    os.Getenv("S3_ENDPOINT_URL"),
		Bucket:      os.Getenv("S3_BUCKET_NAME"),
		AccessKey:   os.Getenv("S3_ACCESS_KEY_ID"),
		SecretKey:   os.Getenv("S3_SECRET_ACCESS_KEY"),
		Region:      os.Getenv("S3_REGION"),
		WorkspaceID: os.Getenv("WORKSPACE_ID"),
	}
	if c.Region == "" {
		c.Region = "us-east-1"
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
	return &Store{c: c, bucket: cfg.Bucket, prefix: "workspaces/" + cfg.WorkspaceID}, nil
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

	// Create and upload squashfs archive for O(1) restore (§14.2).
	// This is best-effort: if mksquashfs is unavailable (e.g. dev cluster
	// without FUSE tools), the checkpoint itself is still valid — restore
	// falls back to file-by-file content-addressed materialization.
	archivePath := filepath.Join(os.TempDir(), "ws-archive-"+m.ID+".sqsh")
	if err := s.CreateArchive(ctx, root, archivePath); err != nil {
		// Log but don't fail: archive is an optimization, not a requirement.
		fmt.Fprintf(os.Stderr, "warning: squashfs archive creation failed: %v\n", err)
	} else {
		if err := s.UploadArchive(ctx, archivePath); err != nil {
			fmt.Fprintf(os.Stderr, "warning: squashfs archive upload failed: %v\n", err)
		}
		// Also upload as per-checkpoint archive for undo-to-checkpoint.
		if err := s.UploadCheckpointArchive(ctx, m.ID, archivePath); err != nil {
			fmt.Fprintf(os.Stderr, "warning: checkpoint archive upload failed: %v\n", err)
		}
		os.Remove(archivePath)
	}

	return m, nil
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
	o, err := s.c.GetObject(ctx, s.bucket, s.archiveKey(), minio.GetObjectOptions{})
	if err != nil {
		return fmt.Errorf("get archive: %w", err)
	}
	defer o.Close()
	// Check for 404 / not-found by reading the first byte.
	var buf [1]byte
	if _, err := o.Read(buf[:]); err != nil {
		return fmt.Errorf("archive not found or empty: %w", err)
	}
	// Seek back and write to file.
	if _, err := o.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("seek archive: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(archivePath), 0o755); err != nil {
		return err
	}
	f, err := os.Create(archivePath)
	if err != nil {
		return fmt.Errorf("create archive file: %w", err)
	}
	defer f.Close()
	if _, err := io.Copy(f, o); err != nil {
		return fmt.Errorf("download archive: %w", err)
	}
	return nil
}

// DownloadCheckpointArchive downloads a squashfs archive for a specific
// checkpoint, enabling undo-to-any-checkpoint (§14.2).
func (s *Store) DownloadCheckpointArchive(ctx context.Context, checkpointID, archivePath string) error {
	o, err := s.c.GetObject(ctx, s.bucket, s.checkpointArchiveKey(checkpointID), minio.GetObjectOptions{})
	if err != nil {
		return fmt.Errorf("get checkpoint archive: %w", err)
	}
	defer o.Close()
	var buf [1]byte
	if _, err := o.Read(buf[:]); err != nil {
		return fmt.Errorf("checkpoint archive not found or empty: %w", err)
	}
	if _, err := o.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("seek checkpoint archive: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(archivePath), 0o755); err != nil {
		return err
	}
	f, err := os.Create(archivePath)
	if err != nil {
		return fmt.Errorf("create checkpoint archive file: %w", err)
	}
	defer f.Close()
	if _, err := io.Copy(f, o); err != nil {
		return fmt.Errorf("download checkpoint archive: %w", err)
	}
	return nil
}

// ArchiveExists checks whether a squashfs archive exists in S3.
func (s *Store) ArchiveExists(ctx context.Context) bool {
	_, err := s.c.StatObject(ctx, s.bucket, s.archiveKey(), minio.StatObjectOptions{})
	return err == nil
}

// ListCheckpoints returns checkpoint IDs in reverse chronological order
// (newest first), for the undo-to-checkpoint UI.
func (s *Store) ListCheckpoints(ctx context.Context) ([]string, error) {
	prefix := s.prefix + "/checkpoints/"
	ch := s.c.ListObjects(ctx, s.bucket, minio.ListObjectsOptions{
		Prefix:    prefix,
		Recursive: false,
	})
	var ids []string
	for obj := range ch {
		if obj.Err != nil {
			return nil, obj.Err
		}
		name := strings.TrimPrefix(obj.Key, prefix)
		name = strings.TrimSuffix(name, ".json")
		if name == "" || name == "LATEST" {
			continue
		}
		ids = append(ids, name)
	}
	sort.Sort(sort.Reverse(sort.StringSlice(ids)))
	return ids, nil
}
