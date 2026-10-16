-- +goose Up
CREATE TABLE user_ui_preferences (
    user_id BIGINT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    locale TEXT NOT NULL DEFAULT 'auto' CHECK (locale IN ('auto','en','ru','uk')),
    mode TEXT NOT NULL DEFAULT 'reading' CHECK (mode IN ('reading','editing')),
    updated_at TIMESTAMPTZ NOT NULL
);

-- +goose Down
DROP TABLE user_ui_preferences;
