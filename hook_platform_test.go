package dynatrace

import (
	"archive/zip"
	"bytes"
	"net/http"
	"runtime"

	"github.com/jarcoal/httpmock"
	. "github.com/onsi/ginkgo"
)

// testOS returns the operating system to simulate in tests, based on the host OS.
// Windows-specific path handling (backslash separators, os.Stat) only works on actual Windows.
var testOS = func() *string { s := defaultTestOS(); return &s }()

func defaultTestOS() string {
	if runtime.GOOS == "windows" {
		return "windows"
	}
	return "linux"
}

var (
	OSName             string
	InstallationMethod string
	ScriptFilename     string
)

var _ = BeforeSuite(func() {
	switch *testOS {
	case "windows":
		OSName = "windows"
		InstallationMethod = "paas"
		ScriptFilename = "dynatrace-env.cmd"
	default: // linux
		OSName = "unix"
		InstallationMethod = "paas-sh"
		ScriptFilename = "dynatrace-env.sh"
	}
})

func getMockResponse() *http.Response {
	if *testOS == "windows" {
		return getWindowsMockResponse()
	}
	return httpmock.NewStringResponse(200, "echo Install Dynatrace")
}

func getWindowsMockResponse() *http.Response {
	var zipBytes bytes.Buffer
	zipWriter := zip.NewWriter(&zipBytes)
	writer, _ := zipWriter.Create("agent/bin/current/windows-x86-64/oneagentdotnet.dll")
	writer.Write([]byte("library"))
	writer, _ = zipWriter.Create("agent/conf/ruxitagentproc.conf")
	writer.Write([]byte("library"))
	zipWriter.Create("agent/dt_fips_disabled.flag")
	writer, _ = zipWriter.Create("manifest.json")
	writer.Write([]byte(manifestJson))
	zipWriter.Close()
	return httpmock.NewBytesResponse(200, zipBytes.Bytes())
}
