// Package e2e drives the platform only through its public API (the Control
// Plane's REST API and the Host data proxy), with a real Docker Runtime.
//
// These tests need Docker and are skipped unless SANDBOX_DOCKER_TESTS=1.
package e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"gpubox/internal/controlplane"
	"gpubox/internal/hostagent"
	"gpubox/internal/runtime/docker"
)

const testTemplate = "alpine:latest"

// guestAgentDir holds the real Guest Agent, cross-built for the Docker VM.
var guestAgentDir string

func TestMain(m *testing.M) {
	if os.Getenv("SANDBOX_DOCKER_TESTS") != "1" {
		fmt.Println("skipping e2e tests: set SANDBOX_DOCKER_TESTS=1 to run them against Docker")
		os.Exit(0)
	}
	dir, err := buildGuestAgent()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	guestAgentDir = dir
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

func buildGuestAgent() (string, error) {
	arch, err := exec.Command("docker", "version", "--format", "{{.Server.Arch}}").Output()
	if err != nil {
		return "", fmt.Errorf("docker version: %w", err)
	}
	dir, err := os.MkdirTemp("", "gpubox-ga")
	if err != nil {
		return "", err
	}
	_, thisFile, _, _ := runtime.Caller(0)
	root := filepath.Dir(filepath.Dir(thisFile))
	cmd := exec.Command("go", "build", "-o", filepath.Join(dir, "guestagent"), "./cmd/guestagent")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GOOS=linux", "GOARCH="+strings.TrimSpace(string(arch)), "CGO_ENABLED=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("build guestagent: %v\n%s", err, out)
	}
	return dir, nil
}

type platformOptions struct {
	guestAgentDir string
	createTimeout time.Duration
}

// startPlatform runs a Host Agent and a Control Plane in this process, wired
// together over a unix socket exactly as the binaries are, and returns the
// Control Plane's base URL.
func startPlatform(t *testing.T, opts platformOptions) string {
	t.Helper()
	if opts.guestAgentDir == "" {
		opts.guestAgentDir = guestAgentDir
	}
	if opts.createTimeout == 0 {
		opts.createTimeout = 60 * time.Second
	}
	dir, err := os.MkdirTemp("", "gpubox")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })

	ha, err := hostagent.Start(hostagent.Config{
		SocketPath: filepath.Join(dir, "hostagent.sock"),
		DataAddr:   "127.0.0.1:0",
		Runtime:    docker.New(docker.Config{GuestAgentDir: opts.guestAgentDir}),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ha.Stop)

	cp, err := controlplane.Start(controlplane.Config{
		Addr:            "127.0.0.1:0",
		DBPath:          filepath.Join(dir, "controlplane.db"),
		HostAgentSocket: filepath.Join(dir, "hostagent.sock"),
		CreateTimeout:   opts.createTimeout,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cp.Stop)
	return "http://" + cp.Addr()
}

// sandbox is the public API's Sandbox object.
type sandbox struct {
	ID                string `json:"id"`
	State             string `json:"state"`
	TerminationReason string `json:"termination_reason"`
	TerminationDetail string `json:"termination_detail"`
	DataEndpoint      string `json:"data_endpoint"`
	AccessToken       string `json:"access_token"`
}

type execResult struct {
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	ExitCode int    `json:"exit_code"`
}

func call(t *testing.T, method, url, token string, body, out any) int {
	t.Helper()
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, url, r)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			t.Fatalf("%s %s: decode %q: %v", method, url, raw, err)
		}
	}
	return resp.StatusCode
}

func createSandbox(t *testing.T, api, template string) (sandbox, int) {
	t.Helper()
	var sb sandbox
	code := call(t, "POST", api+"/v1/sandboxes", "", map[string]string{"template": template}, &sb)
	return sb, code
}

// createRunning creates a Sandbox that must reach Running, and deletes it
// when the test ends.
func createRunning(t *testing.T, api string) sandbox {
	t.Helper()
	sb, code := createSandbox(t, api, testTemplate)
	if code != http.StatusCreated || sb.State != "Running" {
		t.Fatalf("create: HTTP %d, %+v", code, sb)
	}
	t.Cleanup(func() { call(t, "DELETE", api+"/v1/sandboxes/"+sb.ID, "", nil, nil) })
	return sb
}

func execIn(t *testing.T, sb sandbox, token, command string) (execResult, int) {
	t.Helper()
	var res execResult
	code := call(t, "POST", sb.DataEndpoint+"/v1/sandboxes/"+sb.ID+"/exec", token, map[string]string{"command": command}, &res)
	return res, code
}

func TestCreateExecDestroy(t *testing.T) {
	api := startPlatform(t, platformOptions{})

	sb, code := createSandbox(t, api, testTemplate)
	if code != http.StatusCreated || sb.State != "Running" || sb.AccessToken == "" || sb.DataEndpoint == "" {
		t.Fatalf("create: HTTP %d, %+v", code, sb)
	}

	res, code := execIn(t, sb, sb.AccessToken, "echo hi")
	if code != http.StatusOK || res.Stdout != "hi\n" || res.ExitCode != 0 {
		t.Fatalf("exec: HTTP %d, %+v", code, res)
	}

	var deleted sandbox
	if code := call(t, "DELETE", api+"/v1/sandboxes/"+sb.ID, "", nil, &deleted); code != http.StatusOK {
		t.Fatalf("delete: HTTP %d", code)
	}
	var got sandbox
	call(t, "GET", api+"/v1/sandboxes/"+sb.ID, "", nil, &got)
	if got.State != "Terminated" || got.TerminationReason != "deleted" {
		t.Fatalf("after delete: %+v, want Terminated/deleted", got)
	}
}

func TestGuestAgentIsPID1AndNotInTheImage(t *testing.T) {
	api := startPlatform(t, platformOptions{})
	sb := createRunning(t, api)

	res, _ := execIn(t, sb, sb.AccessToken, "tr '\\0' ' ' < /proc/1/cmdline")
	if !strings.HasPrefix(res.Stdout, "/.gpubox/guestagent init") {
		t.Fatalf("PID 1 is %q, want the Guest Agent", res.Stdout)
	}

	// The same image, run without the platform, has no Guest Agent in it.
	out, err := exec.Command("docker", "run", "--rm", "--network", "none", "--entrypoint", "/bin/sh",
		testTemplate, "-c", "test -e /.gpubox && echo present || echo absent").CombinedOutput()
	if err != nil || strings.TrimSpace(string(out)) != "absent" {
		t.Fatalf("plain image: %q, %v; want absent", out, err)
	}
}

func TestGuestAgentIsMountedReadOnly(t *testing.T) {
	api := startPlatform(t, platformOptions{})
	sb := createRunning(t, api)

	// Even root inside the Sandbox can't replace the Guest Agent.
	res, _ := execIn(t, sb, sb.AccessToken, "echo x > /.gpubox/guestagent")

	if res.ExitCode == 0 || !strings.Contains(res.Stderr, "Read-only file system") {
		t.Fatalf("write to the Guest Agent: %+v, want a read-only failure", res)
	}
}

func TestExecWithoutTheRightAccessTokenIsRejected(t *testing.T) {
	api := startPlatform(t, platformOptions{})
	sb := createRunning(t, api)
	other := createRunning(t, api)

	for name, token := range map[string]string{
		"no token":                "",
		"wrong token":             "sat_not-a-real-token",
		"another sandbox's token": other.AccessToken,
	} {
		res, code := execIn(t, sb, token, "echo hi")
		if code != http.StatusUnauthorized || res.Stdout != "" {
			t.Errorf("%s: HTTP %d, %+v; want 401 and nothing run", name, code, res)
		}
	}
}

func TestNonZeroExitCodeComesBackFaithfully(t *testing.T) {
	api := startPlatform(t, platformOptions{})
	sb := createRunning(t, api)

	res, code := execIn(t, sb, sb.AccessToken, "echo partial; echo bad >&2; exit 42")

	if code != http.StatusOK || res.ExitCode != 42 || res.Stdout != "partial\n" || res.Stderr != "bad\n" {
		t.Fatalf("HTTP %d, %+v; want 200, exit 42, stdout \"partial\\n\", stderr \"bad\\n\"", code, res)
	}
}

// getSandbox reads a Sandbox back through the public API.
func getSandbox(t *testing.T, api, id string) sandbox {
	t.Helper()
	var sb sandbox
	if code := call(t, "GET", api+"/v1/sandboxes/"+id, "", nil, &sb); code != http.StatusOK {
		t.Fatalf("get %s: HTTP %d", id, code)
	}
	return sb
}

// assertNoContainer checks the Host was left with nothing for the Sandbox.
func assertNoContainer(t *testing.T, id string) {
	t.Helper()
	out, err := exec.Command("docker", "ps", "--all", "--quiet", "--filter", "label=gpubox.sandbox="+id).Output()
	if err != nil {
		t.Fatal(err)
	}
	if s := strings.TrimSpace(string(out)); s != "" {
		t.Errorf("container left behind for %s: %s", id, s)
	}
}

func TestFailedCreateEndsTerminatedWithAReason(t *testing.T) {
	api := startPlatform(t, platformOptions{})

	sb, code := createSandbox(t, api, "gpubox-test/no-such-template:v0")

	if code < 500 || sb.ID == "" {
		t.Fatalf("create: HTTP %d, %+v; want a 5xx naming the Sandbox", code, sb)
	}
	got := getSandbox(t, api, sb.ID)
	if got.State != "Terminated" || got.TerminationReason != "failed" || got.TerminationDetail == "" {
		t.Fatalf("after failed create: %+v, want Terminated/failed with a detail", got)
	}
	assertNoContainer(t, sb.ID)
}

func TestCreateThatTimesOutEndsTerminatedWithAReason(t *testing.T) {
	// A stand-in Guest Agent that starts but never answers Init.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "guestagent"), []byte("#!/bin/sh\nexec sleep 3600\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	api := startPlatform(t, platformOptions{guestAgentDir: dir, createTimeout: 3 * time.Second})

	sb, code := createSandbox(t, api, testTemplate)

	if code != http.StatusGatewayTimeout || sb.ID == "" {
		t.Fatalf("create: HTTP %d, %+v; want 504 naming the Sandbox", code, sb)
	}
	got := getSandbox(t, api, sb.ID)
	if got.State != "Terminated" || got.TerminationReason != "start_timeout" {
		t.Fatalf("after timed-out create: %+v, want Terminated/start_timeout", got)
	}
	assertNoContainer(t, sb.ID)
}

func dockerInspectEnv(t *testing.T, kind, ref string) []string {
	t.Helper()
	out, err := exec.Command("docker", kind, "inspect", "--format", "{{json .Config.Env}}", ref).Output()
	if err != nil {
		t.Fatalf("docker %s inspect %s: %v", kind, ref, err)
	}
	var env []string
	if err := json.Unmarshal(out, &env); err != nil {
		t.Fatal(err)
	}
	return env
}

func TestContainerConfigHasNoPlatformEnvironment(t *testing.T) {
	api := startPlatform(t, platformOptions{})
	sb := createRunning(t, api)

	// Whatever `docker commit` could capture must be the image's own Env and
	// nothing more (ADR 0008).
	got := dockerInspectEnv(t, "container", containerOf(t, sb.ID))
	want := dockerInspectEnv(t, "image", testTemplate)

	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("container Env = %q, want exactly the image's %q", got, want)
	}
}

// containerOf finds the Sandbox's container by the platform's label.
func containerOf(t *testing.T, id string) string {
	t.Helper()
	out, err := exec.Command("docker", "ps", "--quiet", "--filter", "label=gpubox.sandbox="+id).Output()
	if err != nil || strings.TrimSpace(string(out)) == "" {
		t.Fatalf("no running container for %s: %v", id, err)
	}
	return strings.TrimSpace(string(out))
}

// buildTemplate builds a throwaway local image on top of the test Template.
func buildTemplate(t *testing.T, name, dockerfile string) string {
	t.Helper()
	cmd := exec.Command("docker", "build", "--quiet", "--tag", name, "-")
	cmd.Stdin = strings.NewReader(dockerfile)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("docker build: %v\n%s", err, out)
	}
	t.Cleanup(func() { exec.Command("docker", "image", "rm", "--force", name).Run() })
	return name
}

func TestImageEntrypointAndCmdRunInTheSandbox(t *testing.T) {
	template := buildTemplate(t, "gpubox-test/workload:e2e", `FROM `+testTemplate+`
ENTRYPOINT ["/bin/sh", "-c"]
CMD ["echo started > /tmp/workload-started; exec sleep 3600"]
`)
	api := startPlatform(t, platformOptions{})
	sb, code := createSandbox(t, api, template)
	if code != http.StatusCreated {
		t.Fatalf("create: HTTP %d, %+v", code, sb)
	}
	t.Cleanup(func() { call(t, "DELETE", api+"/v1/sandboxes/"+sb.ID, "", nil, nil) })

	// The entrypoint was started by the Guest Agent and is still its child.
	res, _ := execIn(t, sb, sb.AccessToken, "cat /tmp/workload-started && pgrep -P 1 -x sleep >/dev/null && echo child-of-pid1")

	if res.Stdout != "started\nchild-of-pid1\n" {
		t.Fatalf("got %+v, want the entrypoint's marker and its sleep under PID 1", res)
	}
}

func TestGuestAgentReapsOrphans(t *testing.T) {
	api := startPlatform(t, platformOptions{})
	sb := createRunning(t, api)

	// The inner sh exits at once, so its sleep is re-parented to PID 1.
	// When the sleep exits, only PID 1 can collect it.
	execIn(t, sb, sb.AccessToken, "sh -c 'sleep 0.2 &'")
	time.Sleep(500 * time.Millisecond)
	res, _ := execIn(t, sb, sb.AccessToken, "grep -l '^State:.*Z' /proc/[0-9]*/status | wc -l")

	if strings.TrimSpace(res.Stdout) != "0" {
		t.Fatalf("zombies in the Sandbox: %q, want 0", res.Stdout)
	}
}
