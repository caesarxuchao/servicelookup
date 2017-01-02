package main

// Only required to authenticate against GKE clusters
//_ "k8s.io/client-go/plugin/pkg/client/auth/gcp"

type PodToService struct {
	PodName        string `json:"podName"`
	PodUID         string `json:"podUID"`
	ServiceName    string `json:"serviceName,omitempty"`
	ServiceAddress string `json:"serviceAddress,omitempty"`
}
