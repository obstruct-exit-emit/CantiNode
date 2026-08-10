package library

import (
	"errors"
	"testing"
)

func TestSeededDefaultProfile(t *testing.T) {
	s := newTestStore(t)

	def, err := s.DefaultProfile("music")
	if err != nil {
		t.Fatalf("DefaultProfile: %v", err)
	}
	if def.Name != "Standard Music" || !def.IsDefault {
		t.Errorf("seeded default = %+v", def)
	}
	if len(def.Formats) == 0 || def.Formats[0] != "flac" {
		t.Errorf("seeded formats = %v", def.Formats)
	}
}

func TestProfileCRUDAndDefaultSwap(t *testing.T) {
	s := newTestStore(t)

	p := &QualityProfile{
		Name:    "FLAC Only",
		Formats: []string{" FLAC ", "flac"}, // normalized + deduped
	}
	if err := s.AddProfile(p); err != nil {
		t.Fatalf("AddProfile: %v", err)
	}
	if p.ID == 0 || len(p.Formats) != 1 || p.Formats[0] != "flac" {
		t.Fatalf("added profile = %+v", p)
	}
	if p.MediaType != "music" {
		t.Errorf("media type default = %q", p.MediaType)
	}

	// Validation failures.
	for _, bad := range []*QualityProfile{
		{Name: "", Formats: []string{"flac"}},
		{Name: "x", Formats: nil},
		{Name: "x", Formats: []string{"docx"}},
		{Name: "x", Formats: []string{"flac"}, RetailBonus: 500},
		{Name: "x", Formats: []string{"flac"}, MinSize: 100, MaxSize: 50},
		{Name: "x", MediaType: "vinyl", Formats: []string{"flac"}},
	} {
		if err := s.AddProfile(bad); err == nil {
			t.Errorf("profile %+v should fail validation", bad)
		}
	}

	// Update.
	p.Language = "German"
	p.RetailBonus = 50
	if err := s.UpdateProfile(p); err != nil {
		t.Fatalf("UpdateProfile: %v", err)
	}
	got, _ := s.GetProfile(p.ID)
	if got.Language != "german" || got.RetailBonus != 50 {
		t.Errorf("updated = %+v", got)
	}

	// Default swap: seeded default loses the flag.
	if err := s.SetDefaultProfile(p.ID); err != nil {
		t.Fatalf("SetDefaultProfile: %v", err)
	}
	def, _ := s.DefaultProfile("music")
	if def.ID != p.ID {
		t.Errorf("default = %+v, want %d", def, p.ID)
	}
	profiles, _ := s.ListProfiles()
	defaults := 0
	for _, prof := range profiles {
		if prof.IsDefault {
			defaults++
		}
	}
	if defaults != 1 {
		t.Errorf("%d defaults, want exactly 1", defaults)
	}

	// The default cannot be deleted; the ex-default can.
	if err := s.DeleteProfile(p.ID); err == nil {
		t.Error("deleting the default should fail")
	}
	var exDefault int64
	for _, prof := range profiles {
		if prof.ID != p.ID {
			exDefault = prof.ID
		}
	}
	if err := s.DeleteProfile(exDefault); err != nil {
		t.Errorf("deleting non-default: %v", err)
	}
	if _, err := s.GetProfile(exDefault); !errors.Is(err, ErrNotFound) {
		t.Errorf("deleted profile still present")
	}
}
