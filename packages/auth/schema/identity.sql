-- The two tables `resolveUser()` expects. COPY THIS INTO YOUR APPLICATION.
--
-- It is an example, not a migration the platform runs. The platform provisions a
-- database and a role; everything inside it is yours, including these. Put this
-- in your own migrations, and from then on the shape is yours to change.
--
-- WHY THE LIBRARY EXISTS AT ALL, GIVEN YOU OWN THE TABLES
--
-- Zitadel gives you a SUBJECT -- an opaque string, stable within one issuer. Your
-- records need a local key. Mapping one to the other is fifty lines that every
-- application writes once, and there is exactly one way to get it wrong that
-- does not announce itself:
--
--   joining on EMAIL. An address is mutable at the provider and can be
--   reassigned between people. A lookup keyed on it merges two accounts the
--   moment an address changes hands, and the result is one person reading
--   another's records with nothing reporting an error.
--
-- `resolveUser()` joins on (provider, provider_user_id) and never on the address.
-- That rule, and the race below, are the whole of what the library is for.
--
-- THIS MATCHES WHAT IS ALREADY IN YOUR DATABASE.
--
-- Until 2026-10-01 the platform created these tables itself. If your application
-- was provisioned before then, they exist and hold rows: adopting this file is a
-- no-op against them, and `CREATE TABLE IF NOT EXISTS` says so. Do not drop and
-- recreate -- `identities` is how your existing users are found.
--
-- Two things the platform used to do that are now yours, and you must NOT redo:
--
--   * `identities.provider` was rewritten from 'ory' to 'zitadel'. That migration
--     has run. Do not run it again and do not write 'ory' anywhere.
--   * row-level security was enabled on these tables with policies keyed on
--     `request.jwt.claims`. It has been removed. It never constrained the
--     application -- the application sets that claim -- so it was a backstop
--     against a forgotten WHERE clause, not a boundary. Add your own if you want
--     that backstop, knowing what it is.

CREATE TABLE IF NOT EXISTS public.users (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  -- NOT NULL: `resolveUser` refuses to provision without an email claim, and
  -- says so naming the subject rather than letting this constraint explain it.
  -- UNIQUE: two subjects arriving at one address is an account-LINKING decision
  -- that needs proof the same human holds both. The library refuses it rather
  -- than adopting silently.
  email VARCHAR(255) NOT NULL UNIQUE,
  email_verified BOOLEAN DEFAULT FALSE,
  created_at TIMESTAMPTZ DEFAULT NOW(),
  updated_at TIMESTAMPTZ DEFAULT NOW(),
  metadata JSONB DEFAULT '{}'::jsonb
);

CREATE INDEX IF NOT EXISTS idx_users_email ON public.users(email);

CREATE TABLE IF NOT EXISTS public.identities (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id UUID NOT NULL REFERENCES public.users(id) ON DELETE CASCADE,
  -- The issuer that minted the subject. 'zitadel' on this platform. It is
  -- recorded because subjects are unique WITHIN an issuer, not across them: if
  -- you ever federate a second provider, one subject must not resolve to a user
  -- another provider established.
  provider VARCHAR(50) NOT NULL,
  provider_user_id VARCHAR(255) NOT NULL,
  created_at TIMESTAMPTZ DEFAULT NOW(),
  updated_at TIMESTAMPTZ DEFAULT NOW(),
  metadata JSONB DEFAULT '{}'::jsonb,
  -- THE CONSTRAINT THE LIBRARY DEPENDS ON. Two requests from the same person can
  -- arrive together, both find no identity, and both insert. This is what decides
  -- the race: the loser takes `ON CONFLICT DO NOTHING`, deletes the user row it
  -- had just created, and adopts the winner's. Without it, one person gets two
  -- user records and the second is invisible to every later lookup.
  UNIQUE(provider, provider_user_id)
);

CREATE INDEX IF NOT EXISTS idx_identities_user_id ON public.identities(user_id);
CREATE INDEX IF NOT EXISTS idx_identities_provider ON public.identities(provider, provider_user_id);
