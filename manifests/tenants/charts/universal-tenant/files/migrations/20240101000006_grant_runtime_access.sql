-- Migration: grant the RUNTIME role access to what the OWNER created
-- Description: ADR-093. Runs last, as the owner, granting to :runtime_role.
-- Idempotent: Yes (GRANT and ALTER DEFAULT PRIVILEGES are both idempotent)

-- WHY THIS FILE EXISTS
--
-- Until ADR-093 the migration Job connected as the application's own role, so the
-- application OWNED every table above -- and PostgreSQL exempts a table's owner
-- from its own row-level security policies. Every policy created above was inert:
-- measured on a live spoke, a query as the application's role with no claims set
-- returned every row where the policy would have matched none.
--
-- The Job now runs as tenant_<tenantId>_<appId>_owner. That fixes the exemption
-- and removes the application's access at the same time, because it no longer owns
-- anything. This file is the other half: it grants back exactly what the
-- application needs, as a non-owner, so the policies apply to it.
--
-- WHY IT CANNOT BE LEFT TO DefaultPrivileges
--
-- The composition configures ALTER DEFAULT PRIVILEGES so that objects the owner
-- creates grant DML to the runtime role. Those apply ONLY to objects created
-- AFTER they are set. Every table above already exists on any spoke that has run
-- the baseline before, so default privileges alone would leave a live application
-- able to reach nothing. Existing objects need explicit grants; future ones need
-- the defaults. Both, or the application is broken in one direction or the other.
--
-- :runtime_role is passed by the Job with --set and interpolated as a quoted
-- IDENTIFIER (:"runtime_role"), never as text.

-- WHAT THIS DOES NOT COVER: SCHEMAS THAT DO NOT EXIST YET
--
-- Everything below applies to the schemas present WHEN THIS FILE RUNS. A later
-- migration that creates a new application schema does not inherit any of it --
-- not the USAGE grant, not the grants on its tables, not the default privileges
-- for objects created in it afterwards.
--
-- THE RULE: a migration that creates an application schema must establish that
-- schema's runtime USAGE and default privileges in the same file. Creating the
-- schema and granting access to it are one change, not two.
--
-- Deliberately NOT solved by making this file watch for new schemas at runtime.
-- A grant that appears because a schema appeared is a privilege nobody decided
-- to give: it would mean any future schema silently becomes readable by the
-- runtime role, including one created for a purpose that should not be. Schema
-- creation stays declarative and its access stays explicit.
--
-- This file iterating over existing schemas is not the same thing: it is
-- repairing what ALREADY exists on a database that predates the owner/runtime
-- split, which is a migration concern, not a standing policy.

-- EVERY SCHEMA, NOT JUST public.
--
-- This grants over all non-system schemas, because a tenant's tables are not all
-- in public and the ones outside it are the ones that break loudest. waypoint
-- holds 118 relations in public and 50 more across workflow, graphile_worker,
-- drizzle and workflow_drizzle; a public-only grant would have left its SDK unable
-- to read its own workflow tables the moment ownership moved -- and unlike the
-- baseline tables those carry no policies, so the failure is plain permission
-- denial on every query rather than an empty result.
--
-- \gexec rather than a DO block: psql interpolates :'runtime_role' in ordinary SQL
-- but NOT inside a dollar-quoted body, so a DO block cannot see it. Each SELECT
-- below builds one statement per schema and \gexec runs them. %I quotes each
-- identifier, so a schema named oddly cannot become injected SQL.

-- Schema access. Without USAGE the object grants below are unreachable.
SELECT format('GRANT USAGE ON SCHEMA %I TO %I', nspname, :'runtime_role')
  FROM pg_catalog.pg_namespace
 WHERE nspname NOT IN ('pg_catalog', 'information_schema')
   AND nspname NOT LIKE 'pg\_toast%' AND nspname NOT LIKE 'pg\_temp%'
 ORDER BY nspname
\gexec

-- Existing objects.
--
-- No TRUNCATE and no REFERENCES: the application reads and writes rows, and
-- neither dropping every row nor creating foreign keys is part of that. The
-- composition's DefaultPrivileges are broader for historical reasons; this is the
-- set a request path actually needs, and the narrower of the two is the one to
-- converge on.
SELECT format('GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA %I TO %I', nspname, :'runtime_role')
  FROM pg_catalog.pg_namespace
 WHERE nspname NOT IN ('pg_catalog', 'information_schema')
   AND nspname NOT LIKE 'pg\_toast%' AND nspname NOT LIKE 'pg\_temp%'
 ORDER BY nspname
\gexec

SELECT format('GRANT SELECT, USAGE ON ALL SEQUENCES IN SCHEMA %I TO %I', nspname, :'runtime_role')
  FROM pg_catalog.pg_namespace
 WHERE nspname NOT IN ('pg_catalog', 'information_schema')
   AND nspname NOT LIKE 'pg\_toast%' AND nspname NOT LIKE 'pg\_temp%'
 ORDER BY nspname
\gexec

-- Future objects created by THIS role (the owner), for anything a later migration
-- adds between now and the next time this file runs.
SELECT format('ALTER DEFAULT PRIVILEGES IN SCHEMA %I GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO %I', nspname, :'runtime_role')
  FROM pg_catalog.pg_namespace
 WHERE nspname NOT IN ('pg_catalog', 'information_schema')
   AND nspname NOT LIKE 'pg\_toast%' AND nspname NOT LIKE 'pg\_temp%'
 ORDER BY nspname
\gexec

SELECT format('ALTER DEFAULT PRIVILEGES IN SCHEMA %I GRANT SELECT, USAGE ON SEQUENCES TO %I', nspname, :'runtime_role')
  FROM pg_catalog.pg_namespace
 WHERE nspname NOT IN ('pg_catalog', 'information_schema')
   AND nspname NOT LIKE 'pg\_toast%' AND nspname NOT LIKE 'pg\_temp%'
 ORDER BY nspname
\gexec

-- The runtime role must NOT be able to create objects: an object it created would
-- be one it owns, and one it owns is one whose policies do not apply to it. This
-- is the defect ADR-093 removes, and revoking CREATE is what stops it reappearing
-- one migration at a time.
SELECT format('REVOKE CREATE ON SCHEMA %I FROM %I', nspname, :'runtime_role')
  FROM pg_catalog.pg_namespace
 WHERE nspname NOT IN ('pg_catalog', 'information_schema')
   AND nspname NOT LIKE 'pg\_toast%' AND nspname NOT LIKE 'pg\_temp%'
 ORDER BY nspname
\gexec
