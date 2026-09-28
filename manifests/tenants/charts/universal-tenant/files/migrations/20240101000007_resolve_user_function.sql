-- Migration: resolve a provider subject to a tenant-local user
-- Description: ADR-057's mapping, as a hardened SECURITY DEFINER function (ADR-093)
-- Idempotent: Yes (CREATE OR REPLACE; grants are idempotent)

-- WHY A FUNCTION AND NOT APPLICATION SQL
--
-- ADR-057 decides that resolution is a LIBRARY on the application's own
-- connection, not a service -- no network hop, no second component on the request
-- path. That is unchanged. What changed is that the application no longer owns its
-- tables (ADR-093), so row-level security now applies to it, and resolution runs
-- BEFORE any acting user exists:
--
--   the identities policy is  user_id = claims->>'user_id'
--   at resolution time there are no claims
--   so the lookup matches nothing, the caller concludes the user is absent,
--   creates a second one, and collides on (provider, provider_user_id)
--
-- This function runs as its OWNER, so it sees the identity table; the application
-- can only call it. The privilege the statement runs with changes; where it runs
-- does not.
--
-- WHY THE RACE IS HANDLED HERE AND NOT BY SECURITY DEFINER
--
-- SECURITY DEFINER grants privilege. It does not make anything atomic. Two
-- requests from the same person arriving together both observe no identity and
-- both try to create one.
--
-- The BEGIN/EXCEPTION block below is a subtransaction: when the identity insert
-- raises unique_violation, EVERYTHING in that block rolls back -- including the
-- user row inserted a line earlier. That is what prevents the failure ADR-057
-- names, a user record no identity refers to, invisible to every later lookup and
-- recreated on every request. The loser then re-reads and adopts the winner's
-- user, rather than returning an id nothing points at.
--
-- HARDENING (ADR-093)
--
--   search_path is fixed and trusted, so an unqualified name cannot be shadowed
--   by a caller's schema; every reference below is schema-qualified anyway.
--   EXECUTE is revoked from PUBLIC -- otherwise every role on the cluster could
--   run a function that bypasses RLS -- and granted only to the runtime role.
--   No dynamic SQL. The return is the user id, the address, and whether the user
--   was created -- what the caller needs and nothing more. The address is returned
--   rather than looked up by the caller because under RLS the caller CANNOT read
--   public.users until it has a user context, and it has no context until this
--   returns.

-- THE CONSTRAINT NAMES ARE A CONTRACT, AND THIS ASSERTS IT.
--
-- resolve_user branches on CONSTRAINT_NAME to tell a genuine identity race from
-- an email collision -- the distinction that stops it looping forever. That makes
-- these two names part of the baseline's contract rather than an incidental
-- detail of how the tables were written.
--
-- Asserted here, in the migration that depends on them, and on every sync. A
-- separate test would drift from the schema it describes; this cannot, because it
-- runs against the database the function is about to be created in.
--
-- The function already fails safe if a name changes -- an unrecognised constraint
-- re-raises rather than looping -- but failing at migration time names the cause,
-- while failing at request time only says a user could not be resolved.
DO $contract$
DECLARE
  missing text[] := ARRAY[]::text[];
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_catalog.pg_constraint
     WHERE conname = 'identities_provider_provider_user_id_key'
       AND conrelid = 'public.identities'::regclass
  ) THEN
    missing := missing || 'identities_provider_provider_user_id_key';
  END IF;

  IF NOT EXISTS (
    SELECT 1 FROM pg_catalog.pg_constraint
     WHERE conname = 'users_email_key'
       AND conrelid = 'public.users'::regclass
  ) THEN
    missing := missing || 'users_email_key';
  END IF;

  IF array_length(missing, 1) > 0 THEN
    RAISE EXCEPTION
      'baseline contract broken: resolve_user branches on constraint name(s) % which do not exist. Renaming or dropping them changes how an identity race is told apart from an email collision.',
      array_to_string(missing, ', ')
      USING ERRCODE = 'undefined_object';
  END IF;
END
$contract$;

-- DROP before CREATE, deliberately.
--
-- CREATE OR REPLACE cannot change a function's RETURN TYPE -- it fails with
-- "cannot change return type of existing function". This migration re-runs on
-- every sync against databases that may hold an earlier signature, so replacing
-- without dropping would wedge the whole baseline on one statement. The argument
-- list identifies the function for DROP; the return type does not, so this
-- matches whatever shape is there.
--
-- Safe because nothing holds a persistent reference: the function is called by
-- name on each request, and dropping it inside this transaction is invisible to
-- anything until commit.
DROP FUNCTION IF EXISTS public.resolve_user(text, text, text);

CREATE OR REPLACE FUNCTION public.resolve_user(
  p_provider text,
  p_subject  text,
  p_email    text DEFAULT NULL
)
RETURNS TABLE (user_id uuid, email text, is_new boolean)
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, pg_temp
AS $fn$
DECLARE
  v_user_id    uuid;
  v_email      text;
  v_constraint text;
  v_attempts   int := 0;
BEGIN
  IF p_provider IS NULL OR p_subject IS NULL
     OR length(p_provider) = 0 OR length(p_subject) = 0 THEN
    RAISE EXCEPTION 'resolve_user: provider and subject are required'
      USING ERRCODE = 'invalid_parameter_value';
  END IF;

  LOOP
    -- BOUNDED. A genuine race resolves on the first retry: the loser re-reads and
    -- finds the winner's identity. More than a couple of passes means something
    -- structural, and an unbounded loop in a function on the request path is a
    -- hung connection rather than an error.
    v_attempts := v_attempts + 1;
    IF v_attempts > 3 THEN
      RAISE EXCEPTION
        'resolve_user: gave up resolving %/% after % attempts', p_provider, p_subject, v_attempts - 1
        USING ERRCODE = 'internal_error';
    END IF;

    -- Already mapped: the overwhelmingly common path.
    SELECT i.user_id, u.email INTO v_user_id, v_email
      FROM public.identities i
      JOIN public.users u ON u.id = i.user_id
     WHERE i.provider = p_provider
       AND i.provider_user_id = p_subject;

    IF FOUND THEN
      -- Refresh a changed address so the tenant does not display a stale one.
      -- Done HERE rather than by the caller because the caller cannot: under RLS
      -- it cannot see the row until it has a user context, and it has no context
      -- until this function returns. Guarded on inequality -- an unconditional
      -- UPDATE would write on every authenticated request.
      IF p_email IS NOT NULL AND length(p_email) > 0 THEN
        UPDATE public.users u
           SET email = p_email
         WHERE u.id = v_user_id
           AND u.email IS DISTINCT FROM p_email;
      END IF;

      user_id := v_user_id;
      email   := coalesce(nullif(p_email, ''), v_email);
      is_new  := false;
      RETURN NEXT;
      RETURN;
    END IF;

    -- First sighting. users.email is NOT NULL in the baseline, so a subject with
    -- no address cannot be provisioned. Fail with something that names the cause
    -- rather than letting a NOT NULL violation surface from two frames down.
    IF p_email IS NULL OR length(p_email) = 0 THEN
      RAISE EXCEPTION
        'resolve_user: cannot provision a user for subject % without an email claim', p_subject
        USING ERRCODE = 'not_null_violation';
    END IF;

    -- Not mapped. Create the user and the identity together; if another
    -- transaction wins the race, this whole block rolls back and we re-read.
    BEGIN
      INSERT INTO public.users (email)
           VALUES (p_email)
        RETURNING public.users.id INTO v_user_id;

      INSERT INTO public.identities (user_id, provider, provider_user_id)
           VALUES (v_user_id, p_provider, p_subject);

      user_id := v_user_id;
      email   := p_email;
      is_new  := true;
      RETURN NEXT;
      RETURN;
    EXCEPTION
      WHEN unique_violation THEN
        -- WHICH constraint decides what this means. Treating both as "someone
        -- else got there first" was a real infinite loop: an email collision is
        -- not a race, so re-reading finds no identity, the insert is retried, and
        -- it fails identically forever. Reproduced against a live database, where
        -- the call spun until statement_timeout killed it.
        GET STACKED DIAGNOSTICS v_constraint = CONSTRAINT_NAME;

        IF v_constraint = 'identities_provider_provider_user_id_key' THEN
          -- A genuine race: another transaction created this identity between our
          -- read and our write. The user row inserted a line earlier rolls back
          -- with this block, so no orphan is left. Loop and adopt the winner.
          NULL;

        ELSIF v_constraint = 'users_email_key' THEN
          -- A DIFFERENT subject already holds this address. Refused, deliberately.
          --
          -- Linking the new subject to the existing user would be account linking
          -- by email, and an address is mutable at the provider and reassignable
          -- between people -- which is precisely why ADR-057 makes the subject the
          -- join key and never the address. Doing it silently here would
          -- reintroduce that as a takeover path: acquire an address, authenticate
          -- with a new subject, inherit the previous holder's records.
          --
          -- Deterministic failure instead. Linking a second provider identity to
          -- an existing user is a real requirement, but it needs an explicit
          -- verified-linking flow with the acting user's consent, not an implicit
          -- consequence of a collision.
          RAISE EXCEPTION
            'resolve_user: address % is already registered to a different identity; linking a new subject to an existing user requires an explicit verified-linking flow', p_email
            USING ERRCODE = 'unique_violation';

        ELSE
          -- An unknown constraint. Re-raise rather than loop: the two names above
          -- are the baseline's, and if one is renamed this must fail loudly rather
          -- than silently degrade into the loop it was written to prevent.
          RAISE;
        END IF;
    END;
  END LOOP;
END;
$fn$;

-- The function bypasses RLS by construction, so who may call it is the whole
-- security boundary. PUBLIC must not be able to.
REVOKE ALL ON FUNCTION public.resolve_user(text, text, text) FROM PUBLIC;
SELECT format('GRANT EXECUTE ON FUNCTION public.resolve_user(text, text, text) TO %I', :'runtime_role')
\gexec

COMMENT ON FUNCTION public.resolve_user(text, text, text) IS
  'ADR-057/ADR-093: map a provider subject to a tenant-local user, creating both on first sight. SECURITY DEFINER because resolution predates the RLS user context.';
