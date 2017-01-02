package main

import (
	"flag"

	"github.com/golang/glog"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/pkg/api"
	"k8s.io/client-go/pkg/api/v1"
	"k8s.io/client-go/pkg/runtime"
	"k8s.io/client-go/pkg/util/workqueue"
	"k8s.io/client-go/pkg/watch"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/kubernetes/pkg/controller"

	// Only required to authenticate against GKE clusters
	//_ "k8s.io/client-go/plugin/pkg/client/auth/gcp"
)

func getClientsetOrDie(kubeconfig string) *kuberntes.Clientset {
	// Create the client config. Use kubeconfig if given, otherwise assume in-cluster.
	config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		panic(err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		panic(err)
	}
}

func main() {
	kubeconfig := flag.String("kubeconfig", "", "Path to a kube config. Only required if out-of-cluster.")
	flag.Parse()
	controller := newServiceLookupController(kubeconfig)
}

type ServiceLookupManager struct {
	clientset *kubernetes.Clientset
	// A cache of services
	serviceStore cache.StoreToServiceLister
	// Watches changes to all services
	serviceController *cache.Controller
	// A cache of pods
	podStore cache.StoreToPodLister
	// Watches changes to all pods
	podController cache.Controller

	podsQueue     workqueue.RateLimitingInterface
	servicesQueue workqueue.RateLimitingInterface
}

func newServiceLookupController(kubeconfig) *ServiceLookupManager {
	slm := &ServiceLookupManager{
		kubeClient:    getClientsetOrDie(kubeconfig),
		podsQueue:     workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "pods"),
		servicesQueue: workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "services"),
	}

	slm.podStore.Indexer, slm.podController = cache.NewIndexerInformer(
		&cache.ListWatch{
			ListFunc: func(options v1.ListOptions) (runtime.Object, error) {
				return slm.kubeClient.Core().Pods(api.NamespaceAll).List(options)
			},
			WatchFunc: func(options api.ListOptions) (watch.Interface, error) {
				return slm.kubeClient.Core().Pods(api.NamespaceAll).Watch(options)
			},
		},
		&v1.Pod{},
		// resync is not needed
		0,
		cache.ResourceEventHandlerFuncs{
			AddFunc: slm.enqueuePod,
		},
	)

	slm.serviceStore.Indexer, slm.serviceController = cache.NewIndexerInformer(
		&cache.ListWatch{
			ListFunc: func(options v1.ListOptions) (runtime.Object, error) {
				return slm.kubeClient.Core().Services(api.NamespaceAll).List(options)
			},
			WatchFunc: func(options api.ListOptions) (watch.Interface, error) {
				return slm.kubeClient.Core().Services(api.NamespaceAll).Watch(options)
			},
		},
		&v1.Service{},
		// resync is not needed
		0,
		cache.ResourceEventHandlerFuncs{
			AddFunc:    slm.enqueueService,
			UpdateFunc: slm.enqueueService,
			DeleteFunc: slm.enqueueService,
		},
	)
}

// obj could be an *api.ReplicationController, or a DeletionFinalStateUnknown marker item.
func (rm *ServiceLookupManager) enqueuePod(obj interface{}) {
	key, err := controller.KeyFunc(obj)
	if err != nil {
		glog.Errorf("Couldn't get key for object %+v: %v", obj, err)
		return
	}

	// TODO: Handle overlapping controllers better. Either disallow them at admission time or
	// deterministically avoid syncing controllers that fight over pods. Currently, we only
	// ensure that the same controller is synced for a given pod. When we periodically relist
	// all controllers there will still be some replica instability. One way to handle this is
	// by querying the store for all controllers that this rc overlaps, as well as all
	// controllers that overlap this rc, and sorting them.
	rm.queue.Add(key)
}
