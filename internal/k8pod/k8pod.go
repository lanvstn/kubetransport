package k8pod

import (
	"github.com/lanvstn/kubetransport/internal"
	"github.com/lanvstn/kubetransport/internal/state"
	"github.com/samber/lo"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	lcorev1 "k8s.io/client-go/listers/core/v1"
	_ "k8s.io/client-go/plugin/pkg/client/auth"
)

const controllerID internal.ControllerID = "k8pod"

type internalState struct {
	lister lcorev1.PodLister
}

func getInternal(s state.State) *internalState {
	return s.Internal[controllerID].(*internalState)
}

func Init(s state.State, lister lcorev1.PodLister) state.State {
	s.Internal[controllerID] = &internalState{}
	s.Internal[controllerID].(*internalState).lister = lister

	return s
}

func Reconcile(s state.State) state.State {
	podl, err := getInternal(s).lister.List(labels.Everything())
	if err != nil {
		return s.WithErr(err)
	}

	s.Forwards = lo.Map(s.Forwards, func(fwd state.Forward, _ int) state.Forward {
		if fwd.Service.LabelSelector == nil {
			// Managed service like default/kubernetes
			fwd.Status = state.StatusInvalid
			return fwd
		}

		pod, hasPod := lo.Find(podl, func(pod *corev1.Pod) bool {
			return lo.Every(lo.Entries(pod.Labels), lo.Entries(fwd.Service.LabelSelector)) &&
				pod.Namespace == fwd.Service.Namespace
		})

		if !hasPod {
			fwd.Status = state.StatusWaitPod
			fwd.Pod = state.KResource{}
			return fwd
		}

		fwd.Pod = state.KResource{
			Name:      pod.Name,
			Namespace: pod.Namespace,
		}
		fwd.Status = state.StatusSetup
		return fwd
	})

	return s
}
