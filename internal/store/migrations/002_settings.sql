-- Small key/value table for things that belong to the installation rather
-- than to any one raffle: the shared password hash, the session signing key.
--
-- These live in the database rather than in a config file so that the session
-- key survives a restart (nobody gets logged out by a redeploy) and so the
-- password is never sitting in plaintext in a plist or an environment
-- variable that shows up in `ps`.
CREATE TABLE settings (
    key        TEXT PRIMARY KEY,
    value      TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
