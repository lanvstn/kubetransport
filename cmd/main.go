package main

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/lanvstn/kubetransport/internal"
	"github.com/lanvstn/kubetransport/internal/hostsync"
	"github.com/lanvstn/kubetransport/internal/k8pod"
	"github.com/lanvstn/kubetransport/internal/k8svc"
	"github.com/lanvstn/kubetransport/internal/pf"
	"github.com/lanvstn/kubetransport/internal/state"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"
)

func main() {
	// event signals something changed and it is worth reconciling
	// It may sound a bit weird that we are not using the actual event data
	// But it is not wasteful since informers have a fancy cache and stuff.
	event := make(chan struct{}, 255)

	s := Init(event)

	for range ratelimit(event, 5*time.Second) {
		s = k8svc.Reconcile(s)
		if s.Err != nil {
			log.Printf("ERR: %v", s.Err)
		}
		sort.Sort(s.Forwards)

		s = k8pod.Reconcile(s)
		if s.Err != nil {
			log.Printf("ERR: %v", s.Err)
		}
		sort.Sort(s.Forwards)

		s = hostsync.Reconcile(s)
		if s.Err != nil {
			log.Printf("ERR: %v", s.Err)
		}
		sort.Sort(s.Forwards)

		s = pf.Reconcile(s)
		if s.Err != nil {
			log.Printf("ERR: %v", s.Err)
		}
		sort.Sort(s.Forwards)

		_ = json.NewEncoder(os.Stdout).Encode(&s) //debug
	}
}

func Init(event chan<- struct{}) state.State {
	s := state.State{
		Config:   &state.Config{HostsFilePath: "./myhosts", KubeconfigPath: ""},
		Internal: make(map[internal.ControllerID]any),
	}

	kubeconfig := s.Config.KubeconfigPath
	if kubeconfig == "" {
		if home := homedir.HomeDir(); home != "" {
			kubeconfig = filepath.Join(home, ".kube", "config")
		} else {
			panic("no home directory! kubeconfig path must be set by hand")
		}
	}

	// use the current context in kubeconfig
	config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		return s.WithErr(err)
	}

	// create the clientset
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return s.WithErr(err)
	}

	factory := informers.NewSharedInformerFactory(clientset, 10*time.Second) // TODO raise

	svci := factory.Core().V1().Services()
	podi := factory.Core().V1().Pods()

	for _, i := range []cache.SharedIndexInformer{svci.Informer(), podi.Informer()} {
		i.AddEventHandler(cache.ResourceEventHandlerFuncs{
			AddFunc: func(obj any) {
				event <- struct{}{}
			},
			UpdateFunc: func(oldObj any, newObj any) {
				event <- struct{}{}
			},
			DeleteFunc: func(obj any) {
				event <- struct{}{}
			},
		})
	}

	s = k8svc.Init(s, svci.Lister())
	s = k8pod.Init(s, podi.Lister())
	s = pf.Init(s, config)

	factory.Start(wait.NeverStop)

	return s
}

func ratelimit(c <-chan struct{}, d time.Duration) <-chan struct{} {
	limited := make(chan struct{})

	eat := func() {
		for {
			select {
			case <-c: // omnomnom
			case <-time.After(d):
				limited <- struct{}{}
				return
			}
		}
	}

	go func() {
		for r := range c {
			limited <- r
			eat()
		}
	}()

	return limited
}
