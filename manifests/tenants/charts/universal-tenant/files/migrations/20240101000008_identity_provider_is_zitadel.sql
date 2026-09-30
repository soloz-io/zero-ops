-- Migration: 20240101000008_identity_provider_is_zitadel
-- Description: `identities.provider` records the provider that actually issued
--              the subject. It said "ory" on a fleet whose issuer is Zitadel.

-- WHY THIS IS A MIGRATION AND NOT A DEFAULT CHANGED IN THE LIBRARY
--
-- `identities` is UNIQUE(provider, provider_user_id), and resolution looks a
-- person up by that pair. Change the name the library sends and every existing
-- row becomes unreachable: the lookup matches nothing, resolve_user concludes
-- the person is new, and inserts a SECOND `users` row for someone who already
-- has one. Nothing fails -- the duplicate is created successfully, owns no
-- records, and the person's data is behind the id they no longer resolve to.
--
-- So the stored value moves in the same change as the value that is sent.
--
-- WHERE "ory" CAME FROM
--
-- A default carried over from an earlier identity provider and never revisited.
-- It worked, which is why it survived: the name is only ever compared against
-- itself, so a wrong one is consistent with itself and invisible. It was found
-- by the oranger team reading a row, not by anything failing.
--
-- Corrected now because the fleet holds a handful of identities. The cost of
-- this migration is proportional to the number of people who have ever signed
-- in, and it only ever grows.

-- Idempotent by the WHERE clause: the second run updates nothing.
--
-- Not an unconditional UPDATE. If a subject somehow holds BOTH an "ory" and a
-- "zitadel" row, this collides with UNIQUE(provider, provider_user_id) and the
-- file fails -- which is correct, and the assertion below says so out loud
-- rather than leaving the failure to a constraint name.
DO $$
DECLARE
  v_conflicts int;
  v_renamed   int;
BEGIN
  SELECT count(*) INTO v_conflicts
  FROM public.identities o
  WHERE o.provider = 'ory'
    AND EXISTS (
      SELECT 1 FROM public.identities z
      WHERE z.provider = 'zitadel'
        AND z.provider_user_id = o.provider_user_id
    );

  IF v_conflicts > 0 THEN
    -- Two rows for one subject means two `users` rows for one person, created
    -- while the name was in flight. Merging them is a decision about whose
    -- records survive, which a migration must not make on its own.
    RAISE EXCEPTION
      'identity provider rename: % subject(s) hold both an "ory" and a "zitadel" identity, so one person has two user rows. Merge them, then re-run.',
      v_conflicts
      USING ERRCODE = 'unique_violation';
  END IF;

  UPDATE public.identities SET provider = 'zitadel' WHERE provider = 'ory';
  GET DIAGNOSTICS v_renamed = ROW_COUNT;
  RAISE NOTICE 'identity provider rename: % row(s) ory -> zitadel', v_renamed;
END
$$;

-- The set re-applies on every sync, so this is also the standing assertion that
-- no "ory" row has come back -- which it would if a workload were still running
-- a zero-ops-auth older than 0.16.0.
DO $$
DECLARE
  v_stale int;
BEGIN
  SELECT count(*) INTO v_stale FROM public.identities WHERE provider = 'ory';
  IF v_stale > 0 THEN
    RAISE EXCEPTION
      'identity provider rename: % "ory" identity row(s) reappeared after the rename. A workload is still sending the old provider name; upgrade it to zero-ops-auth >= 0.16.0.',
      v_stale
      USING ERRCODE = 'raise_exception';
  END IF;
END
$$;
