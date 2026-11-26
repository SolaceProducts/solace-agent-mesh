-- +goose Up
UPDATE connectors
SET "values" = '{"auth_oauth_pkce":"disabled",' || substr("values", 2)
WHERE type = 'api'
  AND subtype = 'openapi'
  AND instr("values", '{') = 1
  AND (instr("values", '"auth_type":"oauth"') > 0 OR instr("values", '"auth_type": "oauth"') > 0)
  AND instr("values", '"auth_oauth_pkce"') = 0
  AND instr("values", '"auth_oauth_token_endpoint_auth_method":"none"') = 0
  AND instr("values", '"auth_oauth_token_endpoint_auth_method": "none"') = 0;

UPDATE connectors
SET updated_time = CAST(strftime('%s', 'now') AS INTEGER) * 1000
WHERE type = 'api'
  AND subtype = 'openapi'
  AND (instr("values", '"auth_type":"oauth"') > 0 OR instr("values", '"auth_type": "oauth"') > 0);

-- +goose Down
UPDATE connectors
SET "values" = '{' || substr("values", length('{"auth_oauth_pkce":"disabled",') + 1)
WHERE type = 'api'
  AND subtype = 'openapi'
  AND instr("values", '{') = 1
  AND instr("values", '{"auth_oauth_pkce":"disabled",') = 1;
