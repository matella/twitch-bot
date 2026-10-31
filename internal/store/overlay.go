package store

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/matella/twitch-bot/internal/model"
)

const overlaySettingsKey = "overlay_settings"

// GetOverlaySettings renvoie les réglages de l'incrustation, ou les valeurs par défaut.
func (s *Store) GetOverlaySettings(ctx context.Context) (model.OverlaySettings, error) {
	raw, err := s.GetSetting(ctx, overlaySettingsKey)
	if errors.Is(err, model.ErrNotFound) {
		return model.DefaultOverlaySettings(), nil
	}
	if err != nil {
		return model.OverlaySettings{}, err
	}
	// Partir des valeurs par défaut : un champ absent (ajouté après l'enregistrement) garde sa valeur par défaut.
	o := model.DefaultOverlaySettings()
	if err := json.Unmarshal([]byte(raw), &o); err != nil {
		return model.DefaultOverlaySettings(), nil
	}
	return o, nil
}

// SetOverlaySettings enregistre les réglages de l'incrustation.
func (s *Store) SetOverlaySettings(ctx context.Context, o model.OverlaySettings) error {
	b, err := json.Marshal(o)
	if err != nil {
		return err
	}
	return s.SetSetting(ctx, overlaySettingsKey, string(b))
}
