package goprof

import (
	"fmt"
	"os"
	"testing"
	"time"
)

// mockStopper is an object returning stopFxn instances which just remembers the fact they were called and does nothing else
type mockStopper struct {
	called bool
}

const testProfilingDuration = 10 * time.Nanosecond

func (m *mockStopper) fxn() stopFxn {
	return func() {
		m.called = true
	}
}

// mockStarter is an object with start function which does nothing, remembers passed directory and returns constant result
type mockStarter struct {
	profileDir string
}

func (m *mockStarter) fxn(result error) startFxn {
	return func(dir string) error {
		m.profileDir = dir
		return result
	}
}

// mockDumper is an object with dump function which does nothing, remembers passed params and returns constant result
type mockDumper struct {
	profileDir string
	profile    prof
}

func (m *mockDumper) fxn(result error) dumpFxn {
	return func(profile prof, dir string) error {
		m.profileDir = dir
		m.profile = profile
		return result
	}
}

func TestMain(m *testing.M) {
	noLogging := func(format string, args ...interface{}) {}
	SetLogFunction(noLogging)
	os.Exit(m.Run())
}

func TestStopWhenNotRunning(t *testing.T) {
	ourProfilingStateGuard.Lock()
	defer ourProfilingStateGuard.Unlock()
	if path := stopProfiling(); path != "" {
		t.Fatalf("Expected empty string when stopping not running profiling. Got '%s'", path)
	}
}

func TestStopManyTimesWhenNotRunning(t *testing.T) {
	ourProfilingStateGuard.Lock()
	defer ourProfilingStateGuard.Unlock()
	if path := stopProfiling(); path != "" {
		t.Fatalf("Expected empty string when stopping not running profiling. Got '%s'", path)
	}
	if path := stopProfiling(); path != "" {
		t.Fatalf("Expected empty string when stopping not running profiling. Got '%s'", path)
	}
}

func startMockProfiling() (string, error) {
	startTrace, startCPU := &mockStarter{}, &mockStarter{}
	stopTrace, stopCPU := &mockStopper{}, &mockStopper{}
	return doStartProfiling(profileAll, testProfilingDuration, startTrace.fxn(nil), stopTrace.fxn(), startCPU.fxn(nil), stopCPU.fxn(), nil)
}

func TestStartTraceFailed(t *testing.T) {
	ourProfilingStateGuard.Lock()
	defer ourProfilingStateGuard.Unlock()
	startTrace, startCPU := &mockStarter{}, &mockStarter{}
	stopTrace, stopCPU := &mockStopper{}, &mockStopper{}
	dir, err := doStartProfiling(profileAll, testProfilingDuration, startTrace.fxn(fmt.Errorf("test")), stopTrace.fxn(), startCPU.fxn(nil), stopCPU.fxn(), nil)
	defer cancelAutoStop()
	if dir != "" || err == nil {
		t.Fatalf("Start profiling should return error and no dir. I got '%s' and %v", dir, err)
	}
	if profilingInProgress() {
		t.Fatalf("Profiling is running")
	}
	if !stopTrace.called || !stopCPU.called {
		t.Fatalf("Profiles was not stopped")
	}
	if _, statErr := os.Stat(startTrace.profileDir); statErr == nil {
		t.Errorf("Temporary dir for profiling data seem to exist")
	}
}

func TestStartCPUFailed(t *testing.T) {
	ourProfilingStateGuard.Lock()
	defer ourProfilingStateGuard.Unlock()
	startTrace, startCPU := &mockStarter{}, &mockStarter{}
	stopTrace, stopCPU := &mockStopper{}, &mockStopper{}
	dir, err := doStartProfiling(profileAll, testProfilingDuration, startTrace.fxn(nil), stopTrace.fxn(), startCPU.fxn(fmt.Errorf("test")), stopCPU.fxn(), nil)
	defer cancelAutoStop()
	if dir != "" || err == nil {
		t.Fatalf("Start profiling should return error and no dir. I got '%s' and %v", dir, err)
	}
	if profilingInProgress() {
		t.Fatalf("Profiling is running")
	}
	if !stopTrace.called || !stopCPU.called {
		t.Fatalf("Profiles was not stopped")
	}
	if _, statErr := os.Stat(startTrace.profileDir); statErr == nil {
		t.Errorf("Temporary dir '%s' for profiling data seem to exist", startTrace.profileDir)
	}
}

func TestStartHeapJustDumps(t *testing.T) {
	ourProfilingStateGuard.Lock()
	defer ourProfilingStateGuard.Unlock()
	dumper := &mockDumper{}
	dir, err := doStartProfiling(profileHeap, testProfilingDuration, nil, nil, nil, nil, dumper.fxn(nil))
	defer cancelAutoStop()
	if dir == "" || err != nil {
		t.Fatalf("Profiling should start without errors. I got '%s' and %v", dir, err)
	}
	if dumper.profile != profileHeap || dumper.profileDir != dir {
		t.Fatalf("Expecting heap to be dumped to %v, got %#v instead", dir, dumper)
	}
	if profilingInProgress() {
		t.Fatalf("Profiling is running after heap dump")
	}
}

func TestStopFailedToDumpHeap(t *testing.T) {
	ourProfilingStateGuard.Lock()
	defer ourProfilingStateGuard.Unlock()
	startDir, err := startMockProfiling()
	defer cancelAutoStop()
	if startDir == "" || err != nil {
		t.Fatalf("Profiling should be started successfully. I got '%s' and %v", startDir, err)
	}
	defer os.RemoveAll(startDir)
	writeHeap := &mockDumper{}
	stopTrace, stopCPU := &mockStopper{}, &mockStopper{}
	stopDir := doStopProfiling(writeHeap.fxn(fmt.Errorf("test")), stopTrace.fxn(), stopCPU.fxn())
	if stopDir != startDir {
		t.Fatalf("Different dirs for start and stop: '%s' and '%s'", startDir, stopDir)
	}
	if !stopTrace.called || !stopCPU.called {
		t.Fatalf("Profiles was not stopped")
	}
	if profilingInProgress() {
		t.Fatalf("Profiling is running")
	}
}

func TestStopCallsStop(t *testing.T) {
	ourProfilingStateGuard.Lock()
	defer ourProfilingStateGuard.Unlock()
	startDir, err := startMockProfiling()
	defer cancelAutoStop()
	if startDir == "" || err != nil {
		t.Fatalf("Profiling should be started successfully. I got '%s' and %v", startDir, err)
	}
	defer os.RemoveAll(startDir)
	writeHeap := &mockDumper{}
	stopTrace, stopCPU := &mockStopper{}, &mockStopper{}
	stopDir := doStopProfiling(writeHeap.fxn(nil), stopTrace.fxn(), stopCPU.fxn())
	if stopDir != startDir {
		t.Fatalf("Different dirs for start and stop: '%s' and '%s'", startDir, stopDir)
	}
	if !stopTrace.called || !stopCPU.called {
		t.Fatalf("Profiles was not stopped")
	}
	if writeHeap.profile != profileHeap {
		t.Fatalf("Heap profile wasn't written on stop. Got %v instead", writeHeap.profile)
	}
	if profilingInProgress() {
		t.Fatalf("Profiling is running")
	}
}
