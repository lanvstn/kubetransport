package state

import (
	"net"

	"github.com/go-errors/errors"
	"github.com/lanvstn/kubetransport/internal"
)

type Status string

const (
	StatusSettingUp = "SETTING_UP"
	StatusListening = "LISTENING"
	StatusActive    = "ACTIVE"
)

type State struct {
	Forwards []Forward
	Err      error
	Config   *Config

	Internal map[internal.ControllerID]any `json:"-"`
}

type Forward struct {
	Pod     KResource
	Service KResource
	LocalIP net.IP

	Status Status
	Err    error
}

type KResource struct {
	Name      string
	Namespace string
}

type Config struct {
	HostsFilePath  string
	KubeconfigPath string
}

func (s State) WithErr(err error) State {
	s.Err = errors.New(err)
	return s
}
