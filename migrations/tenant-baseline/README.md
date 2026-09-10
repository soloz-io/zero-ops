# Tenant baseline schema

The SQL here describes the schema a tenant database starts with. **Nothing in the
platform applies it.**

Atlas Operator was the mechanism that did, and ADR-074 withdrew it without naming
a replacement. These files are retained because they describe schemas that exist
in running databases and are not regenerable from anything else — not because a
delivery path is waiting for them.

`atlas.sum` is Atlas's checksum of the files beside it. It records the lineage
that was verified when the migrations were authored, and nothing verifies it now.

Whatever owns schema migration next will need to decide whether to adopt these as
a baseline or treat the existing databases as the baseline. That decision is open.
