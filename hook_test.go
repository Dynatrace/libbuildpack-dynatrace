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
					"path" : "agent/bin/current/windows-x86-64/oneagentdotnet.dll",
					"md5" : "2bf4ba9e90e2589428f6f6f3a964cba2",
					"version" : "1.130.0.20170914-125024",
					"binarytype" : "primary"
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
			GOOS:                "linux",
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
			oldVcapApplication    string
			oldVcapServices       string
			oldBpDebug            string
			oldVcapServicesFile   string
			oldServiceBindingRoot string

			environmentID string
			apiToken      string
		)
		BeforeEach(func() {
			oldVcapApplication = os.Getenv("VCAP_APPLICATION")
			oldVcapServices = os.Getenv("VCAP_SERVICES")
			oldBpDebug = os.Getenv("BP_DEBUG")
			oldVcapServicesFile = os.Getenv("VCAP_SERVICES_FILE_PATH")
			oldServiceBindingRoot = os.Getenv("SERVICE_BINDING_ROOT")
			os.Unsetenv("VCAP_SERVICES_FILE_PATH")
			os.Unsetenv("SERVICE_BINDING_ROOT")
			environmentID = "123456"
			apiToken = "ExcitingToken28"
		})
		AfterEach(func() {
			os.Setenv("VCAP_APPLICATION", oldVcapApplication)
			os.Setenv("VCAP_SERVICES", oldVcapServices)
			os.Setenv("BP_DEBUG", oldBpDebug)
			if oldVcapServicesFile != "" {
				os.Setenv("VCAP_SERVICES_FILE_PATH", oldVcapServicesFile)
			} else {
				os.Unsetenv("VCAP_SERVICES_FILE_PATH")
			}
			if oldServiceBindingRoot != "" {
				os.Setenv("SERVICE_BINDING_ROOT", oldServiceBindingRoot)
			} else {
				os.Unsetenv("SERVICE_BINDING_ROOT")
			}
		})

		Context("no service env vars are set", func() {
			BeforeEach(func() {
				os.Setenv("VCAP_APPLICATION", `{"name":"JimBob"}`)
				os.Unsetenv("VCAP_SERVICES")
				os.Unsetenv("VCAP_SERVICES_FILE_PATH")
				os.Unsetenv("SERVICE_BINDING_ROOT")
			})

			It("does nothing and succeeds", func() {
				err = hook.AfterCompile(stager)
				Expect(err).To(BeNil())

				Expect(buffer.String()).To(Equal(""))
			})

			It("logs credentials not found when debug is enabled", func() {
				os.Setenv("BP_DEBUG", "true")
				err = hook.AfterCompile(stager)
				Expect(err).To(BeNil())

				Expect(buffer.String()).To(ContainSubstring("Dynatrace service credentials not found!"))
			})
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
set COR_PROFILER_PATH_64=C:\users\vcap\app\dynatrace\oneagent\agent\bin\current\windows-x86-64\oneagentdotnet.dll
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
set COR_PROFILER_PATH_64=C:\users\vcap\app\dynatrace\oneagent\agent\bin\current\windows-x86-64\oneagentdotnet.dll
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
set COR_PROFILER_PATH_64=C:\users\vcap\app\dynatrace\oneagent\agent\bin\current\windows-x86-64\oneagentdotnet.dll
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
set COR_PROFILER_PATH_64=C:\users\vcap\app\dynatrace\oneagent\agent\bin\current\windows-x86-64\oneagentdotnet.dll
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
set COR_PROFILER_PATH_64=C:\users\vcap\app\dynatrace\oneagent\agent\bin\current\windows-x86-64\oneagentdotnet.dll
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
set COR_PROFILER_PATH_64=C:\users\vcap\app\dynatrace\oneagent\agent\bin\current\windows-x86-64\oneagentdotnet.dll
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
set COR_PROFILER_PATH_64=C:\users\vcap\app\dynatrace\oneagent\agent\bin\current\windows-x86-64\oneagentdotnet.dll
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
set COR_PROFILER_PATH_64=C:\users\vcap\app\dynatrace\oneagent\agent\bin\current\windows-x86-64\oneagentdotnet.dll
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
set COR_PROFILER_PATH_64=C:\users\vcap\app\dynatrace\oneagent\agent\bin\current\windows-x86-64\oneagentdotnet.dll
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
set COR_PROFILER_PATH_64=C:\users\vcap\app\dynatrace\oneagent\agent\bin\current\windows-x86-64\oneagentdotnet.dll
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

		Context("VCAP_SERVICES_FILE_PATH points to valid file with dynatrace service", func() {
			var vcapFile string

			BeforeEach(func() {
				os.Setenv("BP_DEBUG", "true")
				os.Setenv("VCAP_APPLICATION", `{"name":"JimBob"}`)
				os.Setenv("VCAP_SERVICES", "{}")

				vcapContent := `{
					"0": [{"name":"mysql"}],
					"1": [{"name":"dynatrace","credentials":{"apiurl":"https://example.com","apitoken":"` + apiToken + `","environmentid":"` + environmentID + `"}}],
					"2": [{"name":"redis"}]
				}`

				vcapFile = filepath.Join(buildDir, "vcap_services.json")
				err = os.WriteFile(vcapFile, []byte(vcapContent), 0644)
				Expect(err).To(BeNil())

				os.Setenv("VCAP_SERVICES_FILE_PATH", vcapFile)

				httpmock.RegisterResponder("GET", "https://example.com/v1/deployment/installer/agent/"+OSName+"/"+InstallationMethod+"/latest?bitness=64&include=nginx&include=process&include=dotnet",
					api_header_check)

				httpmock.RegisterResponder("GET", "https://example.com/v1/deployment/installer/agent/processmoduleconfig",
					api_header_check)
			})

			It("loads credentials from file and installs dynatrace", func() {
				if runtime.GOOS != "windows" {
					mockCommand.EXPECT().Execute("", gomock.Any(), gomock.Any(), gomock.Any(), buildDir).Do(simulateUnixInstaller)
				}

				err = hook.AfterCompile(stager)
				Expect(err).To(BeNil())

				Expect(buffer.String()).To(ContainSubstring("Loading VCAP services from file: " + vcapFile))
			})
		})

		Context("VCAP_SERVICES_FILE_PATH points to non-existent file", func() {
			BeforeEach(func() {
				os.Setenv("VCAP_APPLICATION", `{"name":"JimBob"}`)
				os.Setenv("VCAP_SERVICES", `{
					"0": [{"name":"dynatrace","credentials":{"apiurl":"https://example.com","apitoken":"`+apiToken+`","environmentid":"`+environmentID+`"}}]
				}`)
				os.Setenv("VCAP_SERVICES_FILE_PATH", "/nonexistent/path/vcap_services.json")
			})

			It("does nothing and succeeds with error log", func() {
				err = hook.AfterCompile(stager)
				Expect(err).To(BeNil())

				Expect(buffer.String()).To(ContainSubstring("Failed to read VCAP services file /nonexistent/path/vcap_services.json"))
			})
		})

		Context("VCAP_SERVICES_FILE_PATH points to file with invalid JSON", func() {
			BeforeEach(func() {
				os.Setenv("BP_DEBUG", "true")
				os.Setenv("VCAP_APPLICATION", `{"name":"JimBob"}`)
				os.Setenv("VCAP_SERVICES", "{}")

				vcapFile := filepath.Join(buildDir, "vcap_services_invalid.json")
				err = os.WriteFile(vcapFile, []byte("not valid json"), 0644)
				Expect(err).To(BeNil())

				os.Setenv("VCAP_SERVICES_FILE_PATH", vcapFile)
			})

			It("does nothing and succeeds with debug log for unmarshal failure", func() {
				err = hook.AfterCompile(stager)
				Expect(err).To(BeNil())

				Expect(buffer.String()).To(ContainSubstring("Failed to unmarshal VCAP_SERVICES:"))
			})
		})

		Context("VCAP_SERVICES_FILE_PATH points to file with empty JSON object", func() {
			var vcapFile string

			BeforeEach(func() {
				os.Setenv("BP_DEBUG", "true")
				os.Setenv("VCAP_APPLICATION", `{"name":"JimBob"}`)
				os.Setenv("VCAP_SERVICES", "{}")

				vcapFile = filepath.Join(buildDir, "vcap_services_empty.json")
				err = os.WriteFile(vcapFile, []byte("{}"), 0644)
				Expect(err).To(BeNil())

				os.Setenv("VCAP_SERVICES_FILE_PATH", vcapFile)
			})

			It("does nothing and succeeds", func() {
				err = hook.AfterCompile(stager)
				Expect(err).To(BeNil())

				Expect(buffer.String()).To(ContainSubstring("Loading VCAP services from file: " + vcapFile))
			})
		})

		Context("VCAP_SERVICES_FILE_PATH takes precedence over VCAP_SERVICES env var", func() {
			var vcapFile string

			BeforeEach(func() {
				os.Setenv("BP_DEBUG", "true")
				os.Setenv("VCAP_APPLICATION", `{"name":"JimBob"}`)

				// Env var has a different (non-dynatrace) service
				os.Setenv("VCAP_SERVICES", `{"0": [{"name":"mysql"}]}`)

				// File has the dynatrace service
				vcapContent := `{
					"0": [{"name":"dynatrace","credentials":{"apiurl":"https://example.com","apitoken":"` + apiToken + `","environmentid":"` + environmentID + `"}}]
				}`

				vcapFile = filepath.Join(buildDir, "vcap_services_precedence.json")
				err = os.WriteFile(vcapFile, []byte(vcapContent), 0644)
				Expect(err).To(BeNil())

				os.Setenv("VCAP_SERVICES_FILE_PATH", vcapFile)

				httpmock.RegisterResponder("GET", "https://example.com/v1/deployment/installer/agent/"+OSName+"/"+InstallationMethod+"/latest?bitness=64&include=nginx&include=process&include=dotnet",
					api_header_check)

				httpmock.RegisterResponder("GET", "https://example.com/v1/deployment/installer/agent/processmoduleconfig",
					api_header_check)
			})

			It("uses file content and ignores env var", func() {
				if runtime.GOOS != "windows" {
					mockCommand.EXPECT().Execute("", gomock.Any(), gomock.Any(), gomock.Any(), buildDir).Do(simulateUnixInstaller)
				}

				err = hook.AfterCompile(stager)
				Expect(err).To(BeNil())

				Expect(buffer.String()).To(ContainSubstring("Both VCAP_SERVICES_FILE_PATH and VCAP_SERVICES are set, using file: " + vcapFile))
				Expect(buffer.String()).To(ContainSubstring("Loading VCAP services from file: " + vcapFile))
				Expect(buffer.String()).NotTo(ContainSubstring("Loading VCAP services from environment variable"))
			})
		})

		Context("VCAP_SERVICES_FILE_PATH is empty string with VCAP_SERVICES set", func() {
			BeforeEach(func() {
				os.Setenv("BP_DEBUG", "true")
				os.Setenv("VCAP_APPLICATION", `{"name":"JimBob"}`)
				os.Setenv("VCAP_SERVICES", `{
					"0": [{"name":"dynatrace","credentials":{"apiurl":"https://example.com","apitoken":"`+apiToken+`","environmentid":"`+environmentID+`"}}]
				}`)
				os.Setenv("VCAP_SERVICES_FILE_PATH", "")

				httpmock.RegisterResponder("GET", "https://example.com/v1/deployment/installer/agent/"+OSName+"/"+InstallationMethod+"/latest?bitness=64&include=nginx&include=process&include=dotnet",
					api_header_check)

				httpmock.RegisterResponder("GET", "https://example.com/v1/deployment/installer/agent/processmoduleconfig",
					api_header_check)
			})

			It("warns about empty path and falls back to env var", func() {
				if runtime.GOOS != "windows" {
					mockCommand.EXPECT().Execute("", gomock.Any(), gomock.Any(), gomock.Any(), buildDir).Do(simulateUnixInstaller)
				}

				err = hook.AfterCompile(stager)
				Expect(err).To(BeNil())

				Expect(buffer.String()).To(ContainSubstring("VCAP_SERVICES_FILE_PATH is set but empty, falling back to VCAP_SERVICES environment variable"))
			})
		})

		Context("Neither VCAP_SERVICES_FILE_PATH nor VCAP_SERVICES is set", func() {
			BeforeEach(func() {
				os.Setenv("VCAP_APPLICATION", `{"name":"JimBob"}`)
				os.Unsetenv("VCAP_SERVICES_FILE_PATH")
				os.Setenv("VCAP_SERVICES", "")
			})

			It("does nothing and succeeds", func() {
				err = hook.AfterCompile(stager)
				Expect(err).To(BeNil())

				Expect(buffer.String()).To(Equal(""))
			})
		})

		// --- K8s Service Binding Tests ---

		Context("SERVICE_BINDING_ROOT is not set", func() {
			BeforeEach(func() {
				os.Setenv("VCAP_APPLICATION", `{"name":"JimBob"}`)
				os.Setenv("VCAP_SERVICES", "")
				os.Unsetenv("SERVICE_BINDING_ROOT")
			})

			It("does nothing and succeeds", func() {
				err = hook.AfterCompile(stager)
				Expect(err).To(BeNil())

				Expect(buffer.String()).To(Equal(""))
			})
		})

		Context("SERVICE_BINDING_ROOT set but no matching binding exists", func() {
			BeforeEach(func() {
				os.Setenv("BP_DEBUG", "true")
				os.Setenv("VCAP_APPLICATION", `{"name":"JimBob"}`)
				os.Setenv("VCAP_SERVICES", "")
				err = os.MkdirAll(filepath.Join(buildDir, "service-bindings"), 0755)
				Expect(err).To(BeNil())
				os.Setenv("SERVICE_BINDING_ROOT", filepath.Join(buildDir, "service-bindings"))
			})

			It("does nothing and logs warning", func() {
				err = hook.AfterCompile(stager)
				Expect(err).To(BeNil())

				Expect(buffer.String()).To(ContainSubstring("No dynatrace service binding found under"))
			})
		})

		Context("SERVICE_BINDING_ROOT with complete dynatrace credentials", func() {
			BeforeEach(func() {
				os.Setenv("BP_DEBUG", "true")
				os.Setenv("VCAP_APPLICATION", `{"name":"JimBob"}`)
				os.Setenv("VCAP_SERVICES", "")

				bindingDir := filepath.Join(buildDir, "service-bindings", "dynatrace")
				err = os.MkdirAll(bindingDir, 0755)
				Expect(err).To(BeNil())

				os.WriteFile(filepath.Join(bindingDir, "type"), []byte("dynatrace"), 0644)
				os.WriteFile(filepath.Join(bindingDir, "environmentid"), []byte(environmentID), 0644)
				os.WriteFile(filepath.Join(bindingDir, "apitoken"), []byte(apiToken), 0644)
				os.WriteFile(filepath.Join(bindingDir, "apiurl"), []byte("https://example.com"), 0644)
				os.WriteFile(filepath.Join(bindingDir, "name"), []byte("dynatrace"), 0644)

				os.Setenv("SERVICE_BINDING_ROOT", filepath.Join(buildDir, "service-bindings"))

				httpmock.RegisterResponder("GET", "https://example.com/v1/deployment/installer/agent/"+OSName+"/"+InstallationMethod+"/latest?bitness=64&include=nginx&include=process&include=dotnet",
					api_header_check)

				httpmock.RegisterResponder("GET", "https://example.com/v1/deployment/installer/agent/processmoduleconfig",
					api_header_check)
			})

			It("installs dynatrace from service binding", func() {
				if runtime.GOOS != "windows" {
					mockCommand.EXPECT().Execute("", gomock.Any(), gomock.Any(), gomock.Any(), buildDir).Do(simulateUnixInstaller)
				}

				err = hook.AfterCompile(stager)
				Expect(err).To(BeNil())

				Expect(buffer.String()).To(ContainSubstring("Loading Dynatrace credentials from service binding"))
				Expect(buffer.String()).To(ContainSubstring("Found one matching service: dynatrace"))
			})
		})

		Context("SERVICE_BINDING_ROOT with incomplete credentials (missing apitoken)", func() {
			BeforeEach(func() {
				os.Setenv("VCAP_APPLICATION", `{"name":"JimBob"}`)
				os.Setenv("VCAP_SERVICES", "")

				bindingDir := filepath.Join(buildDir, "service-bindings", "dynatrace")
				err = os.MkdirAll(bindingDir, 0755)
				Expect(err).To(BeNil())

				os.WriteFile(filepath.Join(bindingDir, "type"), []byte("dynatrace"), 0644)
				os.WriteFile(filepath.Join(bindingDir, "environmentid"), []byte(environmentID), 0644)

				os.Setenv("SERVICE_BINDING_ROOT", filepath.Join(buildDir, "service-bindings"))
			})

			It("does nothing and warns about incomplete credentials", func() {
				err = hook.AfterCompile(stager)
				Expect(err).To(BeNil())

				Expect(buffer.String()).To(ContainSubstring("Incomplete credentials for service"))
			})
		})

		Context("SERVICE_BINDING_ROOT with customoneagenturl only", func() {
			BeforeEach(func() {
				os.Setenv("BP_DEBUG", "true")
				os.Setenv("VCAP_APPLICATION", `{"name":"JimBob"}`)
				os.Setenv("VCAP_SERVICES", "")

				bindingDir := filepath.Join(buildDir, "service-bindings", "dynatrace")
				err = os.MkdirAll(bindingDir, 0755)
				Expect(err).To(BeNil())

				os.WriteFile(filepath.Join(bindingDir, "type"), []byte("dynatrace"), 0644)
				os.WriteFile(filepath.Join(bindingDir, "customoneagenturl"), []byte("https://example.com/oneagent"), 0644)
				os.WriteFile(filepath.Join(bindingDir, "apiurl"), []byte("https://example.com"), 0644)
				os.WriteFile(filepath.Join(bindingDir, "apitoken"), []byte(apiToken), 0644)
				os.WriteFile(filepath.Join(bindingDir, "environmentid"), []byte(environmentID), 0644)

				os.Setenv("SERVICE_BINDING_ROOT", filepath.Join(buildDir, "service-bindings"))

				httpmock.RegisterResponder("GET", "https://example.com/oneagent", func(r *http.Request) (*http.Response, error) {
					return getMockResponse(), nil
				})

				httpmock.RegisterResponder("GET", "https://example.com/v1/deployment/installer/agent/processmoduleconfig",
					api_header_check)
			})

			It("installs dynatrace using custom URL", func() {
				if runtime.GOOS != "windows" {
					mockCommand.EXPECT().Execute("", gomock.Any(), gomock.Any(), gomock.Any(), buildDir).Do(simulateUnixInstaller)
				}

				err = hook.AfterCompile(stager)
				Expect(err).To(BeNil())

				Expect(buffer.String()).To(ContainSubstring("Found one matching service"))
			})
		})

		Context("SERVICE_BINDING_ROOT with optional fields", func() {
			BeforeEach(func() {
				os.Setenv("BP_DEBUG", "true")
				os.Setenv("VCAP_APPLICATION", `{"name":"JimBob"}`)
				os.Setenv("VCAP_SERVICES", "")

				bindingDir := filepath.Join(buildDir, "service-bindings", "dynatrace")
				err = os.MkdirAll(bindingDir, 0755)
				Expect(err).To(BeNil())

				os.WriteFile(filepath.Join(bindingDir, "type"), []byte("dynatrace"), 0644)
				os.WriteFile(filepath.Join(bindingDir, "environmentid"), []byte(environmentID), 0644)
				os.WriteFile(filepath.Join(bindingDir, "apitoken"), []byte(apiToken), 0644)
				os.WriteFile(filepath.Join(bindingDir, "apiurl"), []byte("https://example.com"), 0644)
				os.WriteFile(filepath.Join(bindingDir, "networkzone"), []byte("west-us"), 0644)
				os.WriteFile(filepath.Join(bindingDir, "skiperrors"), []byte("true"), 0644)
				os.WriteFile(filepath.Join(bindingDir, "enablefips"), []byte("true"), 0644)
				os.WriteFile(filepath.Join(bindingDir, "addtechnologies"), []byte("go,nodejs"), 0644)

				os.Setenv("SERVICE_BINDING_ROOT", filepath.Join(buildDir, "service-bindings"))

				httpmock.RegisterResponder("GET", "https://example.com/v1/deployment/installer/agent/"+OSName+"/"+InstallationMethod+"/latest?bitness=64&include=nginx&include=process&include=dotnet&include=go&include=nodejs&networkZone=west-us",
					api_header_check)

				httpmock.RegisterResponder("GET", "https://example.com/v1/deployment/installer/agent/processmoduleconfig",
					api_header_check)
			})

			It("installs dynatrace with all optional fields parsed correctly", func() {
				if runtime.GOOS != "windows" {
					mockCommand.EXPECT().Execute("", gomock.Any(), gomock.Any(), gomock.Any(), buildDir).Do(simulateUnixInstaller)
				}

				err = hook.AfterCompile(stager)
				Expect(err).To(BeNil())

				Expect(buffer.String()).To(ContainSubstring("Adding additional code module to download: go"))
				Expect(buffer.String()).To(ContainSubstring("Adding additional code module to download: nodejs"))
			})
		})

		Context("SERVICE_BINDING_ROOT with name file present", func() {
			BeforeEach(func() {
				os.Setenv("BP_DEBUG", "true")
				os.Setenv("VCAP_APPLICATION", `{"name":"JimBob"}`)
				os.Setenv("VCAP_SERVICES", "")

				bindingDir := filepath.Join(buildDir, "service-bindings", "dynatrace")
				err = os.MkdirAll(bindingDir, 0755)
				Expect(err).To(BeNil())

				os.WriteFile(filepath.Join(bindingDir, "type"), []byte("dynatrace"), 0644)
				os.WriteFile(filepath.Join(bindingDir, "environmentid"), []byte(environmentID), 0644)
				os.WriteFile(filepath.Join(bindingDir, "apitoken"), []byte(apiToken), 0644)
				os.WriteFile(filepath.Join(bindingDir, "apiurl"), []byte("https://example.com"), 0644)
				os.WriteFile(filepath.Join(bindingDir, "name"), []byte("my-dynatrace-binding"), 0644)

				os.Setenv("SERVICE_BINDING_ROOT", filepath.Join(buildDir, "service-bindings"))

				httpmock.RegisterResponder("GET", "https://example.com/v1/deployment/installer/agent/"+OSName+"/"+InstallationMethod+"/latest?bitness=64&include=nginx&include=process&include=dotnet",
					api_header_check)

				httpmock.RegisterResponder("GET", "https://example.com/v1/deployment/installer/agent/processmoduleconfig",
					api_header_check)
			})

			It("uses name file content as service name", func() {
				if runtime.GOOS != "windows" {
					mockCommand.EXPECT().Execute("", gomock.Any(), gomock.Any(), gomock.Any(), buildDir).Do(simulateUnixInstaller)
				}

				err = hook.AfterCompile(stager)
				Expect(err).To(BeNil())

				Expect(buffer.String()).To(ContainSubstring("Found one matching service: my-dynatrace-binding"))
			})
		})

		Context("SERVICE_BINDING_ROOT with name file absent defaults to dynatrace", func() {
			BeforeEach(func() {
				os.Setenv("BP_DEBUG", "true")
				os.Setenv("VCAP_APPLICATION", `{"name":"JimBob"}`)
				os.Setenv("VCAP_SERVICES", "")

				bindingDir := filepath.Join(buildDir, "service-bindings", "dynatrace")
				err = os.MkdirAll(bindingDir, 0755)
				Expect(err).To(BeNil())

				os.WriteFile(filepath.Join(bindingDir, "type"), []byte("dynatrace"), 0644)
				os.WriteFile(filepath.Join(bindingDir, "environmentid"), []byte(environmentID), 0644)
				os.WriteFile(filepath.Join(bindingDir, "apitoken"), []byte(apiToken), 0644)
				os.WriteFile(filepath.Join(bindingDir, "apiurl"), []byte("https://example.com"), 0644)
				// No name file

				os.Setenv("SERVICE_BINDING_ROOT", filepath.Join(buildDir, "service-bindings"))

				httpmock.RegisterResponder("GET", "https://example.com/v1/deployment/installer/agent/"+OSName+"/"+InstallationMethod+"/latest?bitness=64&include=nginx&include=process&include=dotnet",
					api_header_check)

				httpmock.RegisterResponder("GET", "https://example.com/v1/deployment/installer/agent/processmoduleconfig",
					api_header_check)
			})

			It("defaults service name to dynatrace", func() {
				if runtime.GOOS != "windows" {
					mockCommand.EXPECT().Execute("", gomock.Any(), gomock.Any(), gomock.Any(), buildDir).Do(simulateUnixInstaller)
				}

				err = hook.AfterCompile(stager)
				Expect(err).To(BeNil())

				Expect(buffer.String()).To(ContainSubstring("Found one matching service: dynatrace"))
			})
		})

		Context("SERVICE_BINDING_ROOT with files containing trailing whitespace", func() {
			BeforeEach(func() {
				os.Setenv("BP_DEBUG", "true")
				os.Setenv("VCAP_APPLICATION", `{"name":"JimBob"}`)
				os.Setenv("VCAP_SERVICES", "")

				bindingDir := filepath.Join(buildDir, "service-bindings", "dynatrace")
				err = os.MkdirAll(bindingDir, 0755)
				Expect(err).To(BeNil())

				os.WriteFile(filepath.Join(bindingDir, "type"), []byte("dynatrace"), 0644)
				os.WriteFile(filepath.Join(bindingDir, "environmentid"), []byte(environmentID+"\n  "), 0644)
				os.WriteFile(filepath.Join(bindingDir, "apitoken"), []byte("  "+apiToken+"\n"), 0644)
				os.WriteFile(filepath.Join(bindingDir, "apiurl"), []byte("https://example.com\n"), 0644)

				os.Setenv("SERVICE_BINDING_ROOT", filepath.Join(buildDir, "service-bindings"))

				httpmock.RegisterResponder("GET", "https://example.com/v1/deployment/installer/agent/"+OSName+"/"+InstallationMethod+"/latest?bitness=64&include=nginx&include=process&include=dotnet",
					api_header_check)

				httpmock.RegisterResponder("GET", "https://example.com/v1/deployment/installer/agent/processmoduleconfig",
					api_header_check)
			})

			It("trims values and installs dynatrace", func() {
				if runtime.GOOS != "windows" {
					mockCommand.EXPECT().Execute("", gomock.Any(), gomock.Any(), gomock.Any(), buildDir).Do(simulateUnixInstaller)
				}

				err = hook.AfterCompile(stager)
				Expect(err).To(BeNil())

				Expect(buffer.String()).To(ContainSubstring("Found one matching service"))
			})
		})

		// --- servicebinding.io spec compliance tests ---

		Context("SERVICE_BINDING_ROOT with directory name containing dynatrace as substring", func() {
			BeforeEach(func() {
				os.Setenv("BP_DEBUG", "true")
				os.Setenv("VCAP_APPLICATION", `{"name":"JimBob"}`)
				os.Setenv("VCAP_SERVICES", "")

				bindingDir := filepath.Join(buildDir, "service-bindings", "my-dynatrace-binding")
				err = os.MkdirAll(bindingDir, 0755)
				Expect(err).To(BeNil())

				os.WriteFile(filepath.Join(bindingDir, "type"), []byte("dynatrace"), 0644)
				os.WriteFile(filepath.Join(bindingDir, "environmentid"), []byte(environmentID), 0644)
				os.WriteFile(filepath.Join(bindingDir, "apitoken"), []byte(apiToken), 0644)
				os.WriteFile(filepath.Join(bindingDir, "apiurl"), []byte("https://example.com"), 0644)

				os.Setenv("SERVICE_BINDING_ROOT", filepath.Join(buildDir, "service-bindings"))

				httpmock.RegisterResponder("GET", "https://example.com/v1/deployment/installer/agent/"+OSName+"/"+InstallationMethod+"/latest?bitness=64&include=nginx&include=process&include=dotnet",
					api_header_check)

				httpmock.RegisterResponder("GET", "https://example.com/v1/deployment/installer/agent/processmoduleconfig",
					api_header_check)
			})

			It("discovers binding by directory name substring and installs", func() {
				if runtime.GOOS != "windows" {
					mockCommand.EXPECT().Execute("", gomock.Any(), gomock.Any(), gomock.Any(), buildDir).Do(simulateUnixInstaller)
				}

				err = hook.AfterCompile(stager)
				Expect(err).To(BeNil())

				Expect(buffer.String()).To(ContainSubstring("Loading Dynatrace credentials from service binding"))
				Expect(buffer.String()).To(ContainSubstring("Found one matching service: my-dynatrace-binding"))
			})
		})

		Context("SERVICE_BINDING_ROOT with directory name containing Dynatrace uppercase", func() {
			BeforeEach(func() {
				os.Setenv("BP_DEBUG", "true")
				os.Setenv("VCAP_APPLICATION", `{"name":"JimBob"}`)
				os.Setenv("VCAP_SERVICES", "")

				bindingDir := filepath.Join(buildDir, "service-bindings", "Dynatrace-prod")
				err = os.MkdirAll(bindingDir, 0755)
				Expect(err).To(BeNil())

				os.WriteFile(filepath.Join(bindingDir, "environmentid"), []byte(environmentID), 0644)
				os.WriteFile(filepath.Join(bindingDir, "apitoken"), []byte(apiToken), 0644)
				os.WriteFile(filepath.Join(bindingDir, "apiurl"), []byte("https://example.com"), 0644)

				os.Setenv("SERVICE_BINDING_ROOT", filepath.Join(buildDir, "service-bindings"))

				httpmock.RegisterResponder("GET", "https://example.com/v1/deployment/installer/agent/"+OSName+"/"+InstallationMethod+"/latest?bitness=64&include=nginx&include=process&include=dotnet",
					api_header_check)

				httpmock.RegisterResponder("GET", "https://example.com/v1/deployment/installer/agent/processmoduleconfig",
					api_header_check)
			})

			It("matches case-insensitively on directory name", func() {
				if runtime.GOOS != "windows" {
					mockCommand.EXPECT().Execute("", gomock.Any(), gomock.Any(), gomock.Any(), buildDir).Do(simulateUnixInstaller)
				}

				err = hook.AfterCompile(stager)
				Expect(err).To(BeNil())

				Expect(buffer.String()).To(ContainSubstring("Found one matching service: Dynatrace-prod"))
			})
		})

		Context("SERVICE_BINDING_ROOT with directory name not containing dynatrace", func() {
			BeforeEach(func() {
				os.Setenv("BP_DEBUG", "true")
				os.Setenv("VCAP_APPLICATION", `{"name":"JimBob"}`)
				os.Setenv("VCAP_SERVICES", "")

				bindingDir := filepath.Join(buildDir, "service-bindings", "mysql-binding")
				err = os.MkdirAll(bindingDir, 0755)
				Expect(err).To(BeNil())

				os.WriteFile(filepath.Join(bindingDir, "environmentid"), []byte(environmentID), 0644)
				os.WriteFile(filepath.Join(bindingDir, "apitoken"), []byte(apiToken), 0644)

				os.Setenv("SERVICE_BINDING_ROOT", filepath.Join(buildDir, "service-bindings"))
			})

			It("skips binding whose name does not contain dynatrace", func() {
				err = hook.AfterCompile(stager)
				Expect(err).To(BeNil())

				Expect(buffer.String()).To(ContainSubstring("No dynatrace service binding found under"))
			})
		})

		Context("SERVICE_BINDING_ROOT with multiple matching bindings", func() {
			BeforeEach(func() {
				os.Setenv("BP_DEBUG", "true")
				os.Setenv("VCAP_APPLICATION", `{"name":"JimBob"}`)
				os.Setenv("VCAP_SERVICES", "")

				bindingDir1 := filepath.Join(buildDir, "service-bindings", "dynatrace-prod")
				err = os.MkdirAll(bindingDir1, 0755)
				Expect(err).To(BeNil())
				os.WriteFile(filepath.Join(bindingDir1, "environmentid"), []byte(environmentID), 0644)
				os.WriteFile(filepath.Join(bindingDir1, "apitoken"), []byte(apiToken), 0644)
				os.WriteFile(filepath.Join(bindingDir1, "apiurl"), []byte("https://example.com"), 0644)

				bindingDir2 := filepath.Join(buildDir, "service-bindings", "dynatrace-staging")
				err = os.MkdirAll(bindingDir2, 0755)
				Expect(err).To(BeNil())
				os.WriteFile(filepath.Join(bindingDir2, "environmentid"), []byte("999999"), 0644)
				os.WriteFile(filepath.Join(bindingDir2, "apitoken"), []byte("OtherToken"), 0644)
				os.WriteFile(filepath.Join(bindingDir2, "apiurl"), []byte("https://other.example.com"), 0644)

				os.Setenv("SERVICE_BINDING_ROOT", filepath.Join(buildDir, "service-bindings"))
			})

			It("warns about multiple matches and does nothing", func() {
				err = hook.AfterCompile(stager)
				Expect(err).To(BeNil())

				Expect(buffer.String()).To(ContainSubstring("More than one matching service binding found"))
			})
		})

		Context("SERVICE_BINDING_ROOT with mixed bindings only one matching", func() {
			BeforeEach(func() {
				os.Setenv("BP_DEBUG", "true")
				os.Setenv("VCAP_APPLICATION", `{"name":"JimBob"}`)
				os.Setenv("VCAP_SERVICES", "")

				// Non-dynatrace binding
				mysqlDir := filepath.Join(buildDir, "service-bindings", "mysql")
				err = os.MkdirAll(mysqlDir, 0755)
				Expect(err).To(BeNil())

				// Dynatrace binding
				dtDir := filepath.Join(buildDir, "service-bindings", "dynatrace")
				err = os.MkdirAll(dtDir, 0755)
				Expect(err).To(BeNil())
				os.WriteFile(filepath.Join(dtDir, "type"), []byte("dynatrace"), 0644)
				os.WriteFile(filepath.Join(dtDir, "environmentid"), []byte(environmentID), 0644)
				os.WriteFile(filepath.Join(dtDir, "apitoken"), []byte(apiToken), 0644)
				os.WriteFile(filepath.Join(dtDir, "apiurl"), []byte("https://example.com"), 0644)

				os.Setenv("SERVICE_BINDING_ROOT", filepath.Join(buildDir, "service-bindings"))

				httpmock.RegisterResponder("GET", "https://example.com/v1/deployment/installer/agent/"+OSName+"/"+InstallationMethod+"/latest?bitness=64&include=nginx&include=process&include=dotnet",
					api_header_check)

				httpmock.RegisterResponder("GET", "https://example.com/v1/deployment/installer/agent/processmoduleconfig",
					api_header_check)
			})

			It("selects the dynatrace binding and ignores others", func() {
				if runtime.GOOS != "windows" {
					mockCommand.EXPECT().Execute("", gomock.Any(), gomock.Any(), gomock.Any(), buildDir).Do(simulateUnixInstaller)
				}

				err = hook.AfterCompile(stager)
				Expect(err).To(BeNil())

				Expect(buffer.String()).To(ContainSubstring("Found one matching service: dynatrace"))
			})
		})

		Context("SERVICE_BINDING_ROOT directory does not exist", func() {
			BeforeEach(func() {
				os.Setenv("BP_DEBUG", "true")
				os.Setenv("VCAP_APPLICATION", `{"name":"JimBob"}`)
				os.Setenv("VCAP_SERVICES", "")
				os.Setenv("SERVICE_BINDING_ROOT", filepath.Join(buildDir, "nonexistent"))
			})

			It("warns about unreadable directory and does nothing", func() {
				err = hook.AfterCompile(stager)
				Expect(err).To(BeNil())

				Expect(buffer.String()).To(ContainSubstring("Failed to read SERVICE_BINDING_ROOT directory"))
			})
		})

		// --- Multi-source warning tests ---

		Context("Both VCAP_SERVICES and SERVICE_BINDING_ROOT are set", func() {
			BeforeEach(func() {
				os.Setenv("BP_DEBUG", "true")
				os.Setenv("VCAP_APPLICATION", `{"name":"JimBob"}`)
				os.Setenv("VCAP_SERVICES", `{
					"0": [{"name":"dynatrace","credentials":{"apiurl":"https://example.com","apitoken":"`+apiToken+`","environmentid":"`+environmentID+`"}}]
				}`)

				bindingDir := filepath.Join(buildDir, "service-bindings", "dynatrace")
				err = os.MkdirAll(bindingDir, 0755)
				Expect(err).To(BeNil())
				os.WriteFile(filepath.Join(bindingDir, "type"), []byte("dynatrace"), 0644)
				os.WriteFile(filepath.Join(bindingDir, "environmentid"), []byte(environmentID), 0644)
				os.WriteFile(filepath.Join(bindingDir, "apitoken"), []byte(apiToken), 0644)
				os.WriteFile(filepath.Join(bindingDir, "apiurl"), []byte("https://example.com"), 0644)

				os.Setenv("SERVICE_BINDING_ROOT", filepath.Join(buildDir, "service-bindings"))

				httpmock.RegisterResponder("GET", "https://example.com/v1/deployment/installer/agent/"+OSName+"/"+InstallationMethod+"/latest?bitness=64&include=nginx&include=process&include=dotnet",
					api_header_check)

				httpmock.RegisterResponder("GET", "https://example.com/v1/deployment/installer/agent/processmoduleconfig",
					api_header_check)
			})

			It("warns about multiple sources and uses VCAP", func() {
				if runtime.GOOS != "windows" {
					mockCommand.EXPECT().Execute("", gomock.Any(), gomock.Any(), gomock.Any(), buildDir).Do(simulateUnixInstaller)
				}

				err = hook.AfterCompile(stager)
				Expect(err).To(BeNil())

				Expect(buffer.String()).To(ContainSubstring("Multiple credential sources detected"))
				Expect(buffer.String()).To(ContainSubstring("VCAP_SERVICES"))
				Expect(buffer.String()).To(ContainSubstring("SERVICE_BINDING_ROOT"))
			})
		})

		Context("VCAP_SERVICES has non-dynatrace and SERVICE_BINDING_ROOT has dynatrace", func() {
			BeforeEach(func() {
				os.Setenv("BP_DEBUG", "true")
				os.Setenv("VCAP_APPLICATION", `{"name":"JimBob"}`)
				os.Setenv("VCAP_SERVICES", `{"0": [{"name":"mysql"}]}`)

				bindingDir := filepath.Join(buildDir, "service-bindings", "dynatrace")
				err = os.MkdirAll(bindingDir, 0755)
				Expect(err).To(BeNil())

				os.WriteFile(filepath.Join(bindingDir, "type"), []byte("dynatrace"), 0644)
				os.WriteFile(filepath.Join(bindingDir, "environmentid"), []byte(environmentID), 0644)
				os.WriteFile(filepath.Join(bindingDir, "apitoken"), []byte(apiToken), 0644)
				os.WriteFile(filepath.Join(bindingDir, "apiurl"), []byte("https://example.com"), 0644)

				os.Setenv("SERVICE_BINDING_ROOT", filepath.Join(buildDir, "service-bindings"))

				httpmock.RegisterResponder("GET", "https://example.com/v1/deployment/installer/agent/"+OSName+"/"+InstallationMethod+"/latest?bitness=64&include=nginx&include=process&include=dotnet",
					api_header_check)

				httpmock.RegisterResponder("GET", "https://example.com/v1/deployment/installer/agent/processmoduleconfig",
					api_header_check)
			})

			It("falls through to K8s binding", func() {
				if runtime.GOOS != "windows" {
					mockCommand.EXPECT().Execute("", gomock.Any(), gomock.Any(), gomock.Any(), buildDir).Do(simulateUnixInstaller)
				}

				err = hook.AfterCompile(stager)
				Expect(err).To(BeNil())

				Expect(buffer.String()).To(ContainSubstring("Found one matching service"))
			})
		})

		Context("No credential sources available at all", func() {
			BeforeEach(func() {
				os.Setenv("VCAP_APPLICATION", `{"name":"JimBob"}`)
				os.Setenv("VCAP_SERVICES", "")
				os.Unsetenv("VCAP_SERVICES_FILE_PATH")
				os.Unsetenv("SERVICE_BINDING_ROOT")
			})

			It("does nothing and succeeds", func() {
				err = hook.AfterCompile(stager)
				Expect(err).To(BeNil())

				Expect(buffer.String()).To(Equal(""))
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
