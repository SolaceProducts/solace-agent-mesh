-- +goose Up
-- +goose StatementBegin

UPDATE a2a_agents
SET agent_card_auth_config = (CASE agent_card_auth_type
    WHEN 'bearer' THEN jsonb_build_object(
        'type', 'bearer',
        'token', agent_card_auth_config::jsonb->'bearerToken'
    )::text
    WHEN 'apikey' THEN jsonb_build_object(
        'type', 'apikey',
        'api_key', agent_card_auth_config::jsonb->'apiKey',
        'header_name', COALESCE(agent_card_auth_config::jsonb->'headerName', '"X-API-Key"'::jsonb)
    )::text
    WHEN 'oauth2' THEN jsonb_build_object(
        'type', 'oauth2',
        'client_id', agent_card_auth_config::jsonb->'clientId',
        'client_secret', agent_card_auth_config::jsonb->'clientSecret',
        'token_endpoint', COALESCE(
            agent_card_auth_config::jsonb->'tokenEndpoint',
            agent_card_auth_config::jsonb->'tokenUrl'
        ),
        'scopes', COALESCE(agent_card_auth_config::jsonb->'scopes', '[]'::jsonb)
    )::text
    WHEN 'basic' THEN jsonb_build_object(
        'type', 'basic',
        'username', agent_card_auth_config::jsonb->'username',
        'password', agent_card_auth_config::jsonb->'password'
    )::text
    ELSE agent_card_auth_config
END)
WHERE agent_card_auth_config IS NOT NULL
  AND agent_card_auth_type IN ('bearer', 'apikey', 'oauth2', 'basic')
  AND (agent_card_auth_config::jsonb->>'type') IS NULL;

UPDATE a2a_agents
SET agent_auth_config = (CASE agent_auth_type
    WHEN 'bearer' THEN jsonb_build_object(
        'type', 'bearer',
        'token', agent_auth_config::jsonb->'bearerToken'
    )::text
    WHEN 'apikey' THEN jsonb_build_object(
        'type', 'apikey',
        'api_key', agent_auth_config::jsonb->'apiKey',
        'header_name', COALESCE(agent_auth_config::jsonb->'headerName', '"X-API-Key"'::jsonb)
    )::text
    WHEN 'oauth2' THEN jsonb_build_object(
        'type', 'oauth2',
        'client_id', agent_auth_config::jsonb->'clientId',
        'client_secret', agent_auth_config::jsonb->'clientSecret',
        'token_endpoint', COALESCE(
            agent_auth_config::jsonb->'tokenEndpoint',
            agent_auth_config::jsonb->'tokenUrl'
        ),
        'scopes', COALESCE(agent_auth_config::jsonb->'scopes', '[]'::jsonb)
    )::text
    WHEN 'basic' THEN jsonb_build_object(
        'type', 'basic',
        'username', agent_auth_config::jsonb->'username',
        'password', agent_auth_config::jsonb->'password'
    )::text
    ELSE agent_auth_config
END)
WHERE agent_auth_config IS NOT NULL
  AND agent_auth_type IN ('bearer', 'apikey', 'oauth2', 'basic')
  AND (agent_auth_config::jsonb->>'type') IS NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
UPDATE a2a_agents
SET agent_card_auth_config = (CASE agent_card_auth_config::jsonb->>'type'
    WHEN 'bearer' THEN jsonb_build_object(
        'bearerToken', agent_card_auth_config::jsonb->'token'
    )::text
    WHEN 'apikey' THEN jsonb_build_object(
        'apiKey', agent_card_auth_config::jsonb->'api_key',
        'headerName', agent_card_auth_config::jsonb->'header_name'
    )::text
    WHEN 'oauth2' THEN jsonb_build_object(
        'clientId', agent_card_auth_config::jsonb->'client_id',
        'clientSecret', agent_card_auth_config::jsonb->'client_secret',
        'tokenEndpoint', agent_card_auth_config::jsonb->'token_endpoint',
        'scopes', agent_card_auth_config::jsonb->'scopes'
    )::text
    WHEN 'basic' THEN jsonb_build_object(
        'username', agent_card_auth_config::jsonb->'username',
        'password', agent_card_auth_config::jsonb->'password'
    )::text
    ELSE agent_card_auth_config
END)
WHERE agent_card_auth_config IS NOT NULL
  AND (agent_card_auth_config::jsonb->>'type') IS NOT NULL;

UPDATE a2a_agents
SET agent_auth_config = (CASE agent_auth_config::jsonb->>'type'
    WHEN 'bearer' THEN jsonb_build_object(
        'bearerToken', agent_auth_config::jsonb->'token'
    )::text
    WHEN 'apikey' THEN jsonb_build_object(
        'apiKey', agent_auth_config::jsonb->'api_key',
        'headerName', agent_auth_config::jsonb->'header_name'
    )::text
    WHEN 'oauth2' THEN jsonb_build_object(
        'clientId', agent_auth_config::jsonb->'client_id',
        'clientSecret', agent_auth_config::jsonb->'client_secret',
        'tokenEndpoint', agent_auth_config::jsonb->'token_endpoint',
        'scopes', agent_auth_config::jsonb->'scopes'
    )::text
    WHEN 'basic' THEN jsonb_build_object(
        'username', agent_auth_config::jsonb->'username',
        'password', agent_auth_config::jsonb->'password'
    )::text
    ELSE agent_auth_config
END)
WHERE agent_auth_config IS NOT NULL
  AND (agent_auth_config::jsonb->>'type') IS NOT NULL;
-- +goose StatementEnd
