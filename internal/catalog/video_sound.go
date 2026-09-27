package catalog

import (
	"context"
	"database/sql"
	"errors"
)

// Clips start muted unless the reviewer has chosen otherwise in Settings. The
// choice is kept in the catalogue rather than the browser so it holds on every
// device; a tab that turns the sound on or off still overrides it until it is
// closed.
const videoSoundSetting = "video_sound"

// VideoMuted reports whether a clip should start without sound.
func (s *Store) VideoMuted(ctx context.Context) (bool, error) {
	var value string
	err := s.read.QueryRowContext(ctx, "SELECT value FROM settings WHERE key=?", videoSoundSetting).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return true, nil
	}
	if err != nil {
		return true, err
	}
	// Only SetVideoMuted writes this; anything but "on" means the default.
	return value != "on", nil
}

func (s *Store) SetVideoMuted(ctx context.Context, muted bool) error {
	value := "on"
	if muted {
		value = "muted"
	}
	_, err := s.write.ExecContext(ctx, "INSERT INTO settings(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", videoSoundSetting, value)
	return err
}
