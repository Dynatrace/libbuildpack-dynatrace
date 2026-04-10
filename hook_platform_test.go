package dynatrace

import (
	"archive/zip"
	"bufio"
	"bytes"
	"flag"
	"net/http"
	"runtime"

	"github.com/jarcoal/httpmock"
	. "github.com/onsi/ginkgo"
)

// testOS can be overridden on the command line to test against a different OS:
//
//	go test ./... -os=windows
var testOS = flag.String("os", defaultTestOS(), "operating system to simulate in tests (linux or windows)")

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
	zipWriter := zip.NewWriter(bufio.NewWriter(&zipBytes))
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
