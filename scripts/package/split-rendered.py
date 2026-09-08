#!/usr/bin/env python3
"""Split a rendered manifest stream into chart files under Helm's size limit.

Helm refuses any file in a chart larger than 5 MiB. The spoke catalogue renders
to over 12 MB, almost all of it vendored CRDs, so a single rendered.yaml cannot
be packaged. The failure is worth naming: `helm template` reports the limit, but
a packager that discards stderr sees only an empty render and concludes the
chart produces no objects.

Splitting happens at document boundaries, so no object is ever divided across
two files and the set reassembles as the same stream in any order.

Usage: split-rendered.py <input.yaml> <outdir> [max-bytes]
"""
import os
import sys

DEFAULT_MAX = 4 * 1024 * 1024  # under Helm's 5 MiB, with room for indentation


def main() -> int:
    if len(sys.argv) < 3:
        print(__doc__)
        return 2
    src, outdir = sys.argv[1], sys.argv[2]
    limit = int(sys.argv[3]) if len(sys.argv) > 3 else DEFAULT_MAX

    with open(src, "r") as handle:
        text = handle.read()

    # Keep the separator with the document that follows it, so every chunk is a
    # complete stream on its own.
    docs = [d for d in text.split("\n---\n") if d.strip()]

    os.makedirs(outdir, exist_ok=True)
    part, size, index = [], 0, 0
    written = []

    def flush() -> None:
        nonlocal part, size, index
        if not part:
            return
        path = os.path.join(outdir, f"rendered-{index:03d}.yaml")
        with open(path, "w") as out:
            out.write("\n---\n".join(part) + "\n")
        written.append(path)
        index += 1
        part, size = [], 0

    for doc in docs:
        chunk = len(doc) + 5
        if size + chunk > limit and part:
            flush()
        part.append(doc)
        size += chunk
    flush()

    if not written:
        print(f"{src}: no documents to write", file=sys.stderr)
        return 1
    for path in written:
        print(f"{os.path.basename(path)} {os.path.getsize(path)}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
