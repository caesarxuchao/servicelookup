package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/pkg/api"
	"k8s.io/client-go/pkg/api/v1"
	"k8s.io/client-go/pkg/runtime"
	utilruntime "k8s.io/client-go/pkg/util/runtime"
	"k8s.io/client-go/pkg/util/wait"
	"k8s.io/client-go/pkg/util/workqueue"
	"k8s.io/client-go/pkg/watch"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/kubernetes/pkg/controller"

	// Only required to authenticate against GKE clusters
	//_ "k8s.io/client-go/plugin/pkg/client/auth/gcp"
)

func getClientsetOrDie(kubeconfig *string) *kubernetes.Clientset {
	// Create the client config. Use kubeconfig if given, otherwise assume in-cluster.
	config, err := clientcmd.BuildConfigFromFlags("", *kubeconfig)
	if err != nil {
		panic(err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		panic(err)
	}
	return clientset
}

func main() {
	kubeconfig := flag.String("kubeconfig", "", "Path to a kube config. Only required if out-of-cluster.")
	flag.Parse()
	controller := newendpointLookupController(kubeconfig)
}

type serviceLookupController struct {
	kubeClient *kubernetes.Clientset
	// A cache of endpoints
	endpointsStore cache.Store
	// Watches changes to all endpoints
	endpointController *cache.Controller
	// A cache of pods
	podStore cache.StoreToPodLister
	// Watches changes to all pods
	podController *cache.Controller

	addressToPod   addressToPod
	podsQueue      workqueue.RateLimitingInterface
	endpointsQueue workqueue.RateLimitingInterface
}

func (slm *serviceLookupController) enqueuePod(obj interface{}) {
	key, err := controller.KeyFunc(obj)
	if err != nil {
		fmt.Printf("Couldn't get key for object %+v: %v", obj, err)
		return
	}
	slm.podsQueue.Add(key)
}

func (slm *serviceLookupController) podWorker() {
	workFunc := func() bool {
		key, quit := slm.podsQueue.Get()
		if quit {
			return true
		}
		defer slm.podsQueue.Done(key)

		obj, exists, err := slm.podStore.Indexer.GetByKey(key.(string))
		if !exists {
			fmt.Printf("Pod has been deleted %v", key)
			return false
		}

		pod := obj.(*v1.Pod)
		slm.addressToPod.Write(pod.Status.PodIP, pod)
		return false
	}
	for {
		if quit := workFunc(); quit {
			fmt.Printf("pod worker shutting down")
			return
		}
	}
}

func (slm *serviceLookupController) enqueueendpoint(obj interface{}) {
	key, err := controller.KeyFunc(obj)
	if err != nil {
		fmt.Printf("Couldn't get key for object %+v: %v", obj, err)
		return
	}
	slm.endpointsQueue.Add(key)
}

func (slm *serviceLookupController) endpointWorker() {
	workFunc := func() bool {
		key, quit := slm.endpointsQueue.Get()
		if quit {
			return true
		}
		defer slm.endpointsQueue.Done(key)

		obj, exists, err := slm.endpointsStore.GetByKey(key.(string))
		if !exists {
			fmt.Printf("endpoint has been deleted %v", key)
			return false
		}

		endpoints := obj.(*v1.Endpoints)

		var output string
		for _, subset := range endpoints.Subsets {
			for _, address := range subset.Addresses {
				// TODO: need a lock here
				pod, ok := slm.addressToPod.Read(address.IP)
				if !ok {
					slm.endpointsQueue.AddRateLimited(key)
					return false
				}
				output += fmt.Sprintf("Pod [%s:%s] is backing service %s\n", pod.ObjectMeta.Name, address, endpoints.ObjectMeta.Name)
			}
		}

		fmt.Fprintf(os.Stderr, output)
		return false
	}
	for {
		if quit := workFunc(); quit {
			fmt.Printf("pod worker shutting down")
			return
		}
	}
}

func newendpointLookupController(kubeconfig *string) *serviceLookupController {
	slm := &serviceLookupController{
		kubeClient:     getClientsetOrDie(kubeconfig),
		podsQueue:      workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "pods"),
		endpointsQueue: workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "endpoints"),
	}

	slm.podStore.Indexer, slm.podController = cache.NewInformer(
		&cache.ListWatch{
			ListFunc: func(options v1.ListOptions) (runtime.Object, error) {
				return slm.kubeClient.Core().Pods(api.NamespaceAll).List(options)
			},
			WatchFunc: func(options v1.ListOptions) (watch.Interface, error) {
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

	slm.endpointsStore, slm.endpointController = cache.NewInformer(
		&cache.ListWatch{
			ListFunc: func(options v1.ListOptions) (runtime.Object, error) {
				return slm.kubeClient.Core().Endpoints(api.NamespaceAll).List(options)
			},
			WatchFunc: func(options v1.ListOptions) (watch.Interface, error) {
				return slm.kubeClient.Core().Endpoints(api.NamespaceAll).Watch(options)
			},
		},
		&v1.Endpoints{},
		// resync is not needed
		0,
		cache.ResourceEventHandlerFuncs{
			AddFunc:    slm.enqueueendpoint,
			UpdateFunc: slm.enqueueendpoint,
			DeleteFunc: slm.enqueueendpoint,
		},
	)
	return slm
}

// Run begins watching and syncing.
func (slm *serviceLookupController) Run(workers int, stopCh <-chan struct{}) {
	defer utilruntime.HandleCrash()
	fmt.Println("Starting serviceLookupController Manager")
	go slm.podController.Run(stopCh)
	go slm.endpointController.Run(stopCh)
	// wait for the controller to List. This help avoid churns during start up.
	if !cache.WaitForCacheSync(stopCh, slm.podController.HasSynced, slm.endpointController.HasSynced) {
		return
	}
	for i := 0; i < workers; i++ {
		go wait.Until(slm.podWorker, time.Second, stopCh)
		go wait.Until(slm.endpointWorker, time.Second, stopCh)
	}

	<-stopCh
	fmt.Printf("Shutting down Service Lookup Controller")
	slm.podsQueue.ShutDown()
	slm.endpointsQueue.ShutDown()
}
