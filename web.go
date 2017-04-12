package goprof

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kardianos/osext"
)

const writtenProfilesRawTemplate = `<!doctype html>
<html lang=en>
<head>
	<meta charset=utf-8>
	<title>Profiling tools</title>
</head>
<body>
	{{ if .Message }}<p>{{ .Message }}</p>{{ end }}
	{{ if .CurrentProfile }}
		<p>Writing {{ .CurrentProfile.Prof }} profile to {{ .CurrentProfile.Dir }} <a href="toggle?enable=0">Stop</a>. Started <span id="started-ago"></span>.</p>
		<script>
		startedAgo = {{ .ProfileStartedSecondsAgo }};
		updateStartedAgoUI = function() {
		  sec = startedAgo % 60;
		  min = Math.floor(startedAgo / 60);
		  durationStr = "";
		  if (min > 0) {durationStr+=min + "min";}
		  if (sec > 0) {durationStr+=" " + sec + "sec";}
		  if (durationStr === "") {durationStr="just now";}else{durationStr+=" ago";}
		  document.getElementById("started-ago").innerHTML = durationStr;
		}
		updateStartedAgoUI();
		window.setInterval(function(){
		  updateStartedAgoUI();
		  startedAgo++;
		}, 1000);
		</script>
	{{ else }}
		<p>Start profiling:
		  <a href="toggle?enable=1&profile=all">all</a>
		  <a href="toggle?enable=1&profile=cpu">cpu</a>
		  <a href="toggle?enable=1&profile=heap">heap (allocations since last gc)</a>
		  <a href="toggle?enable=1&profile=trace">trace</a>
		  <a href="toggle?enable=1&profile=goroutine">goroutine</a>
		  <a href="toggle?enable=1&profile=threadcreate">threadcreate</a>
		  <a href="toggle?enable=1&profile=block">block</a>
		</p>
	{{ end }}
	<p>
	Written profiles:
	<ul>
	{{ range .WrittenProfiles }}
    	<li><a href="{{ download .Dir }}">
          {{ .Prof }}
          {{ if .Prof.OneOff }}
            ({{.Start}})
          {{ else }}
            (lasted for {{.Duration}} since {{.Start}})
          {{ end }}
    	</a>
    {{ else }}
      <li>none
	{{ end }}
	</ul>
	</p>
</body>
</html>`

const showWebScriptTpl = `#!/bin/bash
cd $(dirname $0)
go tool pprof -web {{bin}} {{profile}}`

type ProfileListResponse struct {
	OK    bool   `json:"ok"`
	Items []prof `json:"items"`
}

type SimpleResponse struct {
	OK           bool   `json:"ok"`
	ErrorMessage string `json:"error_message,omitempty"`
}

var (
	writtenProfilesTemplate = template.Must(template.New("profiles").Funcs(template.FuncMap{
		"download": formatDownloadURL,
	}).Parse(writtenProfilesRawTemplate))
)

func formatDownloadURL(path string) string {
	return fmt.Sprintf("download/%s.tgz?path=%s", filepath.Base(path), path)
}

func isJsonRequest(r *http.Request) bool {
	if r.URL.Query().Get("json") == "1" {
		return true
	}

	values := make(map[string]interface{})
	for _, val := range strings.Split(r.Header.Get("accept"), ",") {
		values[val] = struct{}{}
	}
	_, ok := values["application/json"]

	return ok
}

func fatalError(w http.ResponseWriter, r *http.Request, errorMessage string) {
	w.WriteHeader(http.StatusBadRequest)

	if isJsonRequest(r) {
		w.Header().Add("Content-Type", "application/json")
		encoder := json.NewEncoder(w)
		res := SimpleResponse{
			OK:           false,
			ErrorMessage: errorMessage,
		}
		encoder.Encode(res)
	} else {
		http.Error(w, errorMessage, http.StatusBadRequest)
	}
}

func flashError(w http.ResponseWriter, r *http.Request, errorMessage string) {
	w.WriteHeader(http.StatusBadRequest)

	if isJsonRequest(r) {
		w.Header().Add("Content-Type", "application/json")
		encoder := json.NewEncoder(w)
		encoder.Encode(SimpleResponse{
			OK: false,
			ErrorMessage: errorMessage,
		})
	} else {
		renderPage(w, errorMessage)
	}
}

func success(w http.ResponseWriter, r *http.Request) {
	w.Header().Add("Content-Type", "application/json")
	if isJsonRequest(r) {
		encoder := json.NewEncoder(w)
		encoder.Encode(SimpleResponse{
			OK: true,
		})
	} else {
		w.Header().Add("Content-Type", "text/html")
		renderPage(w, "")
	}
}

// handler for toggling profiling. Expects mandatory parameter 'enable' which should be either '0' or '1'
func toggleProfiling(w http.ResponseWriter, r *http.Request) {
	ourProfilingStateGuard.Lock()
	defer ourProfilingStateGuard.Unlock()

	query := r.URL.Query()
	enableParam := query.Get("enable")
	if enableParam != "0" && enableParam != "1" {
		fatalError(w, r, fmt.Sprintf("Bad value for mandatory 'enable' param: '%v'. Please, use 0 or 1.", enableParam))
		return
	}

	enableProfiling := enableParam == "1"
	var dir string
	var err error
	if enableProfiling {
		profile := profName(query.Get("profile"))
		dir, err = startProfiling(profile)
	} else {
		dir = stopProfiling()
	}
	if err != nil {
		flashError(w, r, fmt.Sprintf("Failed to toggle profiling (enable=%v): %v", enableProfiling, err))
		return
	}

	if enableProfiling {
		success(w, r)
		return
	}
	if dir == "" {
		flashError(w, r,"Seems profiling already stopped")
		return
	}
	success(w, r)
}


// handler for downloading written profile files and binary as a single tar.gz archive
// Expects 'path' parameter to point to existing directory with profiles
// If any file is not found (binary or any of profiles) it returns an error
func downloadProfile(w http.ResponseWriter, r *http.Request) {
	// check mandatory param
	profilesDir := r.URL.Query().Get("path")
	if profilesDir == "" {
		fatalError(w, r, "No such profile (param 'path' is mandatory)")
		return
	}
	// check that we aren't writing the profile at the moment
	ourProfilingStateGuard.RLock()
	defer ourProfilingStateGuard.RUnlock()
	if ourCurrentProfile != nil && ourCurrentProfile.Dir == profilesDir {
		flashError(w, r, "We write the requested profile at the moment. Stop it first, then you will be able to download it")
		return

	}
	// check that the param is an accessible directory
	fileInfo, err := os.Stat(profilesDir)
	if err != nil {
		fatalError(w, r, fmt.Sprintf("Cannot stat '%v': %v", profilesDir, err))
		return
	}
	if !fileInfo.IsDir() {
		fatalError(w, r,  fmt.Sprintf("Expecting '%v' to be a directory, but it is not", profilesDir))
		return
	}
	// pack archive and send it to the client
	archive, err := packProfiles(profilesDir)
	if err != nil {
		fatalError(w, r, fmt.Sprintf("Failed to pack profiles: %v", err))
		return
	}
	_, err = io.Copy(w, archive)
	if err != nil {
		fatalError(w, r, fmt.Sprintf( "Failed serve archive: %v", err))
		return
	}
}

func packProfiles(profilesDir string) (*bytes.Buffer, error) {
	archiveBytes := &bytes.Buffer{}
	gz := gzip.NewWriter(archiveBytes)
	defer gz.Close()
	archive := tar.NewWriter(gz)
	defer archive.Close()
	binary, err := osext.Executable()
	if err != nil {
		return nil, err
	}
	if err := writeFile(archive, binary); err != nil {
		return nil, err
	}
	children, err := ioutil.ReadDir(profilesDir)
	if err != nil {
		return nil, fmt.Errorf("failed to ls '%v': %v", profilesDir, err)
	}
	for _, child := range children {
		childName := filepath.Join(profilesDir, child.Name())
		if err := writeFile(archive, childName); err != nil {
			return nil, fmt.Errorf("failed to write %v: %v", childName, err)
		}
	}
	dirname := filepath.Base(profilesDir)
	if !strings.HasPrefix("prof-all", dirname) && !strings.HasPrefix("prof-trace", dirname) && len(children) == 1 {
		binName := filepath.Base(binary)
		profileName := children[0].Name()
		withBinary := strings.Replace(showWebScriptTpl, "{{bin}}", binName, -1)
		scriptSrc := strings.Replace(withBinary, "{{profile}}", profileName, -1)
		tmpDir, err := ioutil.TempDir("", "")
		if err != nil {
			return nil, fmt.Errorf("failed to create temp dir: %v", err)
		}
		tmpFile, err := os.Create(filepath.Join(tmpDir, "show-web"))
		if err != nil {
			return nil, fmt.Errorf("failed to create file in temp dir %v: %v", tmpDir, err)
		}
		defer os.RemoveAll(tmpDir)
		err = tmpFile.Chmod(0777)
		if err != nil {
			return nil, fmt.Errorf("failed to chmod temp file %v: %v", tmpFile, err)
		}
		_, err = tmpFile.WriteString(scriptSrc)
		if err != nil {
			return nil, fmt.Errorf("failed to write temp file %v: %v", tmpFile, err)
		}
		if err := writeFile(archive, tmpFile.Name()); err != nil {
			return nil, fmt.Errorf("failed to write %v: %v", tmpFile.Name(), err)
		}
	}
	return archiveBytes, nil
}

// write a single file into the provided archive
func writeFile(archive *tar.Writer, filePath string) error {
	fileInfo, err := os.Stat(filePath)
	if err != nil {
		return err
	}
	header, err := tar.FileInfoHeader(fileInfo, "")
	if err != nil {
		return err
	}
	if err := archive.WriteHeader(header); err != nil {
		return err
	}
	file, err := os.Open(filePath)
	if err != nil {
		return err
	}
	if _, err := io.Copy(archive, file); err != nil {
		return err
	}
	return nil
}

// showWrittenProfiles renders page with list of all written profiles
func showWrittenProfiles(w http.ResponseWriter, r *http.Request) {
	ourProfilingStateGuard.RLock()
	defer ourProfilingStateGuard.RUnlock()

	if isJsonRequest(r) {
		resp := ProfileListResponse{
			OK:    true,
			Items: ourWrittenProfiles,
		}
		w.Header().Add("Content-Type", "application/json")
		encoder := json.NewEncoder(w)
		encoder.Encode(resp)
	} else {
		renderPage(w, "")
	}
}

func renderPage(w http.ResponseWriter, msg string) {
	templateData := struct {
		WrittenProfiles          []prof
		CurrentProfile           *prof
		Message                  string
		ProfileStartedSecondsAgo int
	}{ourWrittenProfiles, ourCurrentProfile, msg, 0}
	if ourCurrentProfile != nil {
		templateData.ProfileStartedSecondsAgo = int(time.Since(ourCurrentProfile.Start).Seconds())
	}
	err := writtenProfilesTemplate.Execute(w, templateData)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprintf(w, "Failed to render template: %v\n", err)
	}
}

// ListenAndServe starts server on provided address for toggling profiling and downloading results
func ListenAndServe(address string) error {
	return http.ListenAndServe(address, NewHandler())
}

// NewHandler creates http handler for the whole profiling tools application
// If you want to use it aside of other handlers, don't miss http.StripPrefix wrapping like
//   mux.Handle("/pprof/", http.StripPrefix("/pprof", goprof.NewHandler()))
func NewHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", showWrittenProfiles)
	mux.HandleFunc("/toggle", toggleProfiling)
	mux.HandleFunc("/download/", downloadProfile)
	return mux
}
