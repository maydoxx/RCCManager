package games

import (
	"encoding/json"
	"log"
	"os"
	"time"

	"rccmanager/internal/atomicfile"
)

// renders arent saved, the site just asks again

type stateFile struct {
	SavedAt time.Time `json:"savedAt"`
	Servers []Server  `json:"servers"`
}

func (m *Manager) save() {
	m.saveMu.Lock()
	defer m.saveMu.Unlock()

	m.mu.Lock()
	st := stateFile{
		SavedAt: time.Now(),
		Servers: make([]Server, 0, len(m.servers)),
	}
	for _, s := range m.servers {
		st.Servers = append(st.Servers, *s)
	}
	m.mu.Unlock()

	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		log.Printf("cant save state: %v", err)
		return
	}
	if err := atomicfile.Write(m.cfg.StateFile, data); err != nil {
		log.Printf("cant save %s: %v", m.cfg.StateFile, err)
	}
}

func (m *Manager) Save() {
	m.save()
}

func (m *Manager) load() error {
	m.saveMu.Lock()
	defer m.saveMu.Unlock()

	data, err := os.ReadFile(m.cfg.StateFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var st stateFile
	if err := json.Unmarshal(data, &st); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range st.Servers {
		s := st.Servers[i]
		// address may have changed in the config since
		s.Address = m.cfg.SitePublicAddress
		m.servers[s.JobID] = &s
	}
	log.Printf("loaded %d servers from %s", len(st.Servers), m.cfg.StateFile)
	return nil
}
