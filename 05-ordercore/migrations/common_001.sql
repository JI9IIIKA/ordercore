CREATE TABLE IF NOT EXISTS idempotency(project text NOT NULL,tenant text NOT NULL,key text NOT NULL,fingerprint text NOT NULL,response jsonb NOT NULL,created_at timestamptz NOT NULL DEFAULT now(),PRIMARY KEY(project,tenant,key));
CREATE TABLE IF NOT EXISTS outbox(id text PRIMARY KEY,project text NOT NULL,tenant text NOT NULL,kind text NOT NULL,payload jsonb NOT NULL,created_at timestamptz NOT NULL DEFAULT now(),delivered_at timestamptz);
CREATE INDEX IF NOT EXISTS outbox_pending ON outbox(project,created_at) WHERE delivered_at IS NULL;
CREATE TABLE IF NOT EXISTS event_receipts(id text PRIMARY KEY,project text NOT NULL,tenant text NOT NULL,kind text NOT NULL,payload jsonb NOT NULL,received_at timestamptz NOT NULL DEFAULT now());
