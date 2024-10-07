//go:build !windows
// +build !windows

package dynatrace_test

import (
	"net/http"

	"github.com/jarcoal/httpmock"
)

const ScriptFilename = "dynatrace-env.sh"
const OSName = "unix"
const InstallationMethod = "paas-sh"

func getMockResponse() *http.Response {
	return httpmock.NewStringResponse(200, "echo Install Dynatrace")
}
