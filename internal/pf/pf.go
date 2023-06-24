package pf

import (
	"bytes"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/go-errors/errors"
	"github.com/lanvstn/kubetransport/internal"
	"github.com/lanvstn/kubetransport/internal/state"
	"github.com/samber/lo"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/transport/spdy"
)

const controllerID internal.ControllerID = "pf"

type internalState struct {
	config *rest.Config
	dead   chan state.KResource
}

func getInternal(s state.State) *internalState {
	return s.Internal[controllerID].(*internalState)
}

func Init(s state.State, config *rest.Config) state.State {
	s.Internal[controllerID] = &internalState{}
	s.Internal[controllerID].(*internalState).dead = make(chan state.KResource)
	s.Internal[controllerID].(*internalState).config = config

	return s
}

func Reconcile(s state.State) state.State {
	s = buryDead(s)
	s = createForwarders(s)
	return s
}

func buryDead(s state.State) state.State {
	for {
		select {
		case dead := <-getInternal(s).dead:
			_, i, ok := lo.FindIndexOf(s.Forwards, func(fwd state.Forward) bool {
				fmt.Printf("F=%v D=%v\n\n", fwd.Pod, dead)
				return fwd.Pod == &dead
			})
			if !ok {
				panic(fmt.Errorf("non-existing port forward for %q found dead", dead)) // TODO: we may land in this in some cases
			}
			s.Forwards[i].Status = state.StatusSetup
		default:
			return s
		}
	}
}

func createForwarders(s state.State) state.State {
	roundTripper, upgrader, err := spdy.RoundTripperFor(getInternal(s).config)
	if err != nil {
		return s.WithErr(errors.Errorf("making spdy roundtripper: %w", err))
	}

	do := func(pod state.KResource, localIP net.IP) error {
		path := fmt.Sprintf("/api/v1/namespaces/%s/pods/%s/portforward", pod.Namespace, pod.Name)
		hostIP := strings.TrimLeft(getInternal(s).config.Host, "htps:/")
		serverURL := url.URL{Scheme: "https", Path: path, Host: hostIP}

		dialer := spdy.NewDialer(upgrader, &http.Client{Transport: roundTripper}, http.MethodPost, &serverURL)

		stopChan, readyChan := make(chan struct{}, 1), make(chan struct{}, 1)
		out, errOut := new(bytes.Buffer), new(bytes.Buffer)
		forwarder, err := portforward.NewOnAddresses(dialer, []string{localIP.String()}, []string{"8080"}, stopChan, readyChan, out, errOut)
		if err != nil {
			return errors.New(err)
		}

		go func() {
			for range readyChan { // Kubernetes will close this channel when it has something to tell us.
			}
			if len(errOut.String()) != 0 {
				log.Printf("forwarder for pod %s errored: %s", pod, errOut.String()) // TODO field logging plz
			} else if len(out.String()) != 0 {
				log.Printf("forwarder for pod %s said: %s", pod, out.String())
			}
		}()

		dead := getInternal(s).dead

		go func() {
			if err = forwarder.ForwardPorts(); err != nil { // Locks until stopChan is closed.
				dead <- pod
			}
		}()

		return nil
	}

	s.Forwards = lo.Map(s.Forwards, func(fwd state.Forward, _ int) state.Forward {
		if fwd.Status != state.StatusSetup {
			return fwd
		}

		if fwd.Pod == nil {
			return fwd
		}

		err := do(*fwd.Pod, fwd.LocalIP)
		if err != nil {
			fwd.Err = err
			return fwd
		}

		fwd.Status = state.StatusActive
		return fwd
	})

	return s
}
