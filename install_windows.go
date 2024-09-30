//go:build windows
// +build windows

package dynatrace

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/cloudfoundry/libbuildpack"
)

func (h *Hook) downloadAndInstall(creds *credentials, ver string, lang string, installDir string, stager *libbuildpack.Stager) error {
	installerFilePath := filepath.Join(os.TempDir(), "paasInstaller.zip")
	url := h.getDownloadURL(creds, "windows", "paas")

	h.Log.Info("Downloading '%s' to '%s'", url, installerFilePath)
	if err := h.download(url, installerFilePath, ver, lang, creds); err != nil {
		if creds.SkipErrors {
			h.Log.Warning("Error during installer download, skipping installation")
			return nil
		}
		return err
	}

	// Do manual installation...

	h.Log.BeginStep("Starting Dynatrace OneAgent installation")

	h.Log.Info("Unzipping archive '%s' to '%s'", installerFilePath, filepath.Join(stager.BuildDir(), installDir))
	if err := libbuildpack.ExtractZip(installerFilePath, filepath.Join(stager.BuildDir(), installDir)); err != nil {
		h.Log.Error("Error during unzipping paas archive")
		return err
	}

	h.Log.Info("Dynatrace OneAgent installed.")

	// Post-installation setup...

	agentLibPath, err := h.findAgentPath(filepath.Join(stager.BuildDir(), installDir), "oneagentproc.dll", "windows-x86-64")
	if err != nil {
		h.Log.Error("Manifest handling failed!")
		return err
	}

	// windows path separator is "\" instead of "/"
	agentLibPath = strings.ReplaceAll(agentLibPath, "/", "\\")
	agentLibPath = filepath.Join(installDir, agentLibPath)

	agentLibPath = "C:\\users\\vcap\\app\\dynatrace\\oneagent\\agent\\lib64\\oneagentloader.dll"

	agentBuilderLibPath := filepath.Join(stager.BuildDir(), agentLibPath)
	if _, err = os.Stat(agentBuilderLibPath); os.IsNotExist(err) {
		h.Log.Error("Agent library (%s) not found!", agentBuilderLibPath)
		return err
	}

	h.Log.BeginStep("Setting up Dynatrace OneAgent injection...")
	if slices.Contains(h.IncludeTechnologies, "dotnet") {
		err = h.setUpDotNetCorProfilerInjection(creds, ver, lang, agentLibPath, stager)
	} else {
		h.Log.Warning("No injection method available for technology stack")
		return nil
	}
	if err != nil {
		return err
	}

	return nil
}

func (h *Hook) setUpDotNetCorProfilerInjection(creds *credentials, ver string, lang string, agentLibPath string, stager *libbuildpack.Stager) error {
	scriptContent := "set COR_ENABLE_PROFILING=1\n"
	scriptContent += "set COR_PROFILER={B7038F67-52FC-4DA2-AB02-969B3C1EDA03}\n"
	scriptContent += "set DT_AGENTACTIVE=true\n"
	scriptContent += "set DT_BLOCKLIST=powershell*\n"
	scriptContent += fmt.Sprintf("set COR_PROFILER_PATH_32=%s\n", agentLibPath)
	scriptContent += fmt.Sprintf("set COR_PROFILER_PATH_64=%s\n", agentLibPath)

	if creds.NetworkZone != "" {
		h.Log.Debug("Setting DT_NETWORK_ZONE...")
		scriptContent += "set DT_NETWORK_ZONE=" + creds.NetworkZone + "\n"
	}

	h.Log.Debug("Preparing custom properties...")
	scriptContent += fmt.Sprintf("set DT_CUSTOM_PROP=\"%%DT_CUSTOM_PROP%% CloudFoundryBuildpackLanguage=%s CloudFoundryBuildpackVersion=%s\"\n", lang, ver)

	stager.WriteProfileD("dynatrace-env.cmd", scriptContent)

	return nil
}
