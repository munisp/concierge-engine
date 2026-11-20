package domain

// Phase 60: ride modes & cabin pre-conditioning.
//
// A ride mode is a one-tap ambiance preset — "Focus", "Relax", "Family",
// "Celebrate" — that expands into a bundle of Phase 58 preferences through
// the normal validated SavePreference path (provenance "mode:{mode}").
// Modes compose the vocabulary; they never bypass it. The chauffeur trip
// view gains concrete prep tasks (pre-cool to 20 °C, install the toddler
// seat) derived from the same profile — the car is ready before the door
// opens.

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

// houseModes are the default preset content. Tenants override individual
// entries via tenants.mode_presets (Phase 60) — the NAMES stay the house
// enum so preference validation never forks per tenant.
var houseModes = map[string]model.ModePreset{
	"focus": {Label: "Focus", Prefs: map[string]string{
		"ride.conversation": "quiet", "ride.temperature_c": "21",
		"ride.route": "fastest", "ride.music": "none",
	}},
	"relax": {Label: "Relax", Prefs: map[string]string{
		"ride.conversation": "light", "ride.temperature_c": "22",
		"ride.route": "scenic", "ride.music": "mellow lounge",
	}},
	"family": {Label: "Family", Prefs: map[string]string{
		"ride.conversation": "light", "ride.temperature_c": "22",
		"ride.music": "family friendly", "ride.luggage": "standard",
	}},
	"celebrate": {Label: "Celebrate", Prefs: map[string]string{
		"ride.conversation": "chatty", "ride.temperature_c": "21",
		"ride.route": "scenic", "ride.music": "celebration",
	}},
	"custom": {Label: "Custom", Prefs: map[string]string{}},
}

// EffectiveModes merges house presets with the tenant's overrides —
// same-named tenant entries replace the house content.
func (svc *ClientService) EffectiveModes(ctx context.Context, tenantID string) (map[string]model.ModePreset, error) {
	out := make(map[string]model.ModePreset, len(houseModes))
	for k, v := range houseModes {
		cp := model.ModePreset{Label: v.Label, Prefs: map[string]string{}}
		for pk, pv := range v.Prefs {
			cp.Prefs[pk] = pv
		}
		out[k] = cp
	}
	t, err := svc.s.Store.Tenants().Get(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	for name, preset := range t.ModePresets {
		if _, ok := houseModes[name]; !ok {
			continue // tenant-defined names stay out of the validated enum
		}
		out[name] = preset
	}
	return out, nil
}

// ApplyMode sets ride.mode and expands the preset bundle into the rider's
// profile — every write passes through SavePreference (validated,
// consent-scoped, audit-logged) with provenance "mode:{mode}".
func (svc *ClientService) ApplyMode(ctx context.Context, tenantID, clientID, mode, recordedBy string) (map[string]string, error) {
	modes, err := svc.EffectiveModes(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	preset, ok := modes[mode]
	if !ok {
		return nil, httpx.ErrValidation
	}
	src := "mode:" + mode
	if _, err := svc.SavePreference(ctx, tenantID, clientID, "ride.mode", mode, "service", src, recordedBy); err != nil {
		return nil, err
	}
	// Deterministic order so audits and tests see a stable expansion.
	keys := make([]string, 0, len(preset.Prefs))
	for k := range preset.Prefs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	applied := map[string]string{"ride.mode": mode}
	for _, k := range keys {
		if _, err := svc.SavePreference(ctx, tenantID, clientID, k, preset.Prefs[k], "service", src, recordedBy); err != nil {
			return nil, fmt.Errorf("mode %s: %s: %w", mode, k, err)
		}
		applied[k] = preset.Prefs[k]
	}
	return applied, nil
}

// PrepTasks derives the chauffeur's pre-pickup checklist from the comfort
// block — concrete actions, not raw key/value pairs.
func PrepTasks(prefs map[string]string) []string {
	var out []string
	if v := prefs["ride.temperature_c"]; v != "" {
		out = append(out, "Set cabin to "+v+"°C before pickup")
	}
	switch prefs["ride.child_seat"] {
	case "infant", "toddler", "booster":
		out = append(out, "Install "+strings.ReplaceAll(prefs["ride.child_seat"], "_", " ")+" child seat")
	}
	if prefs["ride.luggage"] == "heavy" {
		out = append(out, "Clear full luggage space")
	}
	switch prefs["access.mobility"] {
	case "assistance":
		out = append(out, "Assist passenger boarding")
	case "wheelchair":
		out = append(out, "Wheelchair-accessible setup")
	}
	if m := prefs["ride.music"]; m != "" {
		if isNoMusic(m) {
			out = append(out, "No music — quiet cabin")
		} else {
			task := "Music: " + m
			if v := prefs["ride.music_volume"]; v != "" {
				task += " (" + v + ")"
			}
			out = append(out, task)
		}
	}
	if prefs["ride.conversation"] == "quiet" {
		out = append(out, "Quiet ride — minimal conversation")
	}
	return out
}

// isNoMusic — explicit silence requests ("none" comes from the Focus
// preset; "off"/"silence" are rider-typed equivalents).
func isNoMusic(genre string) bool {
	switch strings.ToLower(strings.TrimSpace(genre)) {
	case "none", "off", "silence", "no music":
		return true
	}
	return false
}

// modeMood — a mode is a newer, more explicit signal than conversation;
// when set it overrides the conversation-derived mood for soundscapes.
func modeMood(mode string) (mood string, bpm int, ok bool) {
	switch mode {
	case "celebrate":
		return "upbeat", 115, true
	case "focus":
		return "ambient", 70, true
	case "relax":
		return "chill", 85, true
	case "family":
		return "chill", 90, true
	}
	return "", 0, false
}

// modeDefaultGenre — the genre a mode implies when the rider hasn't named
// one. Focus implies silence upstream (its preset sets ride.music=none),
// so it never reaches here.
func modeDefaultGenre(mode string) string {
	switch mode {
	case "relax":
		return "mellow lounge"
	case "family":
		return "soft instrumental"
	case "celebrate":
		return "celebration pop"
	case "focus":
		return "ambient instrumental"
	}
	return ""
}
