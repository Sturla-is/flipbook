package models

import "testing"

func TestDefaultEmbedSettingsPresentation(t *testing.T) {
	got := DefaultEmbedSettings()
	if !got.Enabled || got.StartPage != 1 {
		t.Fatalf("default embed must stay enabled from page 1: %+v", got)
	}
	if got.ShowToolbar || got.ShowSearch {
		t.Fatalf("default embed must start without toolbar and search: %+v", got)
	}
	if got.Background != "#FFFFFF" {
		t.Fatalf("default embed background = %q, want #FFFFFF", got.Background)
	}
	if got.FrameWidth() != 1200 || got.FrameHeight() != 800 {
		t.Fatalf("default embed size = %dx%d, want 1200x800", got.FrameWidth(), got.FrameHeight())
	}
}

func TestEmbedFrameSizeUsesSavedValues(t *testing.T) {
	saved := EmbedSettings{Width: 960, Height: 540}
	if saved.FrameWidth() != 960 || saved.FrameHeight() != 540 {
		t.Fatalf("saved size = %dx%d, want 960x540", saved.FrameWidth(), saved.FrameHeight())
	}
	for _, test := range []struct {
		settings EmbedSettings
		valid    bool
	}{
		{EmbedSettings{}, true},
		{EmbedSettings{Width: 200, Height: 4000}, true},
		{EmbedSettings{Width: 1200}, true},
		{EmbedSettings{Width: 199, Height: 800}, false},
		{EmbedSettings{Width: 1200, Height: 4001}, false},
		{EmbedSettings{Width: -1}, false},
	} {
		if got := test.settings.ValidFrameSize(); got != test.valid {
			t.Errorf("ValidFrameSize(%dx%d) = %v, want %v", test.settings.Width, test.settings.Height, got, test.valid)
		}
	}
}

func TestNewFlipbookShowsPlatformAttribution(t *testing.T) {
	for name, defaults := range map[string]FlipbookDefaults{
		"workspace": (&Workspace{Settings: WorkspaceSettings{DefaultEmbed: DefaultEmbedSettings()}}).NewFlipbookDefaults(),
		"fallback":  FallbackFlipbookDefaults(),
	} {
		fb := &Flipbook{ID: "fb"}
		defaults.Apply(fb)
		if fb.Branding.HideBranding {
			t.Errorf("%s: a new publication must not hide the Penninn Midlun attribution", name)
		}
	}
}
