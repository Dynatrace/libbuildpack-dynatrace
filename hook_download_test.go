package dynatrace

import (
	"fmt"
	"io"

	"github.com/cloudfoundry/libbuildpack"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("getDownloadURL", func() {
	var hook *Hook

	BeforeEach(func() {
		hook = &Hook{
			Log:                 libbuildpack.NewLogger(io.Discard),
			IncludeTechnologies: []string{"nginx", "process", "dotnet"},
		}
	})

	Describe("OS-specific path segment", func() {
		for _, tc := range []struct {
			operatingSystem string
			expectedSegment string
		}{
			{"linux", "/unix/paas-sh/latest"},
			{"windows", "/windows/paas/latest"},
		} {
			tc := tc // capture loop variable
			It(fmt.Sprintf("uses %s for %s", tc.expectedSegment, tc.operatingSystem), func() {
				creds := &credentials{APIURL: "https://example.com/api", APIToken: "testtoken"}
				url := hook.getDownloadURL(creds, tc.operatingSystem)
				Expect(url).To(ContainSubstring(tc.expectedSegment))
			})
		}
	})

	Describe("CustomOneAgentURL", func() {
		for _, goos := range []string{"linux", "windows"} {
			goos := goos
			It(fmt.Sprintf("bypasses OS-specific path on %s", goos), func() {
				creds := &credentials{
					CustomOneAgentURL: "https://custom.example.com/oneagent",
					APIURL:            "https://example.com/api",
					APIToken:          "testtoken",
				}
				url := hook.getDownloadURL(creds, goos)
				Expect(url).To(Equal(creds.CustomOneAgentURL))
			})
		}
	})

	Describe("technology includes", func() {
		for _, goos := range []string{"linux", "windows"} {
			goos := goos
			It(fmt.Sprintf("includes all configured technologies in the URL on %s", goos), func() {
				creds := &credentials{APIURL: "https://example.com/api", APIToken: "testtoken"}
				url := hook.getDownloadURL(creds, goos)
				for _, tech := range []string{"nginx", "process", "dotnet"} {
					Expect(url).To(ContainSubstring("include=" + tech))
				}
			})
		}
	})

	Describe("AddTechnologies credential", func() {
		for _, goos := range []string{"linux", "windows"} {
			goos := goos
			It(fmt.Sprintf("appends comma-separated addtechnologies to include params on %s", goos), func() {
				creds := &credentials{
					APIURL:          "https://example.com/api",
					APIToken:        "testtoken",
					AddTechnologies: "go,nodejs",
				}
				url := hook.getDownloadURL(creds, goos)
				Expect(url).To(ContainSubstring("include=go"))
				Expect(url).To(ContainSubstring("include=nodejs"))
			})

			It(fmt.Sprintf("omits no extra includes when addtechnologies is empty on %s", goos), func() {
				creds := &credentials{APIURL: "https://example.com/api", APIToken: "testtoken"}
				url := hook.getDownloadURL(creds, goos)
				// Only the hook's IncludeTechnologies should appear, not extra ones
				Expect(url).NotTo(ContainSubstring("include=go"))
				Expect(url).NotTo(ContainSubstring("include=nodejs"))
			})
		}
	})

	Describe("network zone", func() {
		for _, goos := range []string{"linux", "windows"} {
			goos := goos
			Context(fmt.Sprintf("when networkzone is configured on %s", goos), func() {
				It("includes networkZone in the URL", func() {
					creds := &credentials{
						APIURL:      "https://example.com/api",
						APIToken:    "testtoken",
						NetworkZone: "west-us",
					}
					url := hook.getDownloadURL(creds, goos)
					Expect(url).To(ContainSubstring("networkZone=west-us"))
				})
			})

			Context(fmt.Sprintf("when networkzone is not configured on %s", goos), func() {
				It("omits networkZone from the URL", func() {
					creds := &credentials{APIURL: "https://example.com/api", APIToken: "testtoken"}
					url := hook.getDownloadURL(creds, goos)
					Expect(url).NotTo(ContainSubstring("networkZone"))
				})
			})
		}
	})

	Describe("PaaS tenant fallback", func() {
		for _, goos := range []string{"linux", "windows"} {
			goos := goos
			It(fmt.Sprintf("builds URL from environmentid when apiurl is absent on %s", goos), func() {
				creds := &credentials{EnvironmentID: "abc123", APIToken: "testtoken"}
				url := hook.getDownloadURL(creds, goos)
				Expect(url).To(ContainSubstring("abc123.live.dynatrace.com"))
			})
		}
	})

	Describe("unknown operating system", func() {
		It("returns a URL with empty OS path segments", func() {
			creds := &credentials{APIURL: "https://example.com/api", APIToken: "testtoken"}
			url := hook.getDownloadURL(creds, "plan9")
			// osType and installerType stay empty, producing empty path segments
			Expect(url).NotTo(ContainSubstring("/unix/"))
			Expect(url).NotTo(ContainSubstring("/windows/"))
			Expect(url).NotTo(ContainSubstring("/paas-sh/"))
			Expect(url).NotTo(ContainSubstring("/paas/"))
		})

		It("returns the customoneagenturl unchanged for an unknown OS", func() {
			creds := &credentials{
				CustomOneAgentURL: "https://custom.example.com/agent",
				APIToken:          "testtoken",
			}
			url := hook.getDownloadURL(creds, "plan9")
			Expect(url).To(Equal(creds.CustomOneAgentURL))
		})
	})
})
