-- +goose Up
-- Two opt-in palettes next to system, light and dark: oled (the old dark
-- palette) and soft (the old light palette).
ALTER TABLE user_preferences DROP CONSTRAINT user_preferences_theme_check;
ALTER TABLE user_preferences ADD CONSTRAINT user_preferences_theme_check
    CHECK (theme IN ('system', 'light', 'dark', 'oled', 'soft'));

-- +goose Down
UPDATE user_preferences SET theme = 'system' WHERE theme IN ('oled', 'soft');
ALTER TABLE user_preferences DROP CONSTRAINT user_preferences_theme_check;
ALTER TABLE user_preferences ADD CONSTRAINT user_preferences_theme_check
    CHECK (theme IN ('system', 'light', 'dark'));
