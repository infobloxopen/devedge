CREATE TABLE webhook_endpoints (
    id            TEXT PRIMARY KEY,
    url           TEXT NOT NULL,
    secret        TEXT NOT NULL DEFAULT '',
    event_filters TEXT[] NOT NULL DEFAULT '{}'
);
