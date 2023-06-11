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
				Pod: state.KResource{
					Name:      "foo123",
					Namespace: "default",
				},
				Service: state.KResource{
					Name:      "foo",
					Namespace: "default",
				},
			},
			{
				Pod: state.KResource{
					Name:      "bar123",
					Namespace: "default",
				},
				Service: state.KResource{
					Name:      "bar",
					Namespace: "default",
				},
			},
			{
				Pod: state.KResource{
					Name:      "baz-0",
					Namespace: "bang",
				},
				Service: state.KResource{
					Name:      "baz",
					Namespace: "bang",
				},
			},
		},
	}

	It("must load hosts correctly", func() {
		Expect(loadHosts(bytes.NewReader([]byte(emptyHostsFile)))).To(Equal(nil))
	})

	It("must build hosts", func() {
		Expect(buildHosts(mockState)).To(ConsistOf(
			"127.0.16.1 foo.default foo.default.svc foo.default.svc.cluster.local",
			"127.0.16.2 bar.default bar.default.svc bar.default.svc.cluster.local",
			"127.0.16.3 baz.bang baz.bang.svc baz.bang.svc.cluster.local",
		))
	})
})
