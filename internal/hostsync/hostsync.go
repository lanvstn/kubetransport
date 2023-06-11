package hostsync

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"net"
	"os"
	"strings"

	"github.com/go-errors/errors"
	"github.com/samber/lo"

	"github.com/lanvstn/kubetransport/internal"
	"github.com/lanvstn/kubetransport/internal/state"
)

const controllerID internal.ControllerID = "hostsync"

const managedMarker = "# KUBETRANSPORT MANAGED"

type hostsFile struct {
	raw       []byte
	unmanaged []string
	newline   string
}

func Reconcile(s state.State) state.State {
	s = assignIPs(s)

	f, err := os.Open(s.Config.HostsFilePath)
	if err != nil {
		return s.WithErr(err)
	}

	hosts, err := loadHosts(f)
	if err != nil {
		f.Close()
		return s.WithErr(err)
	}

	f.Close()

	newHostsBytes := []byte(strings.Join(
		mergeHosts(hosts.unmanaged, buildHosts(s)),
		hosts.newline))

	if !bytes.Equal(newHostsBytes, hosts.raw) {
		err := os.WriteFile(s.Config.HostsFilePath, newHostsBytes, 0666)
		if err != nil {
			return s.WithErr(err)
		}
	}

	return s
}

func buildHosts(s state.State) []string {
	return lo.Reduce(s.Forwards, func(agg []string, fwd state.Forward, _ int) []string {
		return append(agg, fmt.Sprintf("%s %s",
			fwd.LocalIP,
			strings.Join(namesForSvc(fwd.Service), " ")))
	}, []string{})
}

// TODO this function is dumb and swap around IPs for no reason, at least sort the thing or something
func assignIPs(s state.State) state.State {
	curIP := net.IPv4(127, 0, 16, 1) // IPv6 would be nice but be careful implementing this (net.IP type has footguns here)
	s.Forwards = lo.Map(s.Forwards, func(fwd state.Forward, _ int) state.Forward {
		fwd.LocalIP = curIP
		curIP = nextIP(curIP)
		return fwd
	})

	return s
}

func nextIP(curIP net.IP) net.IP {
	n := make(net.IP, 4)
	copy(n, curIP.To4())

	if n[3] == 255 {
		if n[2] == 255 {
			panic("out of IPs")
		}
		n[2]++
	} else {
		n[3]++
	}

	return n
}

func namesForSvc(svc state.KResource) []string {
	return []string{
		// TODO support leaving out namespace part for selected namespace
		fmt.Sprintf("%s.%s", svc.Name, svc.Namespace),
		fmt.Sprintf("%s.%s.svc", svc.Name, svc.Namespace),
		fmt.Sprintf("%s.%s.svc.cluster.local", svc.Name, svc.Namespace), // TODO config this
	}
}

func loadHosts(r io.Reader) (*hostsFile, error) {
	raw, err := io.ReadAll(r)
	if err != nil {
		return nil, errors.New(err)
	}

	newline := "\n"
	if bytes.ContainsRune(raw, '\r') {
		newline = "\r\n" // windows :(
	}

	out := make([]string, 0, 10)

	scanner := bufio.NewScanner(bytes.NewReader(raw))
	ignoring := false

	for scanner.Scan() {
		line := scanner.Text()
		if line == managedMarker {
			if !ignoring && len(out) > 0 {
				// ignore the last newline that was us writing last time, we will do it again in mergeHosts
				out = out[:len(out)-1]
			}
			ignoring = !ignoring
		} else if !ignoring {
			out = append(out, line)
		}
	}

	return &hostsFile{
		raw:       raw,
		unmanaged: out,
		newline:   newline,
	}, nil
}

func mergeHosts(unmanaged, managed []string) []string {
	out := make([]string, 0, len(managed)+len(unmanaged)+4)

	out = append(out, unmanaged...)
	out = append(out, "")
	out = append(out, managedMarker)
	out = append(out, managed...)
	out = append(out, managedMarker)
	out = append(out, "")

	return out
}
