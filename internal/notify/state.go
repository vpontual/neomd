package notify

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// State persists per-(account, folder) "highest UID seen" baselines so a
// neomd restart doesn't replay every Inbox notification. Concurrent-safe.
type State struct {
	path string
	mu   sync.Mutex
	UIDs map[string]uint32 `json:"uids"`
}

// LoadState reads path. A missing or corrupt file yields an empty State; the
// caller will treat the first observation per folder as the new baseline.
func LoadState(path string) *State {
	s := &State{path: path, UIDs: map[string]uint32{}}
	data, err := os.ReadFile(path)
	if err != nil {
		return s
	}
	_ = json.Unmarshal(data, s)
	if s.UIDs == nil {
		s.UIDs = map[string]uint32{}
	}
	return s
}

// Get returns the recorded UID and whether one existed.
func (s *State) Get(key string) (uint32, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	uid, ok := s.UIDs[key]
	return uid, ok
}

// Set records uid for key. Caller is responsible for calling Save.
func (s *State) Set(key string, uid uint32) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.UIDs[key] = uid
}

// Save writes the state to disk atomically (temp file + rename).
func (s *State) Save() error {
	s.mu.Lock()
	data, err := json.Marshal(s)
	s.mu.Unlock()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0700); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}
