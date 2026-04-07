package db

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"time"
)

type ProfileStore struct {
	path string
}

func NewProfileStore() (*ProfileStore, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return nil, err
	}

	dir := filepath.Join(configDir, "sqlgo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}

	return &ProfileStore{path: filepath.Join(dir, "profiles.json")}, nil
}

func (s *ProfileStore) Load() ([]ConnectionProfile, error) {
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var profiles []ConnectionProfile
	if err := json.Unmarshal(data, &profiles); err != nil {
		return nil, err
	}

	sort.Slice(profiles, func(i, j int) bool {
		return profiles[i].Name < profiles[j].Name
	})

	return profiles, nil
}

func (s *ProfileStore) Save(profile ConnectionProfile) error {
	return s.SaveAs(profile, "")
}

func (s *ProfileStore) SaveAs(profile ConnectionProfile, previousName string) error {
	if err := profile.Validate(); err != nil {
		return err
	}

	profiles, err := s.Load()
	if err != nil {
		return err
	}

	now := time.Now().UTC()
	if profile.CreatedAt.IsZero() {
		profile.CreatedAt = now
	}
	profile.UpdatedAt = now

	replaced := false
	for i := range profiles {
		if profiles[i].Name == profile.Name || (previousName != "" && profiles[i].Name == previousName) {
			if profile.CreatedAt.IsZero() {
				profile.CreatedAt = profiles[i].CreatedAt
			}
			profiles[i] = profile
			replaced = true
			break
		}
	}
	if !replaced {
		profiles = append(profiles, profile)
	}

	sort.Slice(profiles, func(i, j int) bool {
		return profiles[i].Name < profiles[j].Name
	})

	data, err := json.MarshalIndent(profiles, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(s.path, data, 0o600)
}

func (s *ProfileStore) Delete(name string) error {
	profiles, err := s.Load()
	if err != nil {
		return err
	}

	filtered := profiles[:0]
	for _, profile := range profiles {
		if profile.Name != name {
			filtered = append(filtered, profile)
		}
	}

	data, err := json.MarshalIndent(filtered, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(s.path, data, 0o600)
}

func (s *ProfileStore) Path() string {
	return s.path
}
