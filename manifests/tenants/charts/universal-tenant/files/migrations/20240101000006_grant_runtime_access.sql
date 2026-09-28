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

-- Schema access. Without USAGE the table grants below are unreachable.
GRANT USAGE ON SCHEMA public TO :"runtime_role";

-- Existing objects.
--
-- No TRUNCATE and no REFERENCES: the application reads and writes rows, and
-- neither dropping every row nor creating foreign keys is part of that. The
-- composition's DefaultPrivileges are broader for historical reasons; this is the
-- set a request path actually needs, and the narrower of the two is the one to
-- converge on.
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO :"runtime_role";
GRANT SELECT, USAGE ON ALL SEQUENCES IN SCHEMA public TO :"runtime_role";

-- Future objects created by THIS role (the owner), for anything a later migration
-- adds between now and the next time this file runs.
ALTER DEFAULT PRIVILEGES IN SCHEMA public
  GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO :"runtime_role";
ALTER DEFAULT PRIVILEGES IN SCHEMA public
  GRANT SELECT, USAGE ON SEQUENCES TO :"runtime_role";

-- The runtime role must NOT be able to create objects: an object it created would
-- be one it owns, and one it owns is one whose policies do not apply to it. This
-- is the defect ADR-093 removes, and revoking CREATE is what stops it reappearing
-- one migration at a time.
REVOKE CREATE ON SCHEMA public FROM :"runtime_role";
