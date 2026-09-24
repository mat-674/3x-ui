package naive

import (
	"fmt"
	"sync"

	"github.com/mhsanaei/3x-ui/v3/internal/logger"
)

type managed struct {
	proc        *Process
	fingerprint string
}

type Manager struct {
	mu    sync.Mutex
	procs map[int]*managed
}

var manager = &Manager{procs: make(map[int]*managed)}

func GetManager() *Manager {
	return manager
}

func init() {
	killOrphanedProcesses()
}

func (m *Manager) Ensure(inst Instance) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.ensureLocked(inst)
}

func (m *Manager) ensureLocked(inst Instance) error {
	if inst.Id <= 0 {
		return fmt.Errorf("invalid naive inbound id %d", inst.Id)
	}
	fingerprint := inst.Fingerprint()
	if current, ok := m.procs[inst.Id]; ok && current != nil && current.proc != nil && current.proc.IsRunning() && current.fingerprint == fingerprint {
		return nil
	}
	m.removeLocked(inst.Id)
	if inst.CertificatePEM != "" || inst.KeyPEM != "" {
		certPath, keyPath, err := WriteTLSFiles(inst.Id, inst.CertificatePEM, inst.KeyPEM)
		if err != nil {
			return err
		}
		if certPath != "" {
			inst.CertificateFile = certPath
		}
		if keyPath != "" {
			inst.KeyFile = keyPath
		}
	} else {
		_ = RemoveTLSFiles(inst.Id)
	}
	data, err := GenerateConfig(inst)
	if err != nil {
		return err
	}
	configPath, err := WriteConfigFile(inst.Id, data)
	if err != nil {
		return err
	}
	proc := newProcess(inst.Tag)
	if err := proc.Start(configPath); err != nil {
		_ = RemoveConfigFile(inst.Id)
		return err
	}
	m.procs[inst.Id] = &managed{proc: proc, fingerprint: fingerprint}
	logger.Infof("naive: started inbound %s", inst.Tag)
	return nil
}

func (m *Manager) Remove(id int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.removeLocked(id)
}

func (m *Manager) removeLocked(id int) {
	if current, ok := m.procs[id]; ok && current != nil && current.proc != nil {
		if err := current.proc.Stop(); err != nil && current.proc.IsRunning() {
			logger.Warningf("naive: unable to stop inbound %d: %v", id, err)
		}
	}
	_ = RemoveConfigFile(id)
	delete(m.procs, id)
}

func (m *Manager) Reconcile(desired []Instance) {
	m.mu.Lock()
	defer m.mu.Unlock()

	desiredMap := make(map[int]Instance, len(desired))
	for _, inst := range desired {
		desiredMap[inst.Id] = inst
	}
	for id := range m.procs {
		if _, ok := desiredMap[id]; !ok {
			m.removeLocked(id)
		}
	}
	for _, inst := range desired {
		if err := m.ensureLocked(inst); err != nil {
			logger.Errorf("naive: unable to reconcile inbound %d: %v", inst.Id, err)
		}
	}
}

func (m *Manager) StopAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id := range m.procs {
		m.removeLocked(id)
	}
	m.procs = make(map[int]*managed)
}
