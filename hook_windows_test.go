//go:build windows
// +build windows

package dynatrace_test

import (
	"archive/zip"
	"bufio"
	"bytes"
	"net/http"

	"github.com/jarcoal/httpmock"
)

const ScriptFilename = "dynatrace-env.cmd"
const OSName = "windows"
const InstallationMethod = "paas"

func getMockResponse() *http.Response {
	var zipBytes bytes.Buffer
	zipWriter := zip.NewWriter(bufio.NewWriter(&zipBytes))
	writer, _ := zipWriter.Create("agent/lib64/oneagentloader.dll")
	writer.Write([]byte("library"))
	writer, _ = zipWriter.Create("agent/conf/ruxitagentproc.conf")
	writer.Write([]byte("library"))
	zipWriter.Create("agent/dt_fips_disabled.flag")
	writer, _ = zipWriter.Create("manifest.json")
	writer.Write([]byte(manifestJson))
	zipWriter.Close()
	return httpmock.NewBytesResponse(200, zipBytes.Bytes())
}
