# Creator Database Architecture

**Pattern**: Shared-Schema Multi-Tenancy (PlanetScale Recommended)  
**Status**: Proposed  
**Date**: 2026-04-22

---

## Architecture Decision

Use **shared-schema multi-tenancy** where all users share the same tables, isolated by `user_id` column.

## Database Structure

### Single Database for All Users

```
creator_db
├── Metadata Tables (shared by all users)
│   ├── users
│   ├── applications
│   ├── entities
│   ├── fields
│   ├── relationships
│   ├── blueprints
│   └── permissions
│
└── Data Tables (shared by all users)
    ├── entity_records (polymorphic storage)
    ├── field_values (EAV for flexible fields)
    └── workflow_states
```

### Core Tables

**Metadata Layer:**
```sql
-- User accounts
users (id, email, name, created_at)

-- Applications created by users
applications (id, user_id, name, type, created_at)

-- Entities within applications (Order, Customer, Supplier)
entities (id, application_id, user_id, name, table_name, created_at)

-- Fields for each entity
fields (id, entity_id, name, type, config, is_indexed)

-- Relationships between entities
relationships (id, source_entity_id, target_entity_id, type, config)

-- Workflow blueprints
blueprints (id, application_id, user_id, name, definition)

-- User roles and permissions
permissions (id, application_id, role, entity_id, actions)
```

**Data Storage:**

**Typed Columns with Sparse Tables (Recommended for Reports)**
```sql
-- Shared table with typed columns for fast filtering/sorting
entity_records (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    entity_id BIGINT NOT NULL,
    user_id BIGINT NOT NULL,        -- Isolation column (BIGINT for performance)
    
    -- Typed columns for common field types
    text_1, text_2, ..., text_20,      -- Names, emails, statuses
    number_1, number_2, ..., number_10, -- Deal values, quantities
    date_1, date_2, ..., date_10,       -- Contact dates, deadlines
    bool_1, bool_2, ..., bool_5,        -- Flags, checkboxes
    ref_1, ref_2, ..., ref_10,          -- Foreign keys to other entities (BIGINT)
    
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
)

-- Extension table for entities with >50 fields
entity_records_ext_1 (
    record_id BIGINT PRIMARY KEY REFERENCES entity_records(id),
    text_21, text_22, ..., text_40,
    number_11, number_12, ..., number_20,
    date_11, date_12, ..., date_20
)

-- Field mapping stored in metadata
field_mappings (field_id, column_name, column_type, extension_table)

-- B-tree indexes for fast filtering and sorting
CREATE INDEX idx_records_user_entity ON entity_records(user_id, entity_id);
CREATE INDEX idx_records_text_1 ON entity_records(user_id, entity_id, text_1);
CREATE INDEX idx_records_date_1 ON entity_records(user_id, entity_id, date_1);
CREATE INDEX idx_records_number_1 ON entity_records(user_id, entity_id, number_1);
```

**Why Typed Columns:**
- ✅ Native B-tree indexes for fast WHERE, ORDER BY, range queries
- ✅ Query planner can estimate selectivity accurately
- ✅ Supports multi-column sorting (Opportunity Stage, then Deal Value, then Date)
- ✅ Efficient comparison operators (>, <, BETWEEN, LIKE)
- ✅ Composite indexes for common filter combinations
- ✅ Extension tables handle entities with >50 fields (auto-joined by application)
- ✅ BIGINT for user_id/entity_id provides faster comparisons than text

**JSONB Alternative (Not Recommended):**
- ❌ GIN indexes slower than B-tree for sorting
- ❌ Type casting overhead (data->>'field')::numeric
- ❌ Poor query planner statistics
- ❌ Multi-column sorts require multiple JSONB extractions

## Tenant Isolation

**User-Level Isolation:**
- Every query filters by `user_id`
- Application-layer enforcement via ORM scopes
- Optional RLS policies for defense-in-depth

**Example Queries:**
```sql
-- Filter by Opportunity Stage = "Proposal" AND Deal Value > 5000
SELECT * FROM entity_records 
WHERE user_id = 101 
  AND entity_id = 5
  AND text_1 = 'Proposal'      -- Opportunity Stage mapped to text_1
  AND number_1 > 5000          -- Deal Value mapped to number_1
ORDER BY date_1 DESC           -- Contact Date mapped to date_1
LIMIT 50;

-- Multi-column sort: Stage, then Value, then Date
SELECT * FROM entity_records 
WHERE user_id = 101 AND entity_id = 5
ORDER BY text_1, number_1 DESC, date_1 DESC;

-- Range filter on dates
SELECT * FROM entity_records 
WHERE user_id = 101 
  AND entity_id = 5
  AND date_1 BETWEEN '2023-01-01' AND '2023-12-31';
```

## Partitioning Strategy

For scale (1000+ users, millions of records):

**Hybrid Partitioning Approach:**

```sql
-- Default: Range partitioning for standard users
CREATE TABLE entity_records (...)
PARTITION BY RANGE (user_id);

-- Create partitions for user cohorts
CREATE TABLE entity_records_p1 PARTITION OF entity_records 
    FOR VALUES FROM (1) TO (1000);
CREATE TABLE entity_records_p2 PARTITION OF entity_records 
    FOR VALUES FROM (1000) TO (2000);

-- For "whale" enterprise customers, use LIST partitioning
-- to isolate their data into dedicated partitions
CREATE TABLE entity_records (...)
PARTITION BY LIST (user_id);

CREATE TABLE entity_records_enterprise_acme PARTITION OF entity_records 
    FOR VALUES IN (5001);  -- Acme Corp generates 40% of traffic
CREATE TABLE entity_records_enterprise_globex PARTITION OF entity_records 
    FOR VALUES IN (5002);  -- Globex Inc generates 30% of traffic
CREATE TABLE entity_records_default PARTITION OF entity_records DEFAULT;
```

**Benefits:**
- Standard users share range partitions (cost efficient)
- Enterprise "whale" customers get dedicated partitions
- Isolated vacuum and index maintenance per partition
- Easy tenant offboarding (DROP PARTITION)
- Partition pruning improves query performance

## Why This Architecture

**Follows PlanetScale Best Practices:**
- ✅ Shared-schema is the recommended approach
- ✅ Single database for all users (cost efficient)
- ✅ `user_id` column for isolation
- ✅ Scales to thousands of users
- ✅ Simple migrations (one schema to update)
- ✅ Cross-user analytics possible
- ✅ Partitioning available for scale

**Avoids Anti-Patterns:**
- ❌ No dynamic table creation per user
- ❌ No schema-per-user (catalog bloat)
- ❌ No database-per-user (connection pool issues)

## Trade-offs

**Typed Columns with Sparse Tables (Chosen Approach):**
- ✅ Fast filtering, sorting, and range queries with B-tree indexes
- ✅ Native SQL operators (=, >, <, BETWEEN, LIKE, IN)
- ✅ Multi-column sorting without performance penalty
- ✅ Query planner can optimize with accurate statistics
- ✅ Supports complex report views with multiple filters
- ✅ BIGINT isolation columns faster than text-based identifiers
- ⚠️ Base table supports ~50 fields; extension tables auto-joined for >50 fields
- ⚠️ Sparse table with NULLs for unused columns
- ⚠️ Field type changes require migration

**JSONB (Not Chosen):**
- ✅ Unlimited fields per entity without extension tables
- ✅ No schema changes for new fields
- ❌ Slow filtering/sorting (GIN index + type casting overhead)
- ❌ Poor performance for multi-column sorts
- ❌ Query planner cannot estimate selectivity accurately
- ❌ Unacceptable for report views with complex filters (as shown in UI)

## Scalability Limits

**Single Database Capacity:**
- 10,000+ users
- 100+ entities per user
- 10M+ records per entity
- Partition by `user_id` when approaching limits

**Horizontal Scaling:**
- Shard by user cohorts when single DB limits reached
- Route users to different databases based on `user_id % N`

## Security

- Application-layer `user_id` filtering (primary)
- Optional RLS policies (defense-in-depth)
- Audit logging for all data access
- Encrypted connections (TLS)
- Regular security audits

---

**Conclusion**: Shared-schema multi-tenancy with **typed column sparse table storage** provides the optimal balance of flexibility, performance, and operational simplicity for a low-code platform. This architecture:

- Uses BIGINT for `user_id` and `entity_id` (faster than text identifiers)
- Supports complex report filtering and multi-column sorting via B-tree indexes
- Scales to 100+ fields per entity via extension tables
- Enables hybrid partitioning (range for standard users, list for enterprise whales)
- Follows PlanetScale's recommended shared-schema pattern
- Avoids catalog bloat and connection pool exhaustion
