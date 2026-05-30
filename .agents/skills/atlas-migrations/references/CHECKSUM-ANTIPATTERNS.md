# atlas.sum Checksum Anti-Patterns

Lessons learned from production incidents involving atlas.sum checksum
mismatches. Read alongside SKILL.md's "Anti-Patterns" section.

---

## Anti-Pattern 1: Hashing the content alone

```sql
-- ❌ WRONG: base64(sha256(content))
echo "-- Migration: foo" | sha256sum | base64

-- ✅ CORRECT: base64(sha256(filename + content))
printf '%s' '20250101000001_migration.sql' > /tmp/hash_input
cat migration.sql >> /tmp/hash_input
openssl sha256 -binary /tmp/hash_input | openssl base64 -A
```

**Root cause:** `NewHashFile` (atlas `sql/migrate/dir.go:662-678`) feeds
`filename` then `content` into the **same** `sha256` instance without
resetting it. A file hash is `base64(sha256(filename + file_content))`,
not `base64(sha256(file_content))`.

**Lesson:** Always compute the hash over `filename || content`, not content
alone. If you must hand-verify, concatenate filename (no separator) with
the raw file bytes before hashing.

---

## Anti-Pattern 2: Assuming `atlas.go.sh` works on every platform

```bash
# ❌ FAILS on Windows (MINGW64, MSYS, Cygwin)
curl -sSf https://atlasgo.sh | sh

# ✅ Use the direct binary URL instead
curl -sL "https://release.ariga.io/atlas/atlas-windows-amd64-${VERSION}.exe" \
  -o ~/bin/atlas.exe
```

**Root cause:** The official install script does not recognise `MINGW64_NT-*`
as a valid OS type, so it aborts.

**Lesson:** On Windows, download the `.exe` directly from
`https://release.ariga.io/atlas/atlas-windows-amd64-{version}.exe`. On Linux
and macOS the install script works.

---

## Anti-Pattern 3: `file://` URLs on Windows

```bash
# ❌ FAILS on Windows — Atlas parses "C:" as a URL scheme
atlas migrate hash --dir "file:///C:/path/to/migrations"

# ❌ ALSO FAILS — Go url.Parse produces \C:\path
atlas migrate hash --dir "file://localhost/C:/path/to/migrations"

# ✅ Use the native path (if Atlas build supports it)
# or compute the hash manually with openssl (see Anti-Pattern 1)
```

**Root cause:** Go's `url.Parse` on Windows treats `C:` as a host/scheme,
producing an incorrect native path like `\C:\path`. Atlas's `LocalDir`
then calls `CreateFile` with this broken path.

**Lesson:** Do not rely on `file://` URLs for Atlas on Windows. Use the
CLI on Linux/macOS, or compute the checksum manually with openssl using
the algorithm documented in Anti-Pattern 1.

---

## Anti-Pattern 4: Trusting the first `atlas.sum` format description you read

```text
Line 1: SHA-256 hash of all file-hash lines concatenated
```

The global sum (line 1) is NOT simply `sha256(all_lines)` or
`sha256(line2 + line3 + ...)`. It is computed by `HashFile.Sum()`:

```go
sha := sha256.New()
for _, f := range hashFile {
    sha.Write([]byte(f.N))  // filename
    sha.Write([]byte(f.H))  // file hash (base64 string)
}
globalSum = base64(sha.Sum(nil))
```

So the input is `filename1 || filehash1 || filename2 || filehash2 || ...`
with no newlines or separators between entries.

**Lesson:** When verifying the global sum, concatenate each filename
directly with its base64 hash, then feed all entries sequentially into
sha256, then base64 the result.

---

## Anti-Pattern 5: Hand-editing `atlas.sum` without verifying both levels

When atlas.sum must be hand-edited (e.g. CLI unavailable):

1. **File-level check:** `base64(sha256(filename || content))` must match
   the `h1:` value on the file's line
2. **Directory-level check:** `base64(sha256(filename || filehash_for_file))`
   must match the `h1:` value on line 1

Always verify **both** levels before committing. A mismatch on either
causes `atlas migrate apply` to fail with `"L<N>: <filename> was edited"`.

---

## Anti-Pattern 6: Incomplete trailing newline handling

YAML `|` block scalars append a single trailing `\n`. Kubernetes ConfigMap
volume mounts preserve the exact value bytes. Atlas reads the raw file
bytes via `fs.ReadFile`.

If the content stored in Git/ConfigMap has a trailing newline (from YAML
`|`), the hash **must** include that trailing newline. Omitting it
produces a different digest.

**Safety check:** Dump the exact bytes before hashing:

```bash
# Show trailing newline as $
cat -A migrations/20250101000001_foo.sql | tail -3
```

When extracting from a ConfigMap for offline verification:

```bash
kubectl get configmap -n <ns> <name> \
  -o go-template='{{index .data "<filename>"}}' \
  > /tmp/verify.sql
```

---

## Quick Reference: Manual hash computation

```bash
# 1. Compute file hash
FILENAME="20250101000001_description.sql"
CONTENT_FILE="path/to/${FILENAME}"
printf '%s' "${FILENAME}" > /tmp/_h
cat "${CONTENT_FILE}" >> /tmp/_h
FILE_HASH=$(openssl sha256 -binary /tmp/_h | openssl base64 -A)

# 2. Compute global sum (single file)
GLOBAL_SUM=$(printf '%s' "${FILENAME}${FILE_HASH}" | openssl sha256 -binary | openssl base64 -A)

# 3. Generate atlas.sum
echo "h1:${GLOBAL_SUM}"
echo "${FILENAME} h1:${FILE_HASH}"
```
