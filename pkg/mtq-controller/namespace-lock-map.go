package mtq_controller

import "sync"

type namespaceLockMap struct {
	m     map[string]*sync.Mutex
	mutex *sync.Mutex
}

func (nlm *namespaceLockMap) getLock(key string) *sync.Mutex {
	nlm.mutex.Lock()
	defer nlm.mutex.Unlock()

	if nlm.m[key] == nil {
		nlm.m[key] = &sync.Mutex{}
	}
	return nlm.m[key]
}

func (nlm *namespaceLockMap) Lock(key string) {
	nlm.getLock(key).Lock()
}

func (nlm *namespaceLockMap) Unlock(key string) {
	nlm.getLock(key).Unlock()
}
