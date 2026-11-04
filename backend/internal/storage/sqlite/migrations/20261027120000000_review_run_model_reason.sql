-- model_reason records why AO chose the run's reviewer model when it picked one
-- itself (a risky-path match); empty when the model was explicit or the default.

-- +goose Up
ALTER TABLE review_run ADD COLUMN model_reason TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE review_run DROP COLUMN model_reason;
