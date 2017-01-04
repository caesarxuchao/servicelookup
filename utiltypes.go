package main

import (
	"sync"

	"k8s.io/client-go/pkg/api/v1"
)

type addressToPod struct {
	core map[string]*v1.Pod
	lock sync.RWMutex
}

func newAddressToPod() *addressToPod {
	var a addressToPod
	a.core = make(map[string]*v1.Pod)
	return &a
}

func (a *addressToPod) Read(address string) (*v1.Pod, bool) {
	a.lock.RLock()
	defer a.lock.RUnlock()
	pod, ok := a.core[address]
	return pod, ok
}

func (a *addressToPod) Write(address string, pod *v1.Pod) {
	a.lock.Lock()
	defer a.lock.Unlock()
	a.core[address] = pod
}
