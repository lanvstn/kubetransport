package internal2

import (
	"encoding/json"
	"log"
	"net"
	"net/netip"
	"sort"

	"github.com/go-errors/errors"
	"github.com/samber/lo"
	"go.etcd.io/bbolt"
)

type forwarderKey struct {
	ServiceName      string
	ServiceNamespace string
}

type forwarder struct {
	died      chan associatedService
	active    map[forwarderKey]chan<- struct{} // close kills the forward
	lastState []associatedService
}

type forwarderDiff struct {
	added   []associatedService
	removed []associatedService
}

func (as associatedService) forwarderKey() forwarderKey {
	return forwarderKey{
		ServiceName:      as.Service.Name,
		ServiceNamespace: as.Service.Namespace,
	}
}

func (s service) forwarderKey() forwarderKey {
	return forwarderKey{
		ServiceName:      s.Name,
		ServiceNamespace: s.Namespace,
	}
}

func (as associatedService) preferredPod() pod {
	if len(as.Pods) == 0 {
		panic("associated service not associated!?")
	}
	pods2 := make([]pod, len(as.Pods))
	copy(pods2, as.Pods)
	sort.Sort(pods(pods2))
	return pods2[0]
}

func newForwarder() *forwarder {
	return &forwarder{
		died:      make(chan associatedService),
		active:    make(map[forwarderKey]chan<- struct{}),
		lastState: nil,
	}
}

func diff(newState, lastState []associatedService) forwarderDiff {
	// matchPredicate answers: Is the given svc NOT included in otherSvcSet?
	matchPredicate := func(otherSvcSet []associatedService, svc associatedService, _ int) bool {
		return !lo.ContainsBy(otherSvcSet, func(lastSvc associatedService) bool {
			return svc.Service.Name == lastSvc.Service.Name &&
				svc.Service.Namespace == lastSvc.Service.Namespace &&
				svc.preferredPod().Name == lastSvc.preferredPod().Name
		})
	}

	return forwarderDiff{
		// for newState, filter by whether each entry is NOT included in lastState
		added:   lo.Filter(newState, lo.Partial2(matchPredicate, lastState)),
		removed: lo.Filter(lastState, lo.Partial2(matchPredicate, newState)), // the other way around
	}
}

func (f *forwarder) syncForwards(svcs []associatedService, db *bbolt.DB) error {
	d := diff(svcs, f.lastState)

	for _, removed := range d.removed {
		close(f.active[removed.forwarderKey()])
	}

	allDead := make([]associatedService, 0)
drainDied:
	for {
		select {
		case dead := <-f.died:
			allDead = append(allDead, dead)
		default:
			break drainDied
		}
	}

	deadNotInRemoved := diff(d.removed, allDead).removed
	deadThatShouldBeAlive := diff(d.added, deadNotInRemoved).removed

	svc2ip := make(map[forwarderKey]net.IP)
	err := db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket(allocBucket)
		if b == nil {
			return errors.New("allocBucket not present in DB")
		}

		err := b.ForEach(func(k, v []byte) error {
			var (
				nipa netip.Addr
				nip  net.IP
			)
			err := json.Unmarshal(k, &nipa)
			if err != nil {
				return errors.New(err)
			}
			if nipa.Is4() {
				a4 := nipa.As4()
				nip = net.IP(a4[:])
			} else if nipa.Is6() {
				a16 := nipa.As16()
				nip = net.IP(a16[:])
			}

			svcFromDB := &service{}
			err = json.Unmarshal(v, svcFromDB)
			if err != nil {
				return errors.New(err)
			}

			svc2ip[svcFromDB.forwarderKey()] = nip
			return nil
		})
		if err != nil {
			return errors.New(err)
		}

		return nil
	})
	if err != nil {
		return errors.New(err)
	}

	// reincarnate the dead along the newborns
	for _, todo := range append(d.added, deadThatShouldBeAlive...) {
		kill := make(chan struct{})
		forward(todo, svc2ip[todo.forwarderKey()], kill, f.died)
	}

	return nil
}

func forward(svc associatedService, ip net.IP, kill <-chan struct{}, died chan<- associatedService) {
	for _, port := range svc.preferredPod().Ports {
		port := port

		addr := &net.TCPAddr{
			IP:   ip,
			Port: int(port.Port),
		}

		l, err := net.ListenTCP("tcp", addr)
		if err != nil {
			log.Printf("%v: failed listen %v: %v", svc, addr, err)
			died <- svc
			return
		}

		go func() {
			<-kill
			l.Close()
		}()

		go func() {
			for {
				conn, err := l.Accept()
				if err != nil {
					log.Printf("%v: failed accept %v: %v", svc, addr, err)
					died <- svc
					return
				}

				go func() {
					err := handle(conn)
					if err != nil {
						log.Printf("%v: failed conn handler %v: %v", svc, addr, err)
						return
					}
				}()
			}
		}()
	}
}

func handle(conn net.Conn) error {
	// TODO set up lazy port forwarding
}
