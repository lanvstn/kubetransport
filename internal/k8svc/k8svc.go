package k8svc

import (
	"github.com/lanvstn/kubetransport/internal"
	"github.com/lanvstn/kubetransport/internal/state"
	"github.com/samber/lo"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	lcorev1 "k8s.io/client-go/listers/core/v1"
	_ "k8s.io/client-go/plugin/pkg/client/auth"
)

const controllerID internal.ControllerID = "k8svc"

type internalState struct {
	lister lcorev1.ServiceLister
}

func getInternal(s state.State) *internalState {
	return s.Internal[controllerID].(*internalState)
}

func Init(s state.State, lister lcorev1.ServiceLister) state.State {
	s.Internal[controllerID] = &internalState{}
	s.Internal[controllerID].(*internalState).lister = lister

	return s
}

func Reconcile(s state.State) state.State {
	svcl, err := getInternal(s).lister.List(labels.Everything())
	if err != nil {
		return s.WithErr(err)
	}

	// Rebuild forwards using service list, keeping existing entries
	s.Forwards = lo.Map(svcl, func(svc *corev1.Service, _ int) state.Forward {
		if fwd, ok := lo.Find(s.Forwards, func(fwd state.Forward) bool {
			return fwd.Service.Name == svc.Name && fwd.Service.Namespace == svc.Namespace
		}); ok {
			return fwd
		}

		return state.Forward{
			Service: state.Service{
				KResource: state.KResource{
					Name:      svc.Name,
					Namespace: svc.Namespace,
				},
				LabelSelector: svc.Spec.Selector,
			},
			Status: state.StatusWaitPod,
		}
	})

	return s
}
