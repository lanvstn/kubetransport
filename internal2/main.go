package internal2

import (
	"encoding/json"
	"fmt"
	"net/netip"
	"os"
	"sort"

	"github.com/go-errors/errors"
	"github.com/samber/lo"
	"go.etcd.io/bbolt"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	lcorev1 "k8s.io/client-go/listers/core/v1"
)

type service struct {
	Name          string
	Namespace     string
	LabelSelector map[string]string
	Ports         []servicePort
}

type servicePort struct {
	Port       int32
	TargetNum  int32
	TargetName string
}

type associatedService struct {
	Service service
	Pods    []pod
}

type pod struct {
	Name      string
	Namespace string
	Ports     []podPort
}

type podPort struct {
	Port int32
	Name string
}

type ips []netip.Addr

func (f ips) Len() int           { return len(f) }
func (f ips) Swap(i, j int)      { f[i], f[j] = f[j], f[i] }
func (f ips) Less(i, j int) bool { return f[i].Less(f[j]) }

func Run(slister lcorev1.ServiceLister, plister lcorev1.PodLister, db *bbolt.DB) error {
	svcs, err := services(slister)
	if err != nil {
		return err
	}

	assoc, err := associateServicesPods(plister, svcs)
	if err != nil {
		return err
	}

	err = allocateServices(assoc, db, netip.MustParsePrefix("127.0.16.0/24"))
	if err != nil {
		return err
	}

	_ = json.NewEncoder(os.Stdout).Encode(&assoc)

	return nil
}

func allocateServices(svcs []associatedService, db *bbolt.DB, cidr netip.Prefix) error {
	var allocBucket = []byte("ip-alloc")

	return db.Update(func(tx *bbolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists(allocBucket)
		if err != nil {
			return errors.New(err)
		}

		// Collect latest ip to service mapping
		ip2svc := make(map[string]service)
		err = b.ForEach(func(k, v []byte) error {
			ipStr := ""
			err := json.Unmarshal(k, &ipStr)
			if err != nil {
				return errors.New(err)
			}

			svcFromDB := &service{}
			err = json.Unmarshal(v, svcFromDB)
			if err != nil {
				return errors.New(err)
			}

			ip2svc[ipStr] = *svcFromDB
			return nil
		})
		if err != nil {
			return errors.New(err)
		}

		allocatedAddrs := ips(lo.Map(lo.Keys(ip2svc), func(ipStr string, _ int) netip.Addr {
			return netip.MustParseAddr(ipStr)
		}))
		sort.Sort(allocatedAddrs)

		unallocatedSvcs := lo.Filter(svcs, func(svc associatedService, _ int) bool {
			return !lo.ContainsBy(lo.Values(ip2svc), func(allocatedSvc service) bool {
				return allocatedSvc.Name == svc.Service.Name &&
					allocatedSvc.Namespace == svc.Service.Namespace
			})
		})

		cur := cidr.Addr()
		for _, usvc := range unallocatedSvcs {
			cur = cur.Next()
			for lo.Contains(allocatedAddrs, cur) {
				cur = cur.Next()
			}

			if !cidr.Contains(cur) {
				return errors.New("out of IP addresses in cidr!")
			}

			// Cur is yet unallocated
			fmt.Println(cur)
			err := b.Put(lo.Must(json.Marshal(cur)), lo.Must(json.Marshal(usvc.Service)))
			if err != nil {
				return errors.New(err)
			}
		}

		return nil
	})
}

func associateServicesPods(lister lcorev1.PodLister, svcs []service) ([]associatedService, error) {
	podl, err := lister.List(labels.Everything())
	if err != nil {
		return nil, errors.New(err)
	}

	return lo.Map(svcs, func(svc service, _ int) associatedService {
		if svc.LabelSelector == nil {
			// Managed service like default/kubernetes
			return associatedService{Service: svc}
		}

		return associatedService{
			Service: svc,
			Pods: lo.Map(lo.Filter(podl, func(thisPod *corev1.Pod, _ int) bool {
				// Match pods to service
				return lo.Every(lo.Entries(thisPod.Labels), lo.Entries(svc.LabelSelector)) &&
					thisPod.Namespace == svc.Namespace
			}), func(thisPod *corev1.Pod, _ int) pod {
				// Transform
				return pod{
					Name:      thisPod.Name,
					Namespace: thisPod.Namespace,
					Ports: lo.FlatMap(thisPod.Spec.Containers, func(container corev1.Container, _ int) []podPort {
						return lo.Map(container.Ports, func(containerPort corev1.ContainerPort, _ int) podPort {
							return podPort{
								Port: containerPort.ContainerPort,
								Name: containerPort.Name,
							}
						})
					}),
				}
			}),
		}
	}), nil
}

func services(lister lcorev1.ServiceLister) ([]service, error) {
	svcl, err := lister.List(labels.Everything())
	if err != nil {
		return nil, errors.New(err)
	}

	return lo.Map(svcl, func(svc *corev1.Service, _ int) service {
		return service{
			Name:          svc.Name,
			Namespace:     svc.Namespace,
			LabelSelector: svc.Spec.Selector,
			Ports: lo.FlatMap(svc.Spec.Ports, func(port corev1.ServicePort, _ int) []servicePort {
				if port.AppProtocol != nil && *port.AppProtocol != string(corev1.ProtocolTCP) {
					return nil // we only do TCP
				}
				return []servicePort{{
					Port:       port.Port,
					TargetNum:  lo.Ternary(port.TargetPort.IntVal != 0, port.TargetPort.IntVal, 0),
					TargetName: lo.Ternary(port.TargetPort.StrVal != "", port.TargetPort.StrVal, ""),
				}}
			}),
		}
	}), nil
}
