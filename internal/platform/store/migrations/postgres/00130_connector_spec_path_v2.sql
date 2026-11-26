
-- +goose Up
UPDATE connectors
SET "values" = replace("values",
    '"specification_url":"/api/v1/platform/connectors/' || id::text || '/resource"',
    '"specification_url":"/api/v2/platform/connectors/' || id::text || '/resource"')
WHERE strpos("values", '"specification_url":"/api/v1/platform/connectors/' || id::text || '/resource"') > 0;

-- +goose Down
UPDATE connectors
SET "values" = replace("values",
    '"specification_url":"/api/v2/platform/connectors/' || id::text || '/resource"',
    '"specification_url":"/api/v1/platform/connectors/' || id::text || '/resource"')
WHERE strpos("values", '"specification_url":"/api/v2/platform/connectors/' || id::text || '/resource"') > 0;
