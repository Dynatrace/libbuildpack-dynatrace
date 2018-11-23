package dynatrace

import (
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cloudfoundry/libbuildpack"
)

// Command is used to mock around libbuildpack.Command
type Command interface {
	Execute(string, io.Writer, io.Writer, string, ...string) error
}

// Credentials represent the user settings extracted from the environment.
type Credentials struct {
	ServiceName   string
	EnvironmentID string
	APIToken      string
	APIURL        string
	SkipErrors    bool
}

// Hook implements libbuildpack.Hook. It downloads and install the Dynatrace PaaS OneAgent.
type Hook struct {
	libbuildpack.DefaultHook
	Log                 *libbuildpack.Logger
	Command             Command
	IncludeTechnologies []string
	MaxDownloadRetries  int
}

// AfterCompile downloads and installs the Dynatrace agent.
func (h *Hook) AfterCompile(stager *libbuildpack.Stager) error {
	var err error

	h.Log.Debug("Checking for enabled dynatrace service...")

	credentials, found := h.getCredentials()
	if !found {
		h.Log.Debug("Dynatrace service credentials not found!")
		return nil
	}

	h.Log.Info("Dynatrace service credentials found. Setting up Dynatrace PaaS agent.")

	installerPath := filepath.Join(os.TempDir(), "paasInstaller.sh")
	url := h.getDownloadURL(credentials)

	h.Log.Info("Downloading '%s' to '%s'", url, installerPath)
	if err = h.download(url, installerPath); err != nil {
		if credentials.SkipErrors {
			h.Log.Warning("Error during installer download, skipping installation")
			return nil
		}
		return err
	}

	h.Log.Debug("Making %s executable...", installerPath)
	os.Chmod(installerPath, 0755)

	h.Log.BeginStep("Starting Dynatrace PaaS agent installer")

	if os.Getenv("BP_DEBUG") != "" {
		err = h.Command.Execute("", os.Stdout, os.Stderr, installerPath, stager.BuildDir())
	} else {
		err = h.Command.Execute("", ioutil.Discard, ioutil.Discard, installerPath, stager.BuildDir())
	}
	if err != nil {
		return err
	}

	h.Log.Info("Dynatrace PaaS agent installed.")

	dynatraceEnvName := "dynatrace-env.sh"
	installDir := filepath.Join("dynatrace", "oneagent")
	dynatraceEnvPath := filepath.Join(stager.DepDir(), "profile.d", dynatraceEnvName)
	agentLibPath, err := h.findAgentPath(filepath.Join(stager.BuildDir(), installDir))
	if err != nil {
		h.Log.Error("Manifest handling failed!")
		return err
	}

	agentLibPath = filepath.Join(installDir, agentLibPath)
	agentBuilderLibPath := filepath.Join(stager.BuildDir(), agentLibPath)

	if _, err = os.Stat(agentBuilderLibPath); os.IsNotExist(err) {
		h.Log.Error("Agent library (%s) not found!", agentBuilderLibPath)
		return err
	}

	h.Log.BeginStep("Setting up Dynatrace PaaS agent injection...")
	h.Log.Debug("Copy %s to %s", dynatraceEnvName, dynatraceEnvPath)
	if err = libbuildpack.CopyFile(filepath.Join(stager.BuildDir(), installDir, dynatraceEnvName), dynatraceEnvPath); err != nil {
		return err
	}

	h.Log.Debug("Open %s for modification...", dynatraceEnvPath)
	f, err := os.OpenFile(dynatraceEnvPath, os.O_APPEND|os.O_WRONLY, os.ModeAppend)
	if err != nil {
		return err
	}

	defer f.Close()

	extra := ""

	h.Log.Debug("Setting LD_PRELOAD...")
	extra += fmt.Sprintf("\nexport LD_PRELOAD=${HOME}/%s", agentLibPath)

	// By default, OneAgent logs are printed to stderr. If the customer doesn't override this behavior through an
	// environment variable, then we change the default output to stdout.
	if os.Getenv("DT_LOGSTREAM") == "" {
		h.Log.Debug("Setting DT_LOGSTREAM to stdout...")
		extra += "\nexport DT_LOGSTREAM=stdout"
	}

	if _, err = f.WriteString(extra); err != nil {
		return err
	}

	h.Log.Info("Dynatrace PaaS agent injection is set up.")

	return nil
}

func (h *Hook) getCredentials() (*Credentials, bool) {
	var vcapServices map[string][]struct {
		Name        string                 `json:"name"`
		Credentials map[string]interface{} `json:"credentials"`
	}

	if err := json.Unmarshal([]byte(os.Getenv("VCAP_SERVICES")), &vcapServices); err != nil {
		h.Log.Debug("Failed to unmarshal VCAP_SERVICES: %s", err)
		return nil, false
	}

	var detectedCredentials []*Credentials

	for _, services := range vcapServices {
		for _, service := range services {
			if !strings.Contains(service.Name, "dynatrace") {
				continue
			}

			queryString := func(key string) string {
				if value, ok := service.Credentials[key].(string); ok {
					return value
				}
				return ""
			}

			credentials := &Credentials{
				ServiceName:   service.Name,
				EnvironmentID: queryString("environmentid"),
				APIToken:      queryString("apitoken"),
				APIURL:        queryString("apiurl"),
				SkipErrors:    queryString("skiperrors") == "true",
			}

			if credentials.EnvironmentID != "" && credentials.APIToken != "" {
				detectedCredentials = append(detectedCredentials, credentials)
			}
		}
	}

	if len(detectedCredentials) == 1 {
		h.Log.Debug("Found one matching service: %s", detectedCredentials[0].ServiceName)
		return detectedCredentials[0], true
	}

	if len(detectedCredentials) > 1 {
		h.Log.Warning("More than one matching service found!")
	}

	return nil, false
}

func (h *Hook) appName() string {
	var application struct {
		Name string `json:"name"`
	}

	if err := json.Unmarshal([]byte(os.Getenv("VCAP_APPLICATION")), &application); err != nil {
		return ""
	}

	return application.Name
}

func (h *Hook) download(url, installerPath string) error {
	const baseWaitTime = 3 * time.Second

	out, err := os.Create(installerPath)
	if err != nil {
		return err
	}
	defer out.Close()

	for i := 0; ; i++ {
		var resp *http.Response
		if resp, err = http.Get(url); err == nil {
			// TODO: are partial writes possible?
			_, err = io.Copy(out, resp.Body)
			resp.Body.Close()

			if resp.StatusCode < 400 && err == nil {
				return nil
			}

			h.Log.Debug("Download returned with status %s, error: %v", resp.Status, err)

			if i == h.MaxDownloadRetries {
				h.Log.Warning("Maximum number of retries attempted: %d", h.MaxDownloadRetries)
				return fmt.Errorf("Download returned with status %s, error: %v", resp.Status, err)
			}
		} else {
			h.Log.Debug("Download failed: %v", err)

			if i == h.MaxDownloadRetries {
				h.Log.Warning("Maximum number of retries attempted: %d", h.MaxDownloadRetries)
				return err
			}
		}

		waitTime := baseWaitTime + time.Duration(math.Pow(2, float64(i)))*time.Second
		h.Log.Warning("Error during installer download, retrying in %v", waitTime)
		time.Sleep(waitTime)
	}
}

func (h *Hook) getDownloadURL(c *Credentials) string {
	apiURL := c.APIURL
	if apiURL == "" {
		apiURL = fmt.Sprintf("https://%s.live.dynatrace.com/api", c.EnvironmentID)
	}

	u, err := url.ParseRequestURI(fmt.Sprintf("%s/v1/deployment/installer/agent/unix/paas-sh/latest", apiURL))
	if err != nil {
		return ""
	}

	qv := make(url.Values)
	qv.Add("Api-Token", c.APIToken)
	qv.Add("bitness", "64")
	for _, t := range h.IncludeTechnologies {
		qv.Add("include", t)
	}
	u.RawQuery = qv.Encode() // Parameters will be sorted by key.

	return u.String()
}

// findAgentPath reads the manifest file included in the PaaS agent package, and looks
// for the process agent file path.
func (h *Hook) findAgentPath(installDir string) (string, error) {
	type Binary struct {
		Path       string `json:"path"`
		BinaryType string `json:"binarytype,omitempty"`
	}

	type Architecture map[string][]Binary
	type Technologies map[string]Architecture

	type Manifest struct {
		Technologies Technologies `json:"technologies"`
	}

	fallbackPath := filepath.Join("agent", "lib64", "liboneagentproc.so")

	manifestPath := filepath.Join(installDir, "manifest.json")
	if _, err := os.Stat(manifestPath); os.IsNotExist(err) {
		h.Log.Info("manifest.json not found, using fallback!")
		return fallbackPath, nil
	}

	var manifest Manifest

	if raw, err := ioutil.ReadFile(manifestPath); err != nil {
		return "", err
	} else if err = json.Unmarshal(raw, &manifest); err != nil {
		return "", err
	}

	for _, binary := range manifest.Technologies["process"]["linux-x86-64"] {
		if binary.BinaryType == "primary" {
			return binary.Path, nil
		}
	}

	// Using fallback path.
	h.Log.Warning("Agent path not found in manifest.json, using fallback!")
	return fallbackPath, nil
}
