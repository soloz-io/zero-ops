Your questions are good, and fortunately the Emergent engineering blog actually describes several of these mechanisms explicitly. I’ll answer only using what Emergent itself says and avoid speculation where the article does not state details.

⸻

1️⃣ How Emergent persists the code written by agents

Emergent does NOT persist code by committing to Git or storing raw files in S3 directly.

Instead they use a content-addressed backup system stored in object storage.

Lifecycle described by Emergent:

When a pod starts

An init container restores the workspace state.

Flow:

object storage backup
        ↓
init container
        ↓
restore workspace filesystem
        ↓
main container starts

The result is that the filesystem inside the pod looks exactly like the previous session.  ￼

Key line from their architecture:

The init container pulls the user’s previous working state from a content-addressed backup in object storage.  ￼

So the code lives in:

object storage snapshot

not Git.

⸻

2️⃣ How the work is saved when the session ends

Emergent uses a sidecar container.

During pod shutdown:

preStop hook
     ↓
sidecar container
     ↓
incremental backup
     ↓
object storage

From their description:

On pod termination, the sidecar performs an incremental backup of the working state back to object storage.  ￼

So the persistence loop is:

Session start
   ↓
restore snapshot
   ↓
agent works
   ↓
termination
   ↓
incremental snapshot

This is basically filesystem checkpointing.

⸻

3️⃣ How changes are tracked / reverted

The article reveals an important detail:

content-addressed backup system that scales without an upper bound.  ￼

This means the backups are likely stored similar to:

hash(filesystem)

Typical structure for content-addressed systems:

snapshot
 ├ metadata
 ├ tree hashes
 └ file blobs

Examples of similar systems:
	•	Git
	•	BorgBackup
	•	Restic

Because every snapshot is content addressed, Emergent can:

snapshot_1
snapshot_2
snapshot_3

Reverting simply means:

restore snapshot_N

This is filesystem-level versioning, not Git-level versioning.

So changes are tracked by snapshots, not commits.

⸻

4️⃣ Does the pod run its own database?

Yes — but inside the environment container only for the app the agent is building, not for Emergent’s platform.

The Emergent blog explicitly says the environment contains:
	•	Node
	•	Python
	•	a database
	•	code server
	•	preview server

Meaning the dev environment can run something like:

Postgres
Redis
Mongo

inside the pod if the generated app needs it.  ￼

Important distinction:

Database type	Location
database used by generated app	inside pod
Emergent platform database	not described


⸻

5️⃣ Does each user get their own database?

The article does not mention Emergent’s platform database architecture.

So the following cannot be confirmed from their blog:
	•	whether they use a single DB
	•	org_id separation
	•	database per tenant
	•	schema per tenant

No public information in the article specifies this.

The only database mentioned is:

database inside the development environment pod.  ￼

Which refers to the app being built by the agent, not the Emergent platform.

⸻

6️⃣ The actual storage architecture Emergent describes

From the article we can reconstruct the real stack they describe:

User prompt
      ↓
Kubernetes pod

Pod structure
--------------
Init container
   restore workspace snapshot

Main container
   full Linux dev environment
   Node / Python / database
   agent runs here

Sidecar container
   monitoring
   incremental backup
   snapshot to object storage

Storage:

object storage
   ├ snapshot 1
   ├ snapshot 2
   ├ snapshot 3

Working disk:

network attached SSD
mounted into pod

Important point they mention:

the volume survives node failures and can be reattached to another node.  ￼

⸻

7️⃣ Why they avoided Kubernetes VolumeSnapshots

They briefly mention a scaling issue:

VolumeSnapshot’s 5-million-snapshot wall forced them to move to object-storage backups.  ￼

Meaning:

They initially tried CSI VolumeSnapshots but hit a limit at scale.

So they replaced it with:

content-addressed object storage snapshots


⸻

Key conclusions (specific to Emergent)

Question	Answer
Where is code stored	object storage snapshots
How state restored	init container restore
How state saved	sidecar incremental backup
Versioning method	content-addressed snapshots
DB inside environment	yes (for generated app)
Emergent platform DB architecture	not publicly described


⸻

If you’re interested, the really interesting part of Emergent’s architecture (which the article hints but doesn’t fully explain yet) is this:

how they can snapshot and restore entire Linux workspaces in 2–6 seconds at 30k scale.

That part involves a trick with content-addressed filesystem layers, and it’s the reason their infra cost stays manageable.

-----------------



Short answer: Snapshot storage is usually more expensive than Git for pure source code, but much cheaper for full development environments (which is what platforms like Emergent are storing). The cost depends heavily on what is being stored and how deduplication works.

Let’s analyze it concretely.

⸻

1️⃣ Storage model difference

Git storage

Git stores only source code and history.

Typical repo contents:

src/
package.json
README.md
tests/

What Git does not store:
	•	dependencies (node_modules)
	•	build outputs
	•	caches
	•	installed system packages

Typical repo size:

small project: 1–20 MB
medium project: 50–200 MB


⸻

Snapshot workspace storage

Snapshot systems store entire filesystem states.

Typical workspace snapshot:

src/
node_modules/
venv/
build/
cache/
tmp/

Workspace size:

small project: 200 MB – 1 GB
AI project: 1–5 GB

So raw storage footprint is 10–100× larger than Git.

⸻

2️⃣ But deduplication changes the economics

Snapshot systems use content-addressed storage.

Meaning:

file → hash → stored once globally

Example across many users:

node_modules/react
node_modules/express
node_modules/typescript

Thousands of users share the same files.

Actual stored copy:

1 copy

This is extremely powerful.

Example:

1000 users
node_modules size = 500MB

Without deduplication:

500GB

With deduplication:

~500MB

So large dependency trees become nearly free.

⸻

3️⃣ Snapshot frequency matters

Snapshots are usually incremental.

Example:

snapshot1 = 800MB
snapshot2 = +5MB
snapshot3 = +2MB
snapshot4 = +10MB

Because only changed files are uploaded.

Over time:

1GB project
100 snapshots
≈ 1.3GB total storage

Not 100GB.

⸻

4️⃣ Object storage cost in real numbers

Example using S3-like storage pricing.

Typical price:

$0.02 / GB / month

Example workspace:

avg snapshot size: 1GB

Cost:

1GB → $0.02 / month

Even with 10 snapshots per workspace:

10GB → $0.20 / month

At 10,000 users:

$2000 / month

Which is actually quite cheap.

⸻

5️⃣ Git hosting cost comparison

Git hosting usually includes:
	•	compute
	•	storage
	•	networking
	•	CI integrations

Example:

Platform	Cost
GitHub Team	~$4–10 per user/month
GitLab	~$19 per user/month

Actual storage cost of Git is tiny.

Most of the cost is platform services.

⸻

6️⃣ What happens if each user also has a database?

If every user has their own database, cost increases significantly.

Example with PostgreSQL.

Managed DB minimal size:

1GB

Cost example:

$15–30 per DB/month

At scale:

1000 users → $15k/month

So database-per-user is expensive.

Most SaaS platforms avoid it.

⸻

7️⃣ Typical multi-tenant database strategy

Most systems use one database with org separation.

Example:

projects
users
sessions
workspaces

With:

org_id

Example row:

project_id | org_id | name

Advantages:

cheap
easy scaling
simple backups

This is the standard SaaS architecture.

⸻

8️⃣ What platforms like Emergent likely do

Even though the article doesn’t explicitly say, typical architecture is:

Workspace storage
   → object storage snapshots

Platform metadata
   → single Postgres DB
       org_id separation

Running a DB per user would be economically unrealistic for 30,000 environments.

⸻

9️⃣ Real cost comparison summary

Storage type	Cost efficiency
Git source code	cheapest
Snapshot workspace	medium
Database per user	very expensive


⸻

🔑 Key takeaway

For AI coding platforms:

Git = best for source control
Snapshots = best for environment persistence

So many systems use:

workspace snapshot storage
+ optional git repo


⸻

💡 One interesting thing about the Emergent architecture:
running 30,000 coding environments is actually not the hard part.

The real cost driver in these systems becomes something else entirely — and it’s not compute or storage. If you’re interested, I can break down what that hidden cost is (it surprises most engineers building similar platforms).

==================

Yes. Using a single database for all users is absolutely possible with PostgreSQL, and many large SaaS systems do exactly that. Using a NoSQL database like MongoDB is not required to achieve multi-tenant storage.

The idea is called multi-tenant architecture, and PostgreSQL supports it very well.

⸻

1️⃣ The same approach works in PostgreSQL

Instead of separate databases per user, every table includes a tenant identifier (org/user/workspace id).

Example:

users
------
id
org_id
email

projects
--------
id
org_id
name

sessions
--------
id
org_id
project_id
status

Every query includes the tenant filter:

SELECT * FROM projects
WHERE org_id = 'org_123';

So one database can safely store data for millions of tenants.

⸻

2️⃣ PostgreSQL even has a built-in security feature for this

Postgres supports Row Level Security (RLS).

With RLS you can enforce tenant isolation at the database level.

Example:

ALTER TABLE projects ENABLE ROW LEVEL SECURITY;

CREATE POLICY tenant_isolation
ON projects
USING (org_id = current_setting('app.org_id')::uuid);

Now even if the application forgets to filter by org_id, Postgres will block access.

This makes single-DB multi-tenant systems very safe.

⸻

3️⃣ This architecture scales surprisingly far

Large SaaS platforms run on this model.

Examples using PostgreSQL multi-tenant tables:
	•	Supabase
	•	Notion
	•	Linear

They store all customers in the same database cluster.

Typical pattern:

single Postgres cluster
        ↓
tenant_id column in every table
        ↓
indexes include tenant_id


⸻

4️⃣ Important indexing rule

For performance you must index with the tenant key.

Example:

CREATE INDEX idx_projects_org
ON projects (org_id, id);

Without this, large multi-tenant tables become slow.

⸻

5️⃣ When companies move away from single DB

Even MongoDB systems eventually shard.

The common evolution path is:

Stage 1
Single DB
(org_id separation)

Stage 2
Multiple DB shards
(org_id hash)

Stage 3
Dedicated DB for large enterprise tenants

Most SaaS products stay in Stage 1 or 2 for many years.

⸻

6️⃣ Why Emergent might have chosen MongoDB

For a platform like Emergent, MongoDB gives some convenience:

Advantages:
	•	flexible schema
	•	easier storage of dynamic agent metadata
	•	JSON-like documents
	•	easy horizontal sharding

Example document:

{
  "workspace_id": "ws_123",
  "user_id": "u_456",
  "agent_state": {...},
  "snapshot_id": "snap_789"
}

But Postgres JSONB can do the same thing.

⸻

7️⃣ For your architecture

Since you’re already considering Neon, PostgreSQL is perfectly suitable for:
	•	user accounts
	•	projects
	•	agent workflows
	•	snapshot metadata
	•	orchestration state

Typical table layout:

organizations
users
workspaces
agent_runs
snapshots

Each with:

org_id


⸻

✅ Conclusion

Question	Answer
Can single DB store all users in Postgres	Yes
Is MongoDB required for multi-tenant	No
How to isolate users	org_id column + RLS
Does it scale	Yes (millions of tenants possible)


⸻

If you’re interested, there is also a fourth multi-tenant pattern used by some AI platforms that combines Postgres with database branching (something platforms like Neon support). It can be very useful for per-workspace isolation without huge cost, which might actually fit your agent-workflow architecture well.

==================

Comparing object-storage filesystem snapshots (like Emergent uses) vs Git repositories is mostly about what problem you are solving. They optimize for very different things.

Below is a direct efficiency comparison in the context of AI coding environments.

⸻

1️⃣ Storage efficiency

Object-storage snapshots (content-addressed)

Systems like the one Emergent describes store files using content hashes.

file → hash → stored once

If a file doesn’t change, it is not duplicated in later snapshots.

Example:

snapshot1
 ├ app.js
 ├ package.json
 └ node_modules/

snapshot2
 ├ app.js (changed)
 ├ package.json (same)
 └ node_modules (same)

Only the changed file blobs are stored again.

So storage usage becomes:

base snapshot + changed blocks

This is extremely efficient when large folders exist (like node_modules).

⸻

Git

Git also uses content-addressed storage.

blob → hash
tree → hash
commit → hash

So unchanged files are reused.

However Git has two inefficiencies for AI environments:

1️⃣ Git tracks source files well, but not large build artifacts
2️⃣ Git repositories become heavy when frequent automated commits happen

Example:

AI agent commits every 10 seconds

Repo history explodes.

⸻

Storage efficiency summary

Feature	Object storage snapshots	Git
deduplication	yes	yes
large binary handling	excellent	poor
dependency folders	efficient	bad
frequent updates	efficient	inefficient

Winner for AI environments → object snapshots

⸻

2️⃣ Restore speed

This is where snapshots win massively.

Snapshot restore

A workspace restore looks like:

download snapshot metadata
mount filesystem
lazy load blobs

Startup time:

~2–5 seconds

Because the system may lazy load files only when accessed.

⸻

Git restore

To restore a workspace:

git clone
npm install
pip install
build artifacts

Time:

30 seconds – several minutes

For agent environments this is too slow.

⸻

Restore comparison

Operation	Object snapshots	Git
restore workspace	seconds	minutes
restore dependencies	instant	reinstall
cold start	fast	slow

Winner → snapshots

⸻

3️⃣ Versioning & revert capability

Git is far better for human workflows.

Git supports:
	•	commits
	•	branches
	•	merges
	•	diff
	•	blame
	•	PR review

Snapshot systems support only:

snapshot1
snapshot2
snapshot3

Revert means:

restore snapshot2

There is no semantic change tracking.

⸻

Versioning comparison

Feature	Object snapshots	Git
revert	yes	yes
diff	limited	excellent
collaboration	poor	excellent
branches	no	yes

Winner → Git

⸻

4️⃣ Compute cost

Git operations require:

commit
pack
push
pull
clone

These involve CPU and network.

Snapshot systems do:

incremental block upload

Which is much cheaper at scale.

Emergent likely chose snapshots because they run 30k+ environments.

⸻

5️⃣ Why Emergent likely avoided Git

For an AI coding platform, Git causes problems:

Problem 1 — commit spam

AI agents modify files constantly.

save file
commit
save file
commit

Repos explode.

⸻

Problem 2 — dependency folders

AI-generated projects often contain:

node_modules
venv
build/

Git does poorly here.

Snapshots handle them easily.

⸻

Problem 3 — environment reproducibility

Snapshots preserve:

filesystem
installed packages
cache
temporary files

Git preserves only:

source code


⸻

6️⃣ Real architecture many platforms use

Many platforms combine both.

workspace → snapshot storage
source code → git repo

Example workflow:

agent session
      ↓
snapshot restore
      ↓
agent modifies files
      ↓
snapshot saved
      ↓
optional git commit

This gives:
	•	fast environments
	•	version control

⸻

7️⃣ Efficiency comparison (realistic numbers)

Approximate values from similar systems.

Metric	Object snapshots	Git
restore environment	2–6 s	30–120 s
storage overhead	very low	moderate
dependency storage	excellent	poor
large files	excellent	poor
versioning	weak	excellent


⸻

8️⃣ Why this matters for agent platforms

Systems like:
	•	Replit
	•	Gitpod
	•	CodeSandbox

are slowly moving toward snapshot-based workspaces for this reason.

AI agents create too many environment mutations for Git to track efficiently.

⸻

✅ Simple takeaway

If the goal is	Best tool
developer collaboration	Git
fast AI coding environments	snapshot storage


⸻

If you want, I can also explain something very relevant to your platform architecture:

There is a new filesystem technology used by AI dev platforms that reduces workspace restore to under 1 second, and it’s likely the direction platforms like Emergent will move next.

---------

There are three layers of “snapshot storage” systems commonly used in platforms like Emergent:
	1.	Snapshot engines (software that creates deduplicated snapshots)
	2.	Object storage backends (where snapshots are stored)
	3.	Distributed storage systems with built-in snapshots

Below are the actual tools/services used in production systems.

⸻

1️⃣ Snapshot engines (most common for agent environments)

These tools create incremental filesystem snapshots and store them in object storage.

Restic
	•	Open-source backup system
	•	Content-addressed snapshots
	•	Deduplication + encryption
	•	Stores snapshots in object storage

Used with Kubernetes tools like Velero to back up volumes.  ￼

Typical storage backends:

S3
GCS
Azure Blob
MinIO


⸻

Kopia

Modern alternative to Restic.

Features:
	•	faster snapshotting
	•	deduplicated chunks
	•	incremental snapshots
	•	compression

Used by modern Kubernetes backup tools.

Example:

snapshot
   ↓
Kopia chunk store
   ↓
S3 bucket


⸻

BorgBackup

Another snapshot tool with:
	•	block-level deduplication
	•	compression
	•	encrypted snapshots

Many infrastructure systems use Borg to move point-in-time snapshots to remote storage.  ￼

⸻

2️⃣ Object storage used for snapshot storage

Snapshot tools normally store their data in object storage.

Common providers:

Amazon Web Services S3

Most common backend.

Advantages:
	•	extremely scalable
	•	cheap storage
	•	high durability

⸻

Cloudflare R2

Used by many startups.

Benefits:
	•	no egress cost
	•	S3-compatible

⸻

Google Cloud Cloud Storage

⸻

Microsoft Azure Blob Storage

⸻

MinIO

Self-hosted S3-compatible object storage.

Very popular in:

Kubernetes
self-hosted clouds
on-prem setups


⸻

3️⃣ Kubernetes volume snapshot systems

These snapshot the entire disk volume.

CSI Volume Snapshots

Kubernetes built-in system.

It uses the Container Storage Interface (CSI) to create point-in-time snapshots of volumes.  ￼

Supported providers:
	•	AWS EBS
	•	Azure Managed Disk
	•	Google Persistent Disk
	•	Ceph RBD

⸻

Longhorn

Distributed storage with snapshots.

Features:

volume snapshots
incremental backup
S3 backup

Very common in Kubernetes clusters.

⸻

Ceph / Rook

Distributed storage system with:

instant snapshots
copy-on-write
replication

Used in many large clusters.

⸻

4️⃣ Backup orchestrators used in Kubernetes

These manage snapshot lifecycle.

Velero

Most popular Kubernetes backup system.

Features:

volume snapshot
object storage backup
restore workflows

It can use Restic or Kopia to store snapshots in object storage.  ￼

⸻

5️⃣ Systems closest to what Emergent likely uses

For AI coding environments, typical stack is:

workspace filesystem
      ↓
snapshot engine
      ↓
object storage

Example real stack:

Kopia
   ↓
S3 / R2

or

Restic
   ↓
S3 / MinIO

These allow:

snapshot1
snapshot2
snapshot3

with deduplicated blobs.

⸻

6️⃣ Snapshot storage systems used by dev platforms

Platforms similar to Emergent likely rely on combinations of:

Layer	Tools
snapshot engine	Kopia / Restic
storage backend	S3 / R2 / GCS
orchestration	Velero / custom
local workspace	Kubernetes PVC


⸻

✅ Most modern stack (2024-2026)

Kopia
   ↓
S3 / R2
   ↓
deduplicated snapshots

This is becoming the standard snapshot architecture for cloud platforms.

⸻

💡 Since you’re building an agentic platform similar to Emergent, there is actually a much newer snapshot system used by AI coding platforms that is faster than Restic/Kopia and optimized for instant workspace restore (<1s).

If you’d like, I can explain that system too — it’s what platforms like **Replit and modern cloud IDEs are moving toward.