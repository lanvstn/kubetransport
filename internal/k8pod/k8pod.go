package k8pod

// import (
// 	"context"
// 	"path/filepath"

// 	"github.com/lanvstn/kubetransport/internal"
// 	"github.com/lanvstn/kubetransport/internal/state"
// 	"github.com/samber/lo"
// 	corev1 "k8s.io/api/core/v1"
// 	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
// 	"k8s.io/client-go/kubernetes"
// 	_ "k8s.io/client-go/plugin/pkg/client/auth"
// 	"k8s.io/client-go/tools/clientcmd"
// 	"k8s.io/client-go/util/homedir"
// )

// const controllerID internal.ControllerID = "k8pod"

// type internalState struct {
// 	clientset *kubernetes.Clientset
// }

// func getInternal(s state.State) *internalState {
// 	return s.Internal[controllerID].(*internalState)
// }

// func Init(s state.State) state.State {
// 	s.Internal[controllerID] = &internalState{}

// 	kubeconfig := s.Config.KubeconfigPath
// 	if kubeconfig == "" {
// 		if home := homedir.HomeDir(); home != "" {
// 			filepath.Join(home, ".kube", "config")
// 		} else {
// 			panic("no home directory! kubeconfig path must be set by hand")
// 		}
// 	}

// 	// use the current context in kubeconfig
// 	config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
// 	if err != nil {
// 		return s.WithErr(err)
// 	}

// 	// create the clientset
// 	clientset, err := kubernetes.NewForConfig(config)
// 	if err != nil {
// 		return s.WithErr(err)
// 	}

// 	s.Internal[controllerID].(*internalState).clientset = clientset

// 	return s
// }

// func Reconcile(s state.State) state.State {
// 	svcl, err := getInternal(s).clientset.CoreV1().Pods("").List(context.TODO(), v1.ListOptions{})
// 	if err != nil {
// 		return s.WithErr(err)
// 	}

// 	// Rebuild forwards using service list, keeping existing entries
// 	s.Forwards = lo.Map(svcl.Items, func(svc corev1.Service, _ int) state.Forward {
// 		if fwd, ok := lo.Find(s.Forwards, func(fwd state.Forward) bool {
// 			return fwd.Service.Name == svc.Name && fwd.Service.Namespace == svc.Namespace
// 		}); ok {
// 			return fwd
// 		}

// 		return state.Forward{
// 			Service: state.KResource{
// 				Name:      svc.Name,
// 				Namespace: svc.Namespace,
// 			},
// 		}
// 	})

// 	return s
// }
