//go:build windows
// +build windows

package dynatrace

import (
	"archive/zip"
	"fmt"
	"io"
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
	if err := h.unzipArchive(installerFilePath, filepath.Join(stager.BuildDir(), installDir)); err != nil {
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

	agentLibPath = "dynatrace/oneagent/agent/lib/oneagentloader.dll"

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
	dynatraceEnvName := "dynatrace-env.cmd"
	dynatraceEnvPath := filepath.Join(stager.DepDir(), "profile.d", dynatraceEnvName)

	err := os.MkdirAll(filepath.Join(stager.DepDir(), "profile.d"), os.ModePerm)
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
	extra += fmt.Sprintf("set COR_PROFILER_PATH_64=%s\n", agentLibPath)

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

func (h *Hook) unzipArchive(src, dest string) error {
	r, err := zip.OpenReader(src)
	if err != nil {
		return err
	}
	defer func() {
		if err := r.Close(); err != nil {
			panic(err)
		}
	}()

	extractAndWriteFile := func(f *zip.File) error {
		rc, err := f.Open()
		if err != nil {
			return err
		}
		defer func() {
			if err := rc.Close(); err != nil {
				panic(err)
			}
		}()

		path := filepath.Join(dest, f.Name)

		if !strings.HasPrefix(path, filepath.Clean(dest)+string(os.PathSeparator)) {
			return fmt.Errorf("illegal file path: %s", path)
		}

		if f.FileInfo().IsDir() {
			os.MkdirAll(path, f.Mode())
		} else {
			os.MkdirAll(filepath.Dir(path), f.Mode())
			f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, f.Mode())
			if err != nil {
				return err
			}
			defer func() {
				if err := f.Close(); err != nil {
					panic(err)
				}
			}()

			_, err = io.Copy(f, rc)
			if err != nil {
				return err
			}
		}
		return nil
	}

	for _, f := range r.File {
		err := extractAndWriteFile(f)
		if err != nil {
			return err
		}
	}

	return nil
}
