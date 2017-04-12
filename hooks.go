package goprof

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"runtime/pprof"
	"runtime/trace"
	"sync"
	"time"
)

var (
	// if we are writing profile(s) right now, we keep here the info about the profile in this variable
	ourCurrentProfile  *prof
	ourWrittenProfiles []prof = make([]prof, 0)
	// any changes to profiling state (start, stop) and corresponding changes to profiles directory variable
	// should be done with this mutex hold
	ourProfilingStateGuard = &sync.RWMutex{}
	// at the time it's possible to have only one goroutine waiting for stopping profiling by timeout
	// we use the channel for stopping that goroutine and cancelling autostopping
	ourCancelAutostop chan bool
)

type prof struct {
	Prof     profName      `json:"prof_name"` // which profile is this related to
	Dir      string        `json:"dir"`       // directory where profiles will be placed
	Start    time.Time     `json:"start"`     // profile start time
	Duration time.Duration `json:"duration"`  // how long did profile writing lasted, zero if profile is one-off
}

type profName string

const (
	profileCPU          profName = "cpu"
	profileTrace        profName = "trace"
	profileGoroutine    profName = "goroutine"
	profileThreadcreate profName = "threadcreate"
	profileHeap         profName = "heap"
	profileBlock        profName = "block"
	profileAll          profName = "all"
)

// OneOff returns true if profile is being written constantly and we don't need to start it manually
// everything we can do with such profiles is to dump current state to some file
func (p profName) OneOff() bool {
	switch p {
	case profileGoroutine, profileThreadcreate, profileHeap, profileBlock:
		return true
	}
	return false
}

const (
	defautMaxProfilingDuration = 5 * time.Minute // max duration for profiling process. When this duration exceeds we stop profiling automatically
	// following constants define names of files inside profiles directory
	traceFileName      = "trace"
	cpuProfileFileName = "cpu-profile"
)

// these types used for mocking functions which start/stop profiling
type dumpFxn func(profile profName, dir string) error
type startFxn func(profilesDir string) error
type stopFxn func()

// StartProfiling starts writing profiles and automatically stops it after 5 minutes if not stopped yet
// It returns path to the directory where they will be placed
// if anything goes wrong, corresponding error is returned and no profiling is started
// If writing profiles is in progress it returns an error
func startProfiling(profile profName) (profilesDirectory string, err error) {
	switch profile {
	case profileCPU, profileTrace, profileGoroutine, profileThreadcreate, profileHeap, profileBlock, profileAll: // ok
	default:
		return "", fmt.Errorf("unknown profile: '%v'", profile)
	}
	return doStartProfiling(profile, defautMaxProfilingDuration, startWritingTrace, trace.Stop, startCPUProfiling, pprof.StopCPUProfile, dumpProfile)
}

// stopProfiling stops writing all profiles. Before stopping it tries to write a heap dump
// to the same folder where the other profiles are kept. It returns path to the folder which contains just written profiling files
// If profiling is not in progress, this method does nothing and returns empty string
func stopProfiling() (profilesDirectory string) {
	return doStopProfiling(dumpProfile, trace.Stop, pprof.StopCPUProfile)
}

func profilingInProgress() bool {
	return ourCurrentProfile != nil
}

func doStartProfiling(profile profName, maxProfilingDuration time.Duration,
	startWritingTrace startFxn, stopWritingTrace stopFxn, startCPUProfiling startFxn, stopCPUProfiling stopFxn,
	dumpProfile dumpFxn) (profilesDirectory string, err error) {
	if profilingInProgress() {
		return "", fmt.Errorf("cannot start profiling, since it's already started")
	}
	profilesDir, err := ioutil.TempDir("", fmt.Sprintf("prof-%v", profile))
	if err != nil {
		return "", err
	}
	// don't show that we are "writing profiles..." when user wants heap profile:
	// it confuses people, they think heap profile works as cpu profile and collects data during recording time
	if profile.OneOff() {
		err := dumpProfile(profile, profilesDir)
		if err != nil {
			return "", fmt.Errorf("failed to write heap profile: %v", err)
		}
		ourWrittenProfiles = append(ourWrittenProfiles, prof{
			Prof:  profile,
			Dir:   profilesDir,
			Start: time.Now(),
		})
		return profilesDir, nil
	}
	// if we failed to start profiling we do cleanup finally
	defer func() {
		if err != nil {
			if profile == profileTrace || profile == profileAll {
				stopWritingTrace()
			}
			if profile == profileCPU || profile == profileAll {
				stopCPUProfiling()
			}
			ourCurrentProfile = nil
			if removeErr := os.RemoveAll(profilesDir); removeErr != nil {
				logf("Failed to remove %v: %v", profilesDir, removeErr)
			}
			logf("Failed to start writing profiles: %v", err)
		}
	}()
	if profile == profileTrace || profile == profileAll {
		if err := startWritingTrace(profilesDir); err != nil {
			return "", err
		}
	}
	if profile == profileCPU || profile == profileAll {
		if err := startCPUProfiling(profilesDir); err != nil {
			return "", err
		}
	}
	ourCancelAutostop = make(chan bool, 1)
	go func(cancelAutostop chan bool) {
		select {
		case <-time.After(maxProfilingDuration):
			ourProfilingStateGuard.Lock()
			defer ourProfilingStateGuard.Unlock()
			// this meaningless assignment makes gohint happy
			_ = doStopProfiling(dumpProfile, stopWritingTrace, stopCPUProfiling)
		case <-cancelAutostop:
			return
		}
	}(ourCancelAutostop)
	ourCurrentProfile = &prof{
		Prof:  profile,
		Dir:   profilesDir,
		Start: time.Now(),
	}
	logf("Start writing %v profiles to '%s'", profile, ourCurrentProfile.Dir)
	return profilesDir, nil
}

func cancelAutoStop() {
	select {
	case ourCancelAutostop <- true:
	// cancelled autostop
	default:
		// no running autostop, fine too
	}
}

func doStopProfiling(dumpProfile dumpFxn, stopTrace, stopCPU stopFxn) (profilesDirectory string) {
	cancelAutoStop()
	if !profilingInProgress() {
		return ""
	}
	if ourCurrentProfile.Prof == profileAll {
		if err := dumpProfile(profileHeap, ourCurrentProfile.Dir); err != nil {
			logf("Failed to write heap profile: %v", err)
		}
	}
	// stop everything no matter whether we succeeded with heap profile
	// our main goal here is to stop, so, do it
	if ourCurrentProfile.Prof == profileCPU || ourCurrentProfile.Prof == profileAll {
		stopCPU()
	}
	if ourCurrentProfile.Prof == profileTrace || ourCurrentProfile.Prof == profileAll {
		stopTrace()
	}
	logf("Stop writing profiles to '%s'", ourCurrentProfile.Dir)
	ourCurrentProfile.Duration = time.Since(ourCurrentProfile.Start)
	ourWrittenProfiles = append(ourWrittenProfiles, *ourCurrentProfile)
	profilesDirectory = ourCurrentProfile.Dir
	ourCurrentProfile = nil
	return profilesDirectory
}

func startWritingTrace(profilesDir string) error {
	traceFile, err := os.Create(filepath.Join(profilesDir, traceFileName))
	if err != nil {
		return err
	}
	return trace.Start(traceFile)
}

func dumpProfile(profile profName, profilesDir string) error {
	file, err := os.Create(filepath.Join(profilesDir, fmt.Sprintf("%v-profile", profile)))
	if err != nil {
		return err
	}
	defer file.Close()
	return pprof.Lookup(string(profile)).WriteTo(file, 0)
}

func startCPUProfiling(profilesDir string) error {
	cpuProfileFile, err := os.Create(filepath.Join(profilesDir, cpuProfileFileName))
	if err != nil {
		return err
	}
	return pprof.StartCPUProfile(cpuProfileFile)
}
