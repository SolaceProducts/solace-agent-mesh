-- +goose Up
ALTER TABLE gateways ADD COLUMN dns_id TEXT;
CREATE UNIQUE INDEX IF NOT EXISTS ix_gateways_dns_id ON gateways(dns_id);

-- +goose Down
DROP INDEX IF EXISTS ix_gateways_dns_id;
ALTER TABLE gateways DROP COLUMN dns_id;
