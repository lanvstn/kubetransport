package state

import (
	"fmt"
	"net"

	"github.com/go-errors/errors"
	"github.com/lanvstn/kubetransport/internal"
)

type Status string

const (
	StatusInvalid   = "INVALID"
	StatusWaitPod   = "WAIT_POD"
	StatusSetup     = "SETUP_LISTEN"
	StatusListening = "LISTENING"
	StatusActive    = "ACTIVE"
	StatusTeardown  = "TEARDOWN" // TODO
)

type State struct {
	Forwards Forwards
	Err      error
	Config   *Config

	Internal map[internal.ControllerID]any `json:"-"`
}

type Forward struct {
	Pod     KResource
	Service Service
	LocalIP net.IP

	Status Status
	Err    error
}

type Service struct {
	KResource
	LabelSelector map[string]string
}

type KResource struct {
	Name      string
	Namespace string
}

type Config struct {
	HostsFilePath  string
	KubeconfigPath string
}

type Forwards []Forward

func (s State) WithErr(err error) State {
	s.Err = errors.New(err)
	return s
}

func (f Forwards) Len() int           { return len(f) }
func (f Forwards) Swap(i, j int)      { f[i], f[j] = f[j], f[i] }
func (f Forwards) Less(i, j int) bool { return f[i].String() < f[j].String() }

func (f Forward) String() string {
	return fmt.Sprintf("svc:%s pod:%s localip:%s status:%s",
		f.Service.String(), f.Pod.String(), f.LocalIP.String(), f.Status)
}

func (k KResource) String() string {
	if k.Name == "" || k.Namespace == "" {
		return ""
	}
	return fmt.Sprintf("%s/%s", k.Namespace, k.Name)
}
