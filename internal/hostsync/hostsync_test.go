package hostsync

import (
	"bytes"
	"testing"

	"github.com/lanvstn/kubetransport/internal/state"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestHostSync(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "hostsync units")
}

var _ = Describe("hostsync", func() {
	const emptyHostsFile = `# Some comment
127.0.0.1       localhost
::1             localhost
`

	mockState := state.State{
		Forwards: []state.Forward{
			{
				Status: state.StatusInvalid,
				Service: state.Service{
					KResource: state.KResource{
						Name:      "kubernetes",
						Namespace: "default",
					},
				},
			},
			{
				Status: state.StatusSetup,
				Pod: &state.KResource{
					Name:      "foo123",
					Namespace: "default",
				},
				Service: state.Service{
					KResource: state.KResource{
						Name:      "foo",
						Namespace: "default",
					},
				},
			},
			{
				Status: state.StatusSetup,
				Pod: &state.KResource{
					Name:      "bar123",
					Namespace: "default",
				},
				Service: state.Service{
					KResource: state.KResource{
						Name:      "bar",
						Namespace: "default",
					},
				},
			},
			{
				Status: state.StatusSetup,
				Pod: &state.KResource{
					Name:      "baz-0",
					Namespace: "bang",
				},
				Service: state.Service{
					KResource: state.KResource{
						Name:      "baz",
						Namespace: "bang",
					},
				},
			},
		},
	}

	It("must load hosts correctly", func() {
		loaded, err := loadHosts(bytes.NewReader([]byte(emptyHostsFile)))
		Expect(err).ToNot(HaveOccurred())
		Expect(loaded.newline).To(Equal("\n"))
		Expect(loaded.raw).ToNot(BeZero())
		Expect(loaded.unmanaged).To(HaveLen(3))
	})

	It("must build hosts", func() {
		s := assignIPs(mockState)
		Expect(buildHosts(s)).To(ConsistOf(
			"127.0.16.1 foo.default foo.default.svc foo.default.svc.cluster.local",
			"127.0.16.2 bar.default bar.default.svc bar.default.svc.cluster.local",
			"127.0.16.3 baz.bang baz.bang.svc baz.bang.svc.cluster.local",
		))
	})
})
