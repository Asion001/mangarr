-- +goose Up
-- Interface options that follow the account to every device (theme,
-- start page, other-language chapters, …), as one JSON document.
ALTER TABLE user_ui_preferences ADD COLUMN options TEXT NOT NULL DEFAULT '{}';

-- +goose Down
ALTER TABLE user_ui_preferences DROP COLUMN options;
