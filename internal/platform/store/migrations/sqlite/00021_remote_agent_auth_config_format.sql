-- +goose Up
-- +goose StatementBegin

UPDATE a2a_agents
SET agent_card_auth_config = CASE agent_card_auth_type
    WHEN 'bearer' THEN json_object(
        'type', 'bearer',
        'token', json_extract(agent_card_auth_config, '$.bearerToken')
    )
    WHEN 'apikey' THEN json_object(
        'type', 'apikey',
        'api_key', json_extract(agent_card_auth_config, '$.apiKey'),
        'header_name', COALESCE(json_extract(agent_card_auth_config, '$.headerName'), 'X-API-Key')
    )
    WHEN 'oauth2' THEN json_object(
        'type', 'oauth2',
        'client_id', json_extract(agent_card_auth_config, '$.clientId'),
        'client_secret', json_extract(agent_card_auth_config, '$.clientSecret'),
        'token_endpoint', COALESCE(
            json_extract(agent_card_auth_config, '$.tokenEndpoint'),
            json_extract(agent_card_auth_config, '$.tokenUrl')
        ),
        'scopes', COALESCE(json_extract(agent_card_auth_config, '$.scopes'), json('[]'))
    )
    WHEN 'basic' THEN json_object(
        'type', 'basic',
        'username', json_extract(agent_card_auth_config, '$.username'),
        'password', json_extract(agent_card_auth_config, '$.password')
    )
    ELSE agent_card_auth_config
END
WHERE agent_card_auth_config IS NOT NULL
  AND json_valid(agent_card_auth_config) = 1
  AND agent_card_auth_type IN ('bearer', 'apikey', 'oauth2', 'basic')
  AND json_extract(agent_card_auth_config, '$.type') IS NULL;

UPDATE a2a_agents
SET agent_auth_config = CASE agent_auth_type
    WHEN 'bearer' THEN json_object(
        'type', 'bearer',
        'token', json_extract(agent_auth_config, '$.bearerToken')
    )
    WHEN 'apikey' THEN json_object(
        'type', 'apikey',
        'api_key', json_extract(agent_auth_config, '$.apiKey'),
        'header_name', COALESCE(json_extract(agent_auth_config, '$.headerName'), 'X-API-Key')
    )
    WHEN 'oauth2' THEN json_object(
        'type', 'oauth2',
        'client_id', json_extract(agent_auth_config, '$.clientId'),
        'client_secret', json_extract(agent_auth_config, '$.clientSecret'),
        'token_endpoint', COALESCE(
            json_extract(agent_auth_config, '$.tokenEndpoint'),
            json_extract(agent_auth_config, '$.tokenUrl')
        ),
        'scopes', COALESCE(json_extract(agent_auth_config, '$.scopes'), json('[]'))
    )
    WHEN 'basic' THEN json_object(
        'type', 'basic',
        'username', json_extract(agent_auth_config, '$.username'),
        'password', json_extract(agent_auth_config, '$.password')
    )
    ELSE agent_auth_config
END
WHERE agent_auth_config IS NOT NULL
  AND json_valid(agent_auth_config) = 1
  AND agent_auth_type IN ('bearer', 'apikey', 'oauth2', 'basic')
  AND json_extract(agent_auth_config, '$.type') IS NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
UPDATE a2a_agents
SET agent_card_auth_config = CASE json_extract(agent_card_auth_config, '$.type')
    WHEN 'bearer' THEN json_object(
        'bearerToken', json_extract(agent_card_auth_config, '$.token')
    )
    WHEN 'apikey' THEN json_object(
        'apiKey', json_extract(agent_card_auth_config, '$.api_key'),
        'headerName', json_extract(agent_card_auth_config, '$.header_name')
    )
    WHEN 'oauth2' THEN json_object(
        'clientId', json_extract(agent_card_auth_config, '$.client_id'),
        'clientSecret', json_extract(agent_card_auth_config, '$.client_secret'),
        'tokenEndpoint', json_extract(agent_card_auth_config, '$.token_endpoint'),
        'scopes', json_extract(agent_card_auth_config, '$.scopes')
    )
    WHEN 'basic' THEN json_object(
        'username', json_extract(agent_card_auth_config, '$.username'),
        'password', json_extract(agent_card_auth_config, '$.password')
    )
    ELSE agent_card_auth_config
END
WHERE agent_card_auth_config IS NOT NULL
  AND json_valid(agent_card_auth_config) = 1
  AND json_extract(agent_card_auth_config, '$.type') IS NOT NULL;

UPDATE a2a_agents
SET agent_auth_config = CASE json_extract(agent_auth_config, '$.type')
    WHEN 'bearer' THEN json_object(
        'bearerToken', json_extract(agent_auth_config, '$.token')
    )
    WHEN 'apikey' THEN json_object(
        'apiKey', json_extract(agent_auth_config, '$.api_key'),
        'headerName', json_extract(agent_auth_config, '$.header_name')
    )
    WHEN 'oauth2' THEN json_object(
        'clientId', json_extract(agent_auth_config, '$.client_id'),
        'clientSecret', json_extract(agent_auth_config, '$.client_secret'),
        'tokenEndpoint', json_extract(agent_auth_config, '$.token_endpoint'),
        'scopes', json_extract(agent_auth_config, '$.scopes')
    )
    WHEN 'basic' THEN json_object(
        'username', json_extract(agent_auth_config, '$.username'),
        'password', json_extract(agent_auth_config, '$.password')
    )
    ELSE agent_auth_config
END
WHERE agent_auth_config IS NOT NULL
  AND json_valid(agent_auth_config) = 1
  AND json_extract(agent_auth_config, '$.type') IS NOT NULL;
-- +goose StatementEnd
