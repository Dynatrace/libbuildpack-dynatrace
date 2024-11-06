package dynatrace_test

import (
	"bytes"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	dynatrace "github.com/Dynatrace/libbuildpack-dynatrace"
	"github.com/cloudfoundry/libbuildpack"
	"github.com/golang/mock/gomock"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	"github.com/jarcoal/httpmock"
)

const manifestJson = `{
	"version" : "1.130.0.20170914-153344",
	"technologies" : {
		"process" : {
			"linux-x86-64" : [ 
				{
					"path" : "agent/conf/runtime/default/process/binary_linux-x86-64",
					"md5" : "e086f9c70b53cd456988ff5c4d414f36",
					"version" : "1.130.0.20170914-125024"
				}, 
				{
					"path" : "agent/lib64/liboneagentproc.so",
					"md5" : "2bf4ba9e90e2589428f6f6f3a964cba2",
					"version" : "1.130.0.20170914-125024",
					"binarytype" : "primary"
				}
			],
			"windows-x86-64" : [
				{
					"path" : "agent/conf/runtime/default/process/windows_linux-x86-64",
					"md5" : "e086f9c70b53cd456988ff5c4d414f36",
					"version" : "1.130.0.20170914-125024"
				},  
				{
					"path" : "agent/lib64/oneagentloader.dll",
					"md5" : "2bf4ba9e90e2589428f6f6f3a964cba2",
					"version" : "1.130.0.20170914-125024",
					"binarytype" : "loader"
				}
			]
		}
	}
}`

//go:generate mockgen -source=hook.go --destination=mocks_test.go --package=dynatrace_test

var _ = Describe("dynatraceHook", func() {
	var (
		err                   error
		bpDir                 string
		buildDir              string
		depsDir               string
		depsIdx               string
		logger                *libbuildpack.Logger
		stager                *libbuildpack.Stager
		mockCtrl              *gomock.Controller
		mockCommand           *MockCommand
		buffer                *bytes.Buffer
		hook                  dynatrace.Hook
		simulateUnixInstaller func(string, io.Writer, io.Writer, string, string)
		api_header_check      func(req *http.Request) (*http.Response, error)
	)

	BeforeEach(func() {
		bpDir, err = os.MkdirTemp("", "libbuildpack-dynatrace.buildpack.")
		Expect(err).To(BeNil())

		buildDir, err = os.MkdirTemp("", "libbuildpack-dynatrace.build.")
		Expect(err).To(BeNil())

		depsDir, err = os.MkdirTemp("", "libbuildpack-dynatrace.deps.")
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

		api_header_check = func(req *http.Request) (*http.Response, error) {
			resp_header := req.Header.Get("Authorization")
			if resp_header == "" {
				return httpmock.NewStringResponse(500, `{"error": "No Authorization Header found"}`), nil
			}
			if strings.Index(resp_header, "Api-Token") == -1 {
				return httpmock.NewStringResponse(500, `{"error": "No Api-Token found in Authorization Header"}`), nil
			}

			resp := getMockResponse()

			return resp, nil
		}

		os.Setenv("DT_LOGSTREAM", "")

		os.WriteFile(filepath.Join(bpDir, "manifest.yml"), []byte("---\nlanguage: test42\n"), 0755)
		os.WriteFile(filepath.Join(bpDir, "VERSION"), []byte("1.2.3"), 0755)

		httpmock.Reset()

		simulateUnixInstaller = func(_ string, _, _ io.Writer, file string, _ string) {
			contents, err := os.ReadFile(file)
			Expect(err).To(BeNil())

			Expect(string(contents)).To(Equal("echo Install Dynatrace"))

			err = os.MkdirAll(filepath.Join(buildDir, "dynatrace/oneagent/agent/lib64"), 0755)
			Expect(err).To(BeNil())

			err = os.WriteFile(filepath.Join(buildDir, "dynatrace/oneagent/agent/lib64/liboneagentproc.so"), []byte("library"), 0644)
			Expect(err).To(BeNil())

			err = os.WriteFile(filepath.Join(buildDir, "dynatrace/oneagent/dynatrace-env.sh"), []byte("echo running dynatrace-env.sh"), 0644)
			Expect(err).To(BeNil())

			err = os.WriteFile(filepath.Join(buildDir, "dynatrace/oneagent/manifest.json"), []byte(manifestJson), 0664)
			Expect(err).To(BeNil())

			ruxitagentproc := `
			[section1]
			key1=val1
			key2=val2

			[section2]
			key3=val3
			key4=val4`

			err = os.MkdirAll(filepath.Join(buildDir, "dynatrace/oneagent/agent/conf"), 0755)
			Expect(err).To(BeNil())

			err = os.WriteFile(filepath.Join(buildDir, "dynatrace/oneagent/agent/conf/ruxitagentproc.conf"), []byte(ruxitagentproc), 0664)
			Expect(err).To(BeNil())

			err = os.WriteFile(filepath.Join(buildDir, "dynatrace/oneagent/agent/dt_fips_disabled.flag"), []byte(""), 0664)
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
				os.Setenv("BP_DEBUG", "true")
				os.Setenv("VCAP_APPLICATION", `{"name":"JimBob"}`)
				os.Setenv("VCAP_SERVICES", `{
					"0": [{"name":"mysql"}],
					"1": [{"name":"dynatrace","credentials":{"apiurl":"https://example.com","apitoken":"`+apiToken+`","environmentid":"`+environmentID+`"}}],
					"2": [{"name":"redis"}]
				}`)

				httpmock.RegisterResponder("GET", "https://example.com/v1/deployment/installer/agent/"+OSName+"/"+InstallationMethod+"/latest?bitness=64&include=nginx&include=process&include=dotnet",
					api_header_check)

				httpmock.RegisterResponder("GET", "https://example.com/v1/deployment/installer/agent/processmoduleconfig",
					api_header_check)

			})

			It("installs dynatrace", func() {
				if runtime.GOOS != "windows" {
					mockCommand.EXPECT().Execute("", gomock.Any(), gomock.Any(), gomock.Any(), buildDir).Do(simulateUnixInstaller)
				}

				err = hook.AfterCompile(stager)
				Expect(err).To(BeNil())

				// Sets up profile.d
				contents, err := os.ReadFile(filepath.Join(depsDir, depsIdx, "profile.d", ScriptFilename))
				Expect(err).To(BeNil())

				if runtime.GOOS == "windows" {
					Expect(string(contents)).To(Equal(`set COR_ENABLE_PROFILING=1
set COR_PROFILER={B7038F67-52FC-4DA2-AB02-969B3C1EDA03}
set DT_AGENTACTIVE=true
set DT_BLOCKLIST=powershell*
set COR_PROFILER_PATH_64=C:\users\vcap\app\dynatrace\oneagent\agent\lib64\oneagentloader.dll
set DT_CUSTOM_PROP="%DT_CUSTOM_PROP% CloudFoundryBuildpackLanguage=test42 CloudFoundryBuildpackVersion=1.2.3"
`))
				} else {
					Expect(string(contents)).To(Equal(`echo running dynatrace-env.sh
export LD_PRELOAD=${HOME}/dynatrace/oneagent/agent/lib64/liboneagentproc.so
export DT_LOGSTREAM=stdout
export DT_CUSTOM_PROP="${DT_CUSTOM_PROP} CloudFoundryBuildpackLanguage=test42 CloudFoundryBuildpackVersion=1.2.3"`))
				}
			})
		})

		Context("VCAP_SERVICES contains dynatrace service with customoneagenturl", func() {
			BeforeEach(func() {
				os.Setenv("BP_DEBUG", "true")
				os.Setenv("VCAP_APPLICATION", `{"name":"JimBob"}`)
				os.Setenv("VCAP_SERVICES", `{
					"0": [{"name":"mysql"}],
					"1": [{"name":"dynatrace","credentials":{"apiurl":"https://example.com","apitoken":"`+apiToken+`","environmentid":"`+environmentID+`","customoneagenturl":"https://example.com/oneagent"}}],
					"2": [{"name":"redis"}]
				}`)

				httpmock.RegisterResponder("GET", "https://example.com/oneagent", func(r *http.Request) (*http.Response, error) {
					return getMockResponse(), nil
				})

				httpmock.RegisterResponder("GET", "https://example.com/v1/deployment/installer/agent/processmoduleconfig",
					api_header_check)

			})

			It("installs dynatrace", func() {
				if runtime.GOOS != "windows" {
					mockCommand.EXPECT().Execute("", gomock.Any(), gomock.Any(), gomock.Any(), buildDir).Do(simulateUnixInstaller)
				}

				err = hook.AfterCompile(stager)
				Expect(err).To(BeNil())

				// Sets up profile.d
				contents, err := os.ReadFile(filepath.Join(depsDir, depsIdx, "profile.d", ScriptFilename))
				Expect(err).To(BeNil())

				if runtime.GOOS == "windows" {
					Expect(string(contents)).To(Equal(`set COR_ENABLE_PROFILING=1
set COR_PROFILER={B7038F67-52FC-4DA2-AB02-969B3C1EDA03}
set DT_AGENTACTIVE=true
set DT_BLOCKLIST=powershell*
set COR_PROFILER_PATH_64=C:\users\vcap\app\dynatrace\oneagent\agent\lib64\oneagentloader.dll
set DT_CUSTOM_PROP="%DT_CUSTOM_PROP% CloudFoundryBuildpackLanguage=test42 CloudFoundryBuildpackVersion=1.2.3"
`))
				} else {
					Expect(string(contents)).To(Equal(`echo running dynatrace-env.sh
export LD_PRELOAD=${HOME}/dynatrace/oneagent/agent/lib64/liboneagentproc.so
export DT_LOGSTREAM=stdout
export DT_CUSTOM_PROP="${DT_CUSTOM_PROP} CloudFoundryBuildpackLanguage=test42 CloudFoundryBuildpackVersion=1.2.3"`))
				}
			})
		})

		Context("Agent config can't be fetched from the API", func() {
			BeforeEach(func() {
				os.Setenv("VCAP_APPLICATION", `{"name":"JimBob"}`)
				os.Setenv("VCAP_SERVICES", `{
					"0": [{"name":"mysql"}],
					"1": [{"name":"dynatrace","credentials":{"apiurl":"https://example.com","apitoken":"`+apiToken+`","environmentid":"`+environmentID+`"}}],
					"2": [{"name":"redis"}]
				}`)

				httpmock.RegisterResponder("GET", "https://example.com/v1/deployment/installer/agent/"+OSName+"/"+InstallationMethod+"/latest?bitness=64&include=nginx&include=process&include=dotnet",
					api_header_check)

				httpmock.RegisterResponder("GET", "https://example.com/v1/deployment/installer/agent/processmoduleconfig",
					httpmock.NewStringResponder(404, "echo config not found"))
			})

			It("installs dynatrace and writes comment to uxitagentproc.conf", func() {
				if runtime.GOOS != "windows" {
					mockCommand.EXPECT().Execute("", gomock.Any(), gomock.Any(), gomock.Any(), buildDir).Do(simulateUnixInstaller)
				}

				err = hook.AfterCompile(stager)
				Expect(err).To(BeNil())

				Expect(buffer.String()).To(ContainSubstring("Failed to fetch updated OneAgent config from the API"))

				// Check for comment in ruxitagentproc.conf
				contents, err := os.ReadFile(filepath.Join(buildDir, "dynatrace/oneagent/agent/conf/ruxitagentproc.conf"))
				Expect(err).To(BeNil())

				warn_string := "# Warning: Failed to fetch updated OneAgent config from the API. This config only includes settings provided by the installer."
				Expect(strings.Contains(string(contents), warn_string)).To(BeTrue())

				// Sets up profile.d
				contents, err = os.ReadFile(filepath.Join(depsDir, depsIdx, "profile.d", ScriptFilename))
				Expect(err).To(BeNil())

				if runtime.GOOS == "windows" {
					Expect(string(contents)).To(Equal(`set COR_ENABLE_PROFILING=1
set COR_PROFILER={B7038F67-52FC-4DA2-AB02-969B3C1EDA03}
set DT_AGENTACTIVE=true
set DT_BLOCKLIST=powershell*
set COR_PROFILER_PATH_64=C:\users\vcap\app\dynatrace\oneagent\agent\lib64\oneagentloader.dll
set DT_CUSTOM_PROP="%DT_CUSTOM_PROP% CloudFoundryBuildpackLanguage=test42 CloudFoundryBuildpackVersion=1.2.3"
`))
				} else {
					Expect(string(contents)).To(Equal(`echo running dynatrace-env.sh
export LD_PRELOAD=${HOME}/dynatrace/oneagent/agent/lib64/liboneagentproc.so
export DT_LOGSTREAM=stdout
export DT_CUSTOM_PROP="${DT_CUSTOM_PROP} CloudFoundryBuildpackLanguage=test42 CloudFoundryBuildpackVersion=1.2.3"`))
				}
			})
		})

		Context("Agent config can be fetched from the API", func() {
			BeforeEach(func() {
				os.Setenv("BP_DEBUG", "true")
				os.Setenv("VCAP_APPLICATION", `{"name":"JimBob"}`)
				os.Setenv("VCAP_SERVICES", `{
					"0": [{"name":"mysql"}],
					"1": [{"name":"dynatrace","credentials":{"apiurl":"https://example.com","apitoken":"`+apiToken+`","environmentid":"`+environmentID+`"}}],
					"2": [{"name":"redis"}]
				}`)

				httpmock.RegisterResponder("GET", "https://example.com/v1/deployment/installer/agent/"+OSName+"/"+InstallationMethod+"/latest?bitness=64&include=nginx&include=process&include=dotnet",
					api_header_check)

				httpmock.RegisterResponder("GET", "https://example.com/v1/deployment/installer/agent/processmoduleconfig",
					api_header_check)
			})

			It("installs dynatrace", func() {

				if runtime.GOOS != "windows" {
					mockCommand.EXPECT().Execute("", gomock.Any(), gomock.Any(), gomock.Any(), buildDir).Do(simulateUnixInstaller)
				}

				err = hook.AfterCompile(stager)
				Expect(err).To(BeNil())

				Expect(buffer.String()).To(ContainSubstring("Successfully fetched updated OneAgent config from the API"))

				// Check for comment in ruxitagentproc.conf
				contents, err := os.ReadFile(filepath.Join(buildDir, "dynatrace", "oneagent", "agent", "conf", "ruxitagentproc.conf"))
				Expect(err).To(BeNil())
				configComment := "# This config is a merge between the installer and the Cluster config"
				Expect(strings.Contains(string(contents), configComment)).To(BeTrue())

				// Sets up profile.d
				contents, err = os.ReadFile(filepath.Join(depsDir, depsIdx, "profile.d", ScriptFilename))
				Expect(err).To(BeNil())

				if runtime.GOOS == "windows" {
					Expect(string(contents)).To(Equal(`set COR_ENABLE_PROFILING=1
set COR_PROFILER={B7038F67-52FC-4DA2-AB02-969B3C1EDA03}
set DT_AGENTACTIVE=true
set DT_BLOCKLIST=powershell*
set COR_PROFILER_PATH_64=C:\users\vcap\app\dynatrace\oneagent\agent\lib64\oneagentloader.dll
set DT_CUSTOM_PROP="%DT_CUSTOM_PROP% CloudFoundryBuildpackLanguage=test42 CloudFoundryBuildpackVersion=1.2.3"
`))
				} else {
					Expect(string(contents)).To(Equal(`echo running dynatrace-env.sh
export LD_PRELOAD=${HOME}/dynatrace/oneagent/agent/lib64/liboneagentproc.so
export DT_LOGSTREAM=stdout
export DT_CUSTOM_PROP="${DT_CUSTOM_PROP} CloudFoundryBuildpackLanguage=test42 CloudFoundryBuildpackVersion=1.2.3"`))
				}
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

				httpmock.RegisterResponder("GET", "https://example.com/v1/deployment/installer/agent/"+OSName+"/"+InstallationMethod+"/latest?bitness=64&include=nginx&include=process&include=dotnet",
					api_header_check)
			})

			It("installs dynatrace", func() {
				if runtime.GOOS != "windows" {
					mockCommand.EXPECT().Execute("", gomock.Any(), gomock.Any(), gomock.Any(), buildDir).Do(simulateUnixInstaller)
				}

				err = hook.AfterCompile(stager)
				Expect(err).To(BeNil())

				// Sets up profile.d
				contents, err := os.ReadFile(filepath.Join(depsDir, depsIdx, "profile.d", ScriptFilename))
				Expect(err).To(BeNil())

				if runtime.GOOS == "windows" {
					Expect(string(contents)).To(Equal(`set COR_ENABLE_PROFILING=1
set COR_PROFILER={B7038F67-52FC-4DA2-AB02-969B3C1EDA03}
set DT_AGENTACTIVE=true
set DT_BLOCKLIST=powershell*
set COR_PROFILER_PATH_64=C:\users\vcap\app\dynatrace\oneagent\agent\lib64\oneagentloader.dll
set DT_CUSTOM_PROP="%DT_CUSTOM_PROP% CloudFoundryBuildpackLanguage=test42 CloudFoundryBuildpackVersion=1.2.3"
`))
				} else {
					Expect(string(contents)).To(Equal(`echo running dynatrace-env.sh
export LD_PRELOAD=${HOME}/dynatrace/oneagent/agent/lib64/liboneagentproc.so
export DT_CUSTOM_PROP="${DT_CUSTOM_PROP} CloudFoundryBuildpackLanguage=test42 CloudFoundryBuildpackVersion=1.2.3"`))
				}

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

				httpmock.RegisterResponder("GET", "https://example.com/v1/deployment/installer/agent/"+OSName+"/"+InstallationMethod+"/latest?bitness=64&include=nginx&include=process&include=dotnet",
					api_header_check)
			})

			It("installs dynatrace", func() {
				if runtime.GOOS != "windows" {
					mockCommand.EXPECT().Execute("", gomock.Any(), gomock.Any(), gomock.Any(), buildDir).Do(simulateUnixInstaller)
				}

				err = hook.AfterCompile(stager)
				Expect(err).To(BeNil())

				// Sets up profile.d
				contents, err := os.ReadFile(filepath.Join(depsDir, depsIdx, "profile.d", ScriptFilename))
				Expect(err).To(BeNil())

				if runtime.GOOS == "windows" {
					Expect(string(contents)).To(Equal(`set COR_ENABLE_PROFILING=1
set COR_PROFILER={B7038F67-52FC-4DA2-AB02-969B3C1EDA03}
set DT_AGENTACTIVE=true
set DT_BLOCKLIST=powershell*
set COR_PROFILER_PATH_64=C:\users\vcap\app\dynatrace\oneagent\agent\lib64\oneagentloader.dll
set DT_CUSTOM_PROP="%DT_CUSTOM_PROP% CloudFoundryBuildpackLanguage=test42 CloudFoundryBuildpackVersion=1.2.3"
`))
				} else {
					Expect(string(contents)).To(Equal(`echo running dynatrace-env.sh
export LD_PRELOAD=${HOME}/dynatrace/oneagent/agent/lib64/liboneagentproc.so
export DT_LOGSTREAM=stdout
export DT_CUSTOM_PROP="${DT_CUSTOM_PROP} CloudFoundryBuildpackLanguage=test42 CloudFoundryBuildpackVersion=1.2.3"`))
				}

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

				httpmock.RegisterResponder("GET", "https://example.com/v1/deployment/installer/agent/"+OSName+"/"+InstallationMethod+"/latest?bitness=64&include=nginx&include=process&include=dotnet",
					api_header_check)

				os.Remove(filepath.Join(bpDir, "VERSION"))
			})

			It("installs dynatrace", func() {
				if runtime.GOOS != "windows" {
					mockCommand.EXPECT().Execute("", gomock.Any(), gomock.Any(), gomock.Any(), buildDir).Do(simulateUnixInstaller)
				}

				err = hook.AfterCompile(stager)
				Expect(err).To(BeNil())

				// Sets up profile.d
				contents, err := os.ReadFile(filepath.Join(depsDir, depsIdx, "profile.d", ScriptFilename))
				Expect(err).To(BeNil())

				if runtime.GOOS == "windows" {
					Expect(string(contents)).To(Equal(`set COR_ENABLE_PROFILING=1
set COR_PROFILER={B7038F67-52FC-4DA2-AB02-969B3C1EDA03}
set DT_AGENTACTIVE=true
set DT_BLOCKLIST=powershell*
set COR_PROFILER_PATH_64=C:\users\vcap\app\dynatrace\oneagent\agent\lib64\oneagentloader.dll
set DT_CUSTOM_PROP="%DT_CUSTOM_PROP% CloudFoundryBuildpackLanguage=test42 CloudFoundryBuildpackVersion=unknown"
`))
				} else {
					Expect(string(contents)).To(Equal(`echo running dynatrace-env.sh
export LD_PRELOAD=${HOME}/dynatrace/oneagent/agent/lib64/liboneagentproc.so
export DT_LOGSTREAM=stdout
export DT_CUSTOM_PROP="${DT_CUSTOM_PROP} CloudFoundryBuildpackLanguage=test42 CloudFoundryBuildpackVersion=unknown"`))
				}

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

				httpmock.RegisterResponder("GET", "https://"+environmentID+".live.dynatrace.com/api/v1/deployment/installer/agent/"+OSName+"/"+InstallationMethod+"/latest?bitness=64&include=nginx&include=process&include=dotnet",
					api_header_check)
			})

			It("installs dynatrace", func() {
				if runtime.GOOS != "windows" {
					mockCommand.EXPECT().Execute("", gomock.Any(), gomock.Any(), gomock.Any(), buildDir).Do(simulateUnixInstaller)
				}

				err = hook.AfterCompile(stager)
				Expect(err).To(BeNil())

				// Sets up profile.d
				contents, err := os.ReadFile(filepath.Join(depsDir, depsIdx, "profile.d", ScriptFilename))
				Expect(err).To(BeNil())

				if runtime.GOOS == "windows" {
					Expect(string(contents)).To(Equal(`set COR_ENABLE_PROFILING=1
set COR_PROFILER={B7038F67-52FC-4DA2-AB02-969B3C1EDA03}
set DT_AGENTACTIVE=true
set DT_BLOCKLIST=powershell*
set COR_PROFILER_PATH_64=C:\users\vcap\app\dynatrace\oneagent\agent\lib64\oneagentloader.dll
set DT_CUSTOM_PROP="%DT_CUSTOM_PROP% CloudFoundryBuildpackLanguage=test42 CloudFoundryBuildpackVersion=1.2.3"
`))
				} else {
					Expect(string(contents)).To(Equal(`echo running dynatrace-env.sh
export LD_PRELOAD=${HOME}/dynatrace/oneagent/agent/lib64/liboneagentproc.so
export DT_LOGSTREAM=stdout
export DT_CUSTOM_PROP="${DT_CUSTOM_PROP} CloudFoundryBuildpackLanguage=test42 CloudFoundryBuildpackVersion=1.2.3"`))
				}

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

				httpmock.RegisterResponder("GET", "https://example.com/v1/deployment/installer/agent/"+OSName+"/"+InstallationMethod+"/latest?bitness=64&include=nginx&include=process&include=dotnet",
					func(req *http.Request) (*http.Response, error) {
						if attempt += 1; attempt == 1 {
							return httpmock.NewStringResponse(500, `{"error": "Server failure"}`), nil
						}
						return getMockResponse(), nil
					})
			})

			AfterEach(func() {
				hook.MaxDownloadRetries = 0
			})

			It("installs dynatrace", func() {
				if runtime.GOOS != "windows" {
					mockCommand.EXPECT().Execute("", gomock.Any(), gomock.Any(), gomock.Any(), buildDir).Do(simulateUnixInstaller)
				}

				err = hook.AfterCompile(stager)
				Expect(err).To(BeNil())

				// Sets up profile.d
				contents, err := os.ReadFile(filepath.Join(depsDir, depsIdx, "profile.d", ScriptFilename))
				Expect(err).To(BeNil())

				if runtime.GOOS == "windows" {
					Expect(string(contents)).To(Equal(`set COR_ENABLE_PROFILING=1
set COR_PROFILER={B7038F67-52FC-4DA2-AB02-969B3C1EDA03}
set DT_AGENTACTIVE=true
set DT_BLOCKLIST=powershell*
set COR_PROFILER_PATH_64=C:\users\vcap\app\dynatrace\oneagent\agent\lib64\oneagentloader.dll
set DT_CUSTOM_PROP="%DT_CUSTOM_PROP% CloudFoundryBuildpackLanguage=test42 CloudFoundryBuildpackVersion=1.2.3"
`))
				} else {
					Expect(string(contents)).To(Equal(`echo running dynatrace-env.sh
export LD_PRELOAD=${HOME}/dynatrace/oneagent/agent/lib64/liboneagentproc.so
export DT_LOGSTREAM=stdout
export DT_CUSTOM_PROP="${DT_CUSTOM_PROP} CloudFoundryBuildpackLanguage=test42 CloudFoundryBuildpackVersion=1.2.3"`))
				}
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
					"0": [{"name":"dynatrace","credentials":{"apitoken":"`+apiToken+`","environmentid":"`+environmentID+`","networkzone":"west-us"}}]
				}`)

				httpmock.RegisterResponder("GET", "https://"+environmentID+".live.dynatrace.com/api/v1/deployment/installer/agent/"+OSName+"/"+InstallationMethod+"/latest?bitness=64&include=nginx&include=process&include=dotnet&networkZone=west-us",
					api_header_check)
			})

			It("installs dynatrace", func() {
				if runtime.GOOS != "windows" {
					mockCommand.EXPECT().Execute("", gomock.Any(), gomock.Any(), gomock.Any(), buildDir).Do(simulateUnixInstaller)
				}

				Expect(hook.AfterCompile(stager)).Should(Succeed())

				// Sets up profile.d
				contents, err := os.ReadFile(filepath.Join(depsDir, depsIdx, "profile.d", ScriptFilename))
				Expect(err).Should(Succeed())

				if runtime.GOOS == "windows" {
					Expect(string(contents)).To(Equal(`set COR_ENABLE_PROFILING=1
set COR_PROFILER={B7038F67-52FC-4DA2-AB02-969B3C1EDA03}
set DT_AGENTACTIVE=true
set DT_BLOCKLIST=powershell*
set COR_PROFILER_PATH_64=C:\users\vcap\app\dynatrace\oneagent\agent\lib64\oneagentloader.dll
set DT_NETWORK_ZONE=west-us
set DT_CUSTOM_PROP="%DT_CUSTOM_PROP% CloudFoundryBuildpackLanguage=test42 CloudFoundryBuildpackVersion=1.2.3"
`))
				} else {
					Expect(string(contents)).To(Equal(`echo running dynatrace-env.sh
export LD_PRELOAD=${HOME}/dynatrace/oneagent/agent/lib64/liboneagentproc.so
export DT_NETWORK_ZONE=${DT_NETWORK_ZONE:-west-us}
export DT_LOGSTREAM=stdout
export DT_CUSTOM_PROP="${DT_CUSTOM_PROP} CloudFoundryBuildpackLanguage=test42 CloudFoundryBuildpackVersion=1.2.3"`))
				}
			})
		})

		Context("VCAP_SERVICES contains skiperrors flag", func() {
			BeforeEach(func() {
				os.Setenv("BP_DEBUG", "true")
				os.Setenv("VCAP_APPLICATION", `{"name":"JimBob"}`)
				os.Setenv("VCAP_SERVICES", `{
					"0": [{"name":"dynatrace","credentials":{"environmentid":"`+environmentID+`","apitoken":"`+apiToken+`","skiperrors":"true"}}]
				}`)

				httpmock.RegisterResponder("GET", "https://"+environmentID+".live.dynatrace.com/api/v1/deployment/installer/agent/"+OSName+"/"+InstallationMethod+"/latest?bitness=64&include=nginx&include=process&include=dotnet",
					httpmock.NewStringResponder(404, "echo agent not found"))
			})

			It("does nothing and succeeds", func() {
				err = hook.AfterCompile(stager)
				Expect(err).To(BeNil())

				Expect(buffer.String()).To(ContainSubstring("Download returned with status 404"))
				Expect(buffer.String()).To(ContainSubstring("Error during installer download, skipping installation"))
			})
		})

		Context("FIPS enabled", func() {
			BeforeEach(func() {
				os.Setenv("BP_DEBUG", "true")
				os.Setenv("VCAP_APPLICATION", `{"name":"JimBob"}`)
				os.Setenv("VCAP_SERVICES", `{
					"0": [{"name":"mysql"}],
					"1": [{"name":"dynatrace","credentials":{"apiurl":"https://example.com","apitoken":"`+apiToken+`","environmentid":"`+environmentID+`","enablefips":"true"}}],
					"2": [{"name":"redis"}]
				}`)

				httpmock.RegisterResponder("GET", "https://example.com/v1/deployment/installer/agent/"+OSName+"/"+InstallationMethod+"/latest?bitness=64&include=nginx&include=process&include=dotnet",
					api_header_check)

				httpmock.RegisterResponder("GET", "https://example.com/v1/deployment/installer/agent/processmoduleconfig",
					api_header_check)
			})

			It("installs dynatrace and deletes FIPS flag file", func() {
				if runtime.GOOS != "windows" {
					mockCommand.EXPECT().Execute("", gomock.Any(), gomock.Any(), gomock.Any(), buildDir).Do(simulateUnixInstaller)
				}

				err = hook.AfterCompile(stager)
				Expect(err).To(BeNil())

				_, err := os.Stat(filepath.Join(buildDir, "agent/dt_fips_disabled.flag"))
				Expect(err).To(Not(BeNil()))
			})
		})

		Context("Additional code modules configured", func() {
			BeforeEach(func() {
				os.Setenv("BP_DEBUG", "true")
				os.Setenv("VCAP_APPLICATION", `{"name":"JimBob"}`)
				os.Setenv("VCAP_SERVICES", `{
					"0": [{"name":"dynatrace","credentials":{"apiurl":"https://example.com","apitoken":"`+apiToken+`","environmentid":"`+environmentID+`","addtechnologies":"go,nodejs"}}]
				}`)

				httpmock.RegisterResponder("GET", "https://example.com/v1/deployment/installer/agent/"+OSName+"/"+InstallationMethod+"/latest?bitness=64&include=nginx&include=process&include=dotnet&include=go&include=nodejs",
					api_header_check)

				httpmock.RegisterResponder("GET", "https://example.com/v1/deployment/installer/agent/processmoduleconfig",
					api_header_check)
			})

			It("installs dynatrace with additional code modules", func() {
				if runtime.GOOS != "windows" {
					mockCommand.EXPECT().Execute("", gomock.Any(), gomock.Any(), gomock.Any(), buildDir).Do(simulateUnixInstaller)
				}
				err = hook.AfterCompile(stager)
				Expect(err).To(BeNil())

				Expect(buffer.String()).To(ContainSubstring("Adding additional code module to download: go"))
				Expect(buffer.String()).To(ContainSubstring("Adding additional code module to download: nodejs"))
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
