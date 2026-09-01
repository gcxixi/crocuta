CREATE TABLE IF NOT EXISTS sentryx_users (
  id TEXT PRIMARY KEY,
  email TEXT NOT NULL DEFAULT '',
  name TEXT NOT NULL DEFAULT '',
  username TEXT NOT NULL DEFAULT '',
  date_joined TIMESTAMPTZ NOT NULL DEFAULT now(),
  avatar_url TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS sentryx_organizations (
  id TEXT PRIMARY KEY,
  slug TEXT NOT NULL UNIQUE,
  name TEXT NOT NULL,
  date_created TIMESTAMPTZ NOT NULL DEFAULT now(),
  status TEXT NOT NULL DEFAULT 'active',
  allow_member_invite BOOLEAN NOT NULL DEFAULT true,
  allow_member_project_creation BOOLEAN NOT NULL DEFAULT true,
  require_2fa BOOLEAN NOT NULL DEFAULT false
);

CREATE TABLE IF NOT EXISTS sentryx_teams (
  id TEXT PRIMARY KEY,
  organization_id TEXT NOT NULL REFERENCES sentryx_organizations(id) ON DELETE CASCADE,
  slug TEXT NOT NULL,
  name TEXT NOT NULL,
  date_created TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (organization_id, slug)
);

CREATE TABLE IF NOT EXISTS sentryx_organization_members (
  id TEXT PRIMARY KEY,
  organization_id TEXT NOT NULL REFERENCES sentryx_organizations(id) ON DELETE CASCADE,
  user_id TEXT NOT NULL REFERENCES sentryx_users(id) ON DELETE CASCADE,
  role TEXT NOT NULL DEFAULT 'member',
  date_created TIMESTAMPTZ NOT NULL DEFAULT now(),
  expired BOOLEAN NOT NULL DEFAULT false,
  UNIQUE (organization_id, user_id)
);

CREATE TABLE IF NOT EXISTS sentryx_control_projects (
  id TEXT PRIMARY KEY,
  organization_id TEXT NOT NULL REFERENCES sentryx_organizations(id) ON DELETE CASCADE,
  slug TEXT NOT NULL,
  name TEXT NOT NULL,
  platform TEXT NOT NULL DEFAULT '',
  date_created TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (organization_id, slug)
);

CREATE TABLE IF NOT EXISTS sentryx_project_teams (
  project_id TEXT NOT NULL REFERENCES sentryx_control_projects(id) ON DELETE CASCADE,
  team_id TEXT NOT NULL REFERENCES sentryx_teams(id) ON DELETE CASCADE,
  PRIMARY KEY (project_id, team_id)
);

CREATE TABLE IF NOT EXISTS sentryx_project_keys (
  id TEXT PRIMARY KEY,
  project_id TEXT NOT NULL REFERENCES sentryx_control_projects(id) ON DELETE CASCADE,
  name TEXT NOT NULL DEFAULT '',
  public_key TEXT NOT NULL UNIQUE,
  secret_key TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS sentryx_api_tokens (
  token_hash TEXT PRIMARY KEY,
  user_id TEXT,
  organization_id TEXT,
  scopes JSONB NOT NULL DEFAULT '[]'::jsonb,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

INSERT INTO sentryx_organizations (id, slug, name)
VALUES ('1', 'default', 'Default')
ON CONFLICT (id) DO NOTHING;

