//go:build windows
// +build windows

package dynatrace

import (
	"fmt"
	"os"
	"path/filepath"
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
	if err := h.unzipArchive(installerFilePath, filepath.Join(stager.BuildDir(), installDir)); err != nil {
		h.Log.Error("Error during unzipping paas archive")
		return err
	}

	h.Log.Info("Dynatrace OneAgent installed.")

	// Post-installation setup...

	dynatraceEnvName := "dynatrace-env.cmd"
	dynatraceEnvPath := filepath.Join(stager.DepDir(), "profile.d", dynatraceEnvName)
	agentLibPath, err := h.findAgentPath(filepath.Join(stager.BuildDir(), installDir), "oneagentproc.dll", "windows-x86-64")
	if err != nil {
		h.Log.Error("Manifest handling failed!")
		return err
	}

	// windows paths contain "\" instead of "/", so we need do replace them
	agentLibPath = strings.ReplaceAll(agentLibPath, "/", "\\")
	agentBuilderLibPath := filepath.Join(stager.BuildDir(), installDir, agentLibPath)

	if _, err = os.Stat(agentBuilderLibPath); os.IsNotExist(err) {
		h.Log.Error("Agent library (%s) not found!", agentBuilderLibPath)
		return err
	}

	h.Log.BeginStep("Setting up Dynatrace OneAgent injection...")
	h.Log.Debug("Copy %s to %s", dynatraceEnvName, dynatraceEnvPath)
	err = os.MkdirAll(filepath.Join(stager.DepDir(), "profile.d"), os.ModePerm)
	if err != nil {
		return err
	}

	h.Log.Debug("Creating %s...", dynatraceEnvPath)
	f, err := os.OpenFile(dynatraceEnvPath, os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		h.Log.Error("Cannot create dynatrace-env.cmd!")
		return err
	}

	defer f.Close()

	extra := ""
	extra += "set COR_ENABLE_PROFILING=1\n"
	extra += "set COR_PROFILER={B7038F67-52FC-4DA2-AB02-969B3C1EDA03}\n"
	extra += "set DT_AGENTACTIVE=true\n"
	extra += "set DT_LOGLEVEL=DEBUG\n"
	extra += "set DT_LOGLEVELCON=DEBUG\n"
	extra += fmt.Sprintf("set COR_PROFILER_PATH_64=%s\n", agentBuilderLibPath)

	if creds.NetworkZone != "" {
		h.Log.Debug("Setting DT_NETWORK_ZONE...")
		extra += "set DT_NETWORK_ZONE=" + creds.NetworkZone
	}

	h.Log.Debug("Preparing custom properties...")
	extra += fmt.Sprintf(
		"\nset DT_CUSTOM_PROP=\"${DT_CUSTOM_PROP} CloudFoundryBuildpackLanguage=%s CloudFoundryBuildpackVersion=%s\"", lang, ver)

	if _, err = f.WriteString(extra); err != nil {
		return err
	}

	return nil
}
