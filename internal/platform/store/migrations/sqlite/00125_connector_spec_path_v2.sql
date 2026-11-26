
-- +goose Up
UPDATE connectors
SET "values" = replace("values",
    '"specification_url":"/api/v1/platform/connectors/' || id || '/resource"',
    '"specification_url":"/api/v2/platform/connectors/' || id || '/resource"')
WHERE instr("values", '"specification_url":"/api/v1/platform/connectors/' || id || '/resource"') > 0;

-- +goose Down
UPDATE connectors
SET "values" = replace("values",
    '"specification_url":"/api/v2/platform/connectors/' || id || '/resource"',
    '"specification_url":"/api/v1/platform/connectors/' || id || '/resource"')
WHERE instr("values", '"specification_url":"/api/v2/platform/connectors/' || id || '/resource"') > 0;
