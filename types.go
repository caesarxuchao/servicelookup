package main

import (
	"sync"

	"k8s.io/client-go/pkg/api/v1"
)

// Only required to authenticate against GKE clusters
//_ "k8s.io/client-go/plugin/pkg/client/auth/gcp"

type PodToService struct {
	PodName        string `json:"podName"`
	PodUID         string `json:"podUID"`
	ServiceName    string `json:"serviceName,omitempty"`
	ServiceAddress string `json:"serviceAddress,omitempty"`
}

type addressToPod struct {
	core map[string]*v1.Pod
	lock sync.RWMutex
}

func (a *addressToPod) Read(address string) (*v1.Pod, bool) {
	lock.RLock()
	defer lock.RUnlock()
	pod, ok := a[address]
	return pod, ok
}

func (a *addressToPod) Write(address string, pod *v1.Pod) {
	lock.Lock()
	defer lock.Unlock()
	a[address] = pod
}
