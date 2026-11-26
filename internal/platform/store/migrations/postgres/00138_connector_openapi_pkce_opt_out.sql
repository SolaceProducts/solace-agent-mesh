-- +goose Up
UPDATE connectors
SET "values" = '{"auth_oauth_pkce":"disabled",' || substr("values", 2)
WHERE type = 'api'
  AND subtype = 'openapi'
  AND strpos("values", '{') = 1
  AND (strpos("values", '"auth_type":"oauth"') > 0 OR strpos("values", '"auth_type": "oauth"') > 0)
  AND strpos("values", '"auth_oauth_pkce"') = 0
  AND strpos("values", '"auth_oauth_token_endpoint_auth_method":"none"') = 0
  AND strpos("values", '"auth_oauth_token_endpoint_auth_method": "none"') = 0;

UPDATE connectors
SET updated_time = (EXTRACT(EPOCH FROM now()) * 1000)::bigint
WHERE type = 'api'
  AND subtype = 'openapi'
  AND (strpos("values", '"auth_type":"oauth"') > 0 OR strpos("values", '"auth_type": "oauth"') > 0);

-- +goose Down
UPDATE connectors
SET "values" = '{' || substr("values", length('{"auth_oauth_pkce":"disabled",') + 1)
WHERE type = 'api'
  AND subtype = 'openapi'
  AND strpos("values", '{"auth_oauth_pkce":"disabled",') = 1;
