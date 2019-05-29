package dynatrace_test

import (
	"bytes"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Dynatrace/libbuildpack-dynatrace"
	"github.com/cloudfoundry/libbuildpack"
	"github.com/golang/mock/gomock"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	"gopkg.in/jarcoal/httpmock.v1"
)

//go:generate mockgen -source=hook.go --destination=mocks_test.go --package=dynatrace_test

var _ = Describe("dynatraceHook", func() {
	var (
		err          error
		bpDir        string
		buildDir     string
		depsDir      string
		depsIdx      string
		logger       *libbuildpack.Logger
		stager       *libbuildpack.Stager
		mockCtrl     *gomock.Controller
		mockCommand  *MockCommand
		buffer       *bytes.Buffer
		hook         dynatrace.Hook
		runInstaller func(string, io.Writer, io.Writer, string, string)
	)

	BeforeEach(func() {
		bpDir, err = ioutil.TempDir("", "libbuildpack-dynatrace.buildpack.")
		Expect(err).To(BeNil())

		buildDir, err = ioutil.TempDir("", "libbuildpack-dynatrace.build.")
		Expect(err).To(BeNil())

		depsDir, err = ioutil.TempDir("", "libbuildpack-dynatrace.deps.")
		Expect(err).To(BeNil())

		depsIdx = "07"
		err = os.MkdirAll(filepath.Join(depsDir, depsIdx), 0755)

		buffer = new(bytes.Buffer)
		logger = libbuildpack.NewLogger(io.MultiWriter(buffer, GinkgoWriter))

		mockCtrl = gomock.NewController(GinkgoT())
		mockCommand = NewMockCommand(mockCtrl)
		hook = dynatrace.Hook{
			Command:             mockCommand,
			Log:                 logger,
			MaxDownloadRetries:  0,
			IncludeTechnologies: []string{"nginx", "process", "dotnet"},
		}

		os.Setenv("DT_LOGSTREAM", "")

		ioutil.WriteFile(filepath.Join(bpDir, "manifest.yml"), []byte("---\nlanguage: test42\n"), 0755)
		ioutil.WriteFile(filepath.Join(bpDir, "VERSION"), []byte("1.2.3"), 0755)

		httpmock.Reset()

		runInstaller = func(_ string, _, _ io.Writer, file string, _ string) {
			contents, err := ioutil.ReadFile(file)
			Expect(err).To(BeNil())

			Expect(string(contents)).To(Equal("echo Install Dynatrace"))

			err = os.MkdirAll(filepath.Join(buildDir, "dynatrace/oneagent/agent/lib64"), 0755)
			Expect(err).To(BeNil())

			err = ioutil.WriteFile(filepath.Join(buildDir, "dynatrace/oneagent/agent/lib64/liboneagentproc.so"), []byte("library"), 0644)
			Expect(err).To(BeNil())

			err = ioutil.WriteFile(filepath.Join(buildDir, "dynatrace/oneagent/dynatrace-env.sh"), []byte("echo running dynatrace-env.sh"), 0644)
			Expect(err).To(BeNil())

			manifestJson := `
			{
				"version" : "1.130.0.20170914-153344",
				"technologies" : {
					"process" : {
						"linux-x86-64" : [ {
							"path" : "agent/conf/runtime/default/process/binary_linux-x86-64",
							"md5" : "e086f9c70b53cd456988ff5c4d414f36",
							"version" : "1.130.0.20170914-125024"
						  }, {
							"path" : "agent/lib64/liboneagentproc.so",
							"md5" : "2bf4ba9e90e2589428f6f6f3a964cba2",
							"version" : "1.130.0.20170914-125024",
							"binarytype" : "primary"}]
					}
				}
			}`
			err = ioutil.WriteFile(filepath.Join(buildDir, "dynatrace/oneagent/manifest.json"), []byte(manifestJson), 0664)
			Expect(err).To(BeNil())
		}
	})

	JustBeforeEach(func() {
		args := []string{buildDir, "", depsDir, depsIdx}

		manifest, err := libbuildpack.NewManifest(bpDir, logger, time.Now())
		Expect(err).To(BeNil())

		stager = libbuildpack.NewStager(args, logger, manifest)
	})

	AfterEach(func() {
		mockCtrl.Finish()

		err = os.RemoveAll(buildDir)
		Expect(err).To(BeNil())

		err = os.RemoveAll(bpDir)
		Expect(err).To(BeNil())

		err = os.RemoveAll(depsDir)
		Expect(err).To(BeNil())
	})

	Describe("AfterCompile", func() {
		var (
			oldVcapApplication string
			oldVcapServices    string
			oldBpDebug         string

			environmentID string
			apiToken      string
		)
		BeforeEach(func() {
			oldVcapApplication = os.Getenv("VCAP_APPLICATION")
			oldVcapServices = os.Getenv("VCAP_SERVICES")
			oldBpDebug = os.Getenv("BP_DEBUG")
			environmentID = "123456"
			apiToken = "ExcitingToken28"
		})
		AfterEach(func() {
			os.Setenv("VCAP_APPLICATION", oldVcapApplication)
			os.Setenv("VCAP_SERVICES", oldVcapServices)
			os.Setenv("BP_DEBUG", oldBpDebug)
		})

		Context("VCAP_SERVICES is empty", func() {
			BeforeEach(func() {
				os.Setenv("VCAP_APPLICATION", `{"name":"JimBob"}`)
				os.Setenv("VCAP_SERVICES", "{}")
			})

			It("does nothing and succeeds", func() {
				err = hook.AfterCompile(stager)
				Expect(err).To(BeNil())

				Expect(buffer.String()).To(Equal(""))
			})
		})

		Context("VCAP_SERVICES has non dynatrace services", func() {
			BeforeEach(func() {
				os.Setenv("VCAP_APPLICATION", `{"name":"JimBob"}`)
				os.Setenv("VCAP_SERVICES", `{
					"0": [{"name":"mysql"}],
					"1": [{"name":"redis"}]
				}`)
			})

			It("does nothing and succeeds", func() {
				err = hook.AfterCompile(stager)
				Expect(err).To(BeNil())

				Expect(buffer.String()).To(Equal(""))
			})
		})

		Context("VCAP_SERVICES has incomplete dynatrace service", func() {
			BeforeEach(func() {
				os.Setenv("VCAP_APPLICATION", `{"name":"JimBob"}`)
				os.Setenv("VCAP_SERVICES", `{
					"0": [{"name":"dynatrace","credentials":{"apiurl":"https://example.com","environmentid":"`+environmentID+`"}}],
				}`)
			})

			It("does nothing and succeeds", func() {
				err = hook.AfterCompile(stager)
				Expect(err).To(BeNil())

				Expect(buffer.String()).To(Equal(""))
			})
		})

		Context("VCAP_SERVICES contains malformed dynatrace service", func() {
			BeforeEach(func() {
				os.Setenv("VCAP_APPLICATION", `{"name":"JimBob"}`)
				os.Setenv("VCAP_SERVICES", `{
					"0": [{"name":"mysql"}],
					"1": [{"name":"dynatrace","credentials":{"apiurl":"https://example.com","apitoken":"`+apiToken+`","environmentid":{ "id":"`+environmentID+`"}}}],
					"2": [{"name":"redis"}]
				}`)
			})

			It("does nothing and succeeds", func() {
				err = hook.AfterCompile(stager)
				Expect(err).To(BeNil())

				Expect(buffer.String()).Should(ContainSubstring("Incomplete credentials for service"))
			})
		})

		Context("VCAP_SERVICES contains dynatrace service without credentials", func() {
			BeforeEach(func() {
				os.Setenv("VCAP_APPLICATION", `{"name":"JimBob"}`)
				os.Setenv("VCAP_SERVICES", `{
					"0": [{"name":"mysql"}],
					"1": [{"name":"dynatrace"}],
					"2": [{"name":"redis"}]
				}`)
			})

			It("does nothing and succeeds", func() {
				err = hook.AfterCompile(stager)
				Expect(err).To(BeNil())

				Expect(buffer.String()).To(Equal(""))
			})
		})

		Context("VCAP_SERVICES contains dynatrace service using apiurl", func() {
			BeforeEach(func() {
				os.Setenv("VCAP_APPLICATION", `{"name":"JimBob"}`)
				os.Setenv("VCAP_SERVICES", `{
					"0": [{"name":"mysql"}],
					"1": [{"name":"dynatrace","credentials":{"apiurl":"https://example.com","apitoken":"`+apiToken+`","environmentid":"`+environmentID+`"}}],
					"2": [{"name":"redis"}]
				}`)

				httpmock.RegisterResponder("GET", "https://example.com/v1/deployment/installer/agent/unix/paas-sh/latest?Api-Token="+apiToken+"&bitness=64&include=nginx&include=process&include=dotnet",
					httpmock.NewStringResponder(200, "echo Install Dynatrace"))
			})

			It("installs dynatrace", func() {
				mockCommand.EXPECT().Execute("", gomock.Any(), gomock.Any(), gomock.Any(), buildDir).Do(runInstaller)

				err = hook.AfterCompile(stager)
				Expect(err).To(BeNil())

				// Sets up profile.d
				contents, err := ioutil.ReadFile(filepath.Join(depsDir, depsIdx, "profile.d", "dynatrace-env.sh"))
				Expect(err).To(BeNil())

				Expect(string(contents)).To(Equal(`echo running dynatrace-env.sh
export LD_PRELOAD=${HOME}/dynatrace/oneagent/agent/lib64/liboneagentproc.so
export DT_LOGSTREAM=stdout
export DT_CUSTOM_PROP="${DT_CUSTOM_PROP} CloudFoundryBuildpackLanguage=test42 CloudFoundryBuildpackVersion=1.2.3"`))
			})
		})

		Context("VCAP_SERVICES contains dynatrace service using apiurl and has DT_LOGSTREAM set to stderr", func() {
			BeforeEach(func() {
				os.Setenv("DT_LOGSTREAM", "stderr")
				os.Setenv("VCAP_APPLICATION", `{"name":"JimBob"}`)
				os.Setenv("VCAP_SERVICES", `{
					"0": [{"name":"mysql"}],
					"1": [{"name":"dynatrace","credentials":{"apiurl":"https://example.com","apitoken":"`+apiToken+`","environmentid":"`+environmentID+`"}}],
					"2": [{"name":"redis"}]
				}`)

				httpmock.RegisterResponder("GET", "https://example.com/v1/deployment/installer/agent/unix/paas-sh/latest?Api-Token="+apiToken+"&bitness=64&include=nginx&include=process&include=dotnet",
					httpmock.NewStringResponder(200, "echo Install Dynatrace"))
			})

			It("installs dynatrace", func() {
				mockCommand.EXPECT().Execute("", gomock.Any(), gomock.Any(), gomock.Any(), buildDir).Do(runInstaller)

				err = hook.AfterCompile(stager)
				Expect(err).To(BeNil())

				// Sets up profile.d
				contents, err := ioutil.ReadFile(filepath.Join(depsDir, depsIdx, "profile.d", "dynatrace-env.sh"))
				Expect(err).To(BeNil())

				Expect(string(contents)).To(Equal(`echo running dynatrace-env.sh
export LD_PRELOAD=${HOME}/dynatrace/oneagent/agent/lib64/liboneagentproc.so
export DT_CUSTOM_PROP="${DT_CUSTOM_PROP} CloudFoundryBuildpackLanguage=test42 CloudFoundryBuildpackVersion=1.2.3"`))
			})
		})

		Context("VCAP_SERVICES contains dynatrace service using apiurl and has DT_LOGSTREAM not set", func() {
			BeforeEach(func() {
				os.Setenv("VCAP_APPLICATION", `{"name":"JimBob"}`)
				os.Setenv("VCAP_SERVICES", `{
					"0": [{"name":"mysql"}],
					"1": [{"name":"dynatrace","credentials":{"apiurl":"https://example.com","apitoken":"`+apiToken+`","environmentid":"`+environmentID+`"}}],
					"2": [{"name":"redis"}]
				}`)

				httpmock.RegisterResponder("GET", "https://example.com/v1/deployment/installer/agent/unix/paas-sh/latest?Api-Token="+apiToken+"&bitness=64&include=nginx&include=process&include=dotnet",
					httpmock.NewStringResponder(200, "echo Install Dynatrace"))
			})

			It("installs dynatrace", func() {
				mockCommand.EXPECT().Execute("", gomock.Any(), gomock.Any(), gomock.Any(), buildDir).Do(runInstaller)

				err = hook.AfterCompile(stager)
				Expect(err).To(BeNil())

				// Sets up profile.d
				contents, err := ioutil.ReadFile(filepath.Join(depsDir, depsIdx, "profile.d", "dynatrace-env.sh"))
				Expect(err).To(BeNil())

				Expect(string(contents)).To(Equal(`echo running dynatrace-env.sh
export LD_PRELOAD=${HOME}/dynatrace/oneagent/agent/lib64/liboneagentproc.so
export DT_LOGSTREAM=stdout
export DT_CUSTOM_PROP="${DT_CUSTOM_PROP} CloudFoundryBuildpackLanguage=test42 CloudFoundryBuildpackVersion=1.2.3"`))
			})
		})

		Context("VCAP_SERVICES contains dynatrace service using apiurl and VERSION is not available", func() {
			BeforeEach(func() {
				os.Setenv("VCAP_APPLICATION", `{"name":"JimBob"}`)
				os.Setenv("VCAP_SERVICES", `{
					"0": [{"name":"mysql"}],
					"1": [{"name":"dynatrace","credentials":{"apiurl":"https://example.com","apitoken":"`+apiToken+`","environmentid":"`+environmentID+`"}}],
					"2": [{"name":"redis"}]
				}`)

				httpmock.RegisterResponder("GET", "https://example.com/v1/deployment/installer/agent/unix/paas-sh/latest?Api-Token="+apiToken+"&bitness=64&include=nginx&include=process&include=dotnet",
					httpmock.NewStringResponder(200, "echo Install Dynatrace"))

				os.Remove(filepath.Join(bpDir, "VERSION"))
			})

			It("installs dynatrace", func() {
				mockCommand.EXPECT().Execute("", gomock.Any(), gomock.Any(), gomock.Any(), buildDir).Do(runInstaller)

				err = hook.AfterCompile(stager)
				Expect(err).To(BeNil())

				// Sets up profile.d
				contents, err := ioutil.ReadFile(filepath.Join(depsDir, depsIdx, "profile.d", "dynatrace-env.sh"))
				Expect(err).To(BeNil())

				Expect(string(contents)).To(Equal(`echo running dynatrace-env.sh
export LD_PRELOAD=${HOME}/dynatrace/oneagent/agent/lib64/liboneagentproc.so
export DT_LOGSTREAM=stdout
export DT_CUSTOM_PROP="${DT_CUSTOM_PROP} CloudFoundryBuildpackLanguage=test42 CloudFoundryBuildpackVersion=unknown"`))
			})
		})

		Context("VCAP_SERVICES contains dynatrace service using environmentid redis service and mixed-case service name", func() {
			BeforeEach(func() {
				os.Setenv("VCAP_APPLICATION", `{"name":"JimBob"}`)
				os.Setenv("VCAP_SERVICES", `{
					"0": [{"name":"mysql"}],
					"1": [{"name":"dyNaTRace","credentials":{"environmentid":"`+environmentID+`","apitoken":"`+apiToken+`"}}],
					"2": [{"name":"redis", "credentials":{"db_type":"redis", "instance_administration_api":{"deployment_id":"12345asdf", "instance_id":"12345asdf", "root":"https://doesnotexi.st"}}}]
				}`)

				httpmock.RegisterResponder("GET", "https://"+environmentID+".live.dynatrace.com/api/v1/deployment/installer/agent/unix/paas-sh/latest?Api-Token="+apiToken+"&bitness=64&include=nginx&include=process&include=dotnet",
					httpmock.NewStringResponder(200, "echo Install Dynatrace"))
			})

			It("installs dynatrace", func() {
				mockCommand.EXPECT().Execute("", gomock.Any(), gomock.Any(), gomock.Any(), buildDir).Do(runInstaller)

				err = hook.AfterCompile(stager)
				Expect(err).To(BeNil())

				// Sets up profile.d
				contents, err := ioutil.ReadFile(filepath.Join(depsDir, depsIdx, "profile.d", "dynatrace-env.sh"))
				Expect(err).To(BeNil())

				Expect(string(contents)).To(Equal(`echo running dynatrace-env.sh
export LD_PRELOAD=${HOME}/dynatrace/oneagent/agent/lib64/liboneagentproc.so
export DT_LOGSTREAM=stdout
export DT_CUSTOM_PROP="${DT_CUSTOM_PROP} CloudFoundryBuildpackLanguage=test42 CloudFoundryBuildpackVersion=1.2.3"`))
			})
		})

		Context("VCAP_SERVICES contains dynatrace service and fails the first download", func() {
			BeforeEach(func() {
				os.Setenv("VCAP_APPLICATION", `{"name":"JimBob"}`)
				os.Setenv("VCAP_SERVICES", `{
					"0": [{"name":"mysql"}],
					"1": [{"name":"dynatrace","credentials":{"apiurl":"https://example.com","apitoken":"`+apiToken+`","environmentid":"`+environmentID+`"}}],
					"2": [{"name":"redis"}]
				}`)

				hook.MaxDownloadRetries = 1
				attempt := 0

				httpmock.RegisterResponder("GET", "https://example.com/v1/deployment/installer/agent/unix/paas-sh/latest?Api-Token="+apiToken+"&bitness=64&include=nginx&include=process&include=dotnet",
					func(req *http.Request) (*http.Response, error) {
						if attempt += 1; attempt == 1 {
							return httpmock.NewStringResponse(500, `{"error": "Server failure"}`), nil
						}
						return httpmock.NewStringResponse(200, "echo Install Dynatrace"), nil
					})
			})

			AfterEach(func() {
				hook.MaxDownloadRetries = 0
			})

			It("installs dynatrace", func() {
				mockCommand.EXPECT().Execute("", gomock.Any(), gomock.Any(), gomock.Any(), buildDir).Do(runInstaller)

				err = hook.AfterCompile(stager)
				Expect(err).To(BeNil())

				// Sets up profile.d
				contents, err := ioutil.ReadFile(filepath.Join(depsDir, depsIdx, "profile.d", "dynatrace-env.sh"))
				Expect(err).To(BeNil())

				Expect(string(contents)).To(Equal(`echo running dynatrace-env.sh
export LD_PRELOAD=${HOME}/dynatrace/oneagent/agent/lib64/liboneagentproc.so
export DT_LOGSTREAM=stdout
export DT_CUSTOM_PROP="${DT_CUSTOM_PROP} CloudFoundryBuildpackLanguage=test42 CloudFoundryBuildpackVersion=1.2.3"`))
			})
		})

		Context("VCAP_SERVICES contains second dynatrace service with credentials", func() {
			BeforeEach(func() {
				os.Setenv("VCAP_APPLICATION", `{"name":"JimBob"}`)
				os.Setenv("VCAP_SERVICES", `{
					"0": [{"name":"dynatrace","credentials":{"environmentid":"`+environmentID+`","apitoken":"`+apiToken+`"}}],
					"1": [{"name":"dynatrace-dupe","credentials":{"environmentid":"`+environmentID+`","apitoken":"`+apiToken+`"}}]
				}`)
			})

			It("does nothing and succeeds", func() {
				err = hook.AfterCompile(stager)
				Expect(err).To(BeNil())

				Expect(buffer.String()).To(ContainSubstring("More than one matching service found!"))
			})
		})

		Context("VCAP_SERVICES contains dynatrace service with location", func() {
			BeforeEach(func() {
				os.Setenv("VCAP_APPLICATION", `{"name":"JimBob"}`)
				os.Setenv("VCAP_SERVICES", `{
					"0": [{"name":"dynatrace","credentials":{"apitoken":"`+apiToken+`","environmentid":"`+environmentID+`","location":"west-us"}}]
				}`)

				httpmock.RegisterResponder("GET", "https://"+environmentID+".live.dynatrace.com/api/v1/deployment/installer/agent/unix/paas-sh/latest?Api-Token="+apiToken+"&bitness=64&include=nginx&include=process&include=dotnet",
					httpmock.NewStringResponder(200, "echo Install Dynatrace"))
			})

			It("installs dynatrace", func() {
				mockCommand.EXPECT().Execute("", gomock.Any(), gomock.Any(), gomock.Any(), buildDir).Do(runInstaller)

				Expect(hook.AfterCompile(stager)).Should(Succeed())

				// Sets up profile.d
				contents, err := ioutil.ReadFile(filepath.Join(depsDir, depsIdx, "profile.d", "dynatrace-env.sh"))
				Expect(err).Should(Succeed())

				Expect(string(contents)).To(Equal(`echo running dynatrace-env.sh
export LD_PRELOAD=${HOME}/dynatrace/oneagent/agent/lib64/liboneagentproc.so
export DT_NETWORK_ZONE=${DT_NETWORK_ZONE:-west-us}
export DT_LOGSTREAM=stdout
export DT_CUSTOM_PROP="${DT_CUSTOM_PROP} CloudFoundryBuildpackLanguage=test42 CloudFoundryBuildpackVersion=1.2.3"`))
			})
		})

		Context("VCAP_SERVICES contains skiperrors flag", func() {
			BeforeEach(func() {
				os.Setenv("BP_DEBUG", "true")
				os.Setenv("VCAP_APPLICATION", `{"name":"JimBob"}`)
				os.Setenv("VCAP_SERVICES", `{
					"0": [{"name":"dynatrace","credentials":{"environmentid":"`+environmentID+`","apitoken":"`+apiToken+`","skiperrors":"true"}}]
				}`)

				httpmock.RegisterResponder("GET", "https://"+environmentID+".live.dynatrace.com/api/v1/deployment/installer/agent/unix/paas-sh/latest?Api-Token="+apiToken+"&bitness=64&include=nginx&include=process&include=dotnet",
					httpmock.NewStringResponder(404, "echo agent not found"))
			})

			It("does nothing and succeeds", func() {
				err = hook.AfterCompile(stager)
				Expect(err).To(BeNil())

				Expect(buffer.String()).To(ContainSubstring("Download returned with status 404"))
				Expect(buffer.String()).To(ContainSubstring("Error during installer download, skipping installation"))
			})
		})
	})
})

func TestPackage(t *testing.T) {
	httpmock.Activate()
	defer httpmock.DeactivateAndReset()

	RegisterFailHandler(Fail)
	RunSpecs(t, "DynatraceCloudfoundryBuildpackIntegration Suite")
}
