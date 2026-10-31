package server

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/matella/twitch-bot/internal/model"
)

// OverlayStore enregistre les réglages d'affichage de l'incrustation du chat.
type OverlayStore interface {
	GetOverlaySettings(ctx context.Context) (model.OverlaySettings, error)
	SetOverlaySettings(ctx context.Context, o model.OverlaySettings) error
}

// overlayStatus est la réponse de l'administration : réglages et informations pour l'URL de la source OBS.
type overlayStatus struct {
	Settings      model.OverlaySettings `json:"settings"`
	TokenRequired bool                  `json:"token_required"`
	Path          string                `json:"path"`
}

func (s *server) overlayStatusOf(ctx context.Context) (overlayStatus, error) {
	ms, err := s.overlay.GetOverlaySettings(ctx)
	if err != nil {
		return overlayStatus{}, err
	}
	return overlayStatus{Settings: ms, TokenRequired: s.opts.OverlayTokenRequired, Path: "/overlay/chat"}, nil
}

func (s *server) getOverlay(w http.ResponseWriter, r *http.Request) {
	if s.overlay == nil {
		writeErr(w, http.StatusNotFound, "incrustation non disponible")
		return
	}
	st, err := s.overlayStatusOf(r.Context())
	if err != nil {
		serverErr(w, "lecture des réglages de l'incrustation", err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *server) putOverlay(w http.ResponseWriter, r *http.Request) {
	if s.overlay == nil {
		writeErr(w, http.StatusNotFound, "incrustation non disponible")
		return
	}
	var ms model.OverlaySettings
	if !decode(w, r, &ms) {
		return
	}
	if err := ms.Normalize(); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.overlay.SetOverlaySettings(r.Context(), ms); err != nil {
		serverErr(w, "enregistrement des réglages de l'incrustation", err)
		return
	}
	slog.Info("réglages de l'incrustation modifiés")
	st, err := s.overlayStatusOf(r.Context())
	if err != nil {
		serverErr(w, "lecture des réglages de l'incrustation", err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}
