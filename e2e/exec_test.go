package e2e_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Ticket 04: Exec options, streaming, timeouts and disconnects.

// sseEvent is one Server-Sent Event of a streamed Exec, with when it arrived.
type sseEvent struct {
	name string
	data map[string]any
	at   time.Time
}

// execStream starts a streamed Exec and returns its events as they arrive.
// Cancelling ctx disconnects the client.
func execStream(t *testing.T, ctx context.Context, sb sandbox, body map[string]any) <-chan sseEvent {
	t.Helper()
	b, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, "POST", sb.DataEndpoint+"/v1/sandboxes/"+sb.ID+"/exec", bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Authorization", "Bearer "+sb.AccessToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		resp.Body.Close()
		t.Fatalf("streamed exec: HTTP %d, Content-Type %q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	events := make(chan sseEvent, 1024)
	go func() {
		defer close(events)
		defer resp.Body.Close()
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 1<<20), 4<<20)
		var ev sseEvent
		for sc.Scan() {
			line := sc.Text()
			switch {
			case strings.HasPrefix(line, "event: "):
				ev.name = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &ev.data)
			case line == "" && ev.name != "":
				ev.at = time.Now()
				events <- ev
				ev = sseEvent{}
			}
		}
	}()
	return events
}

// streamAll runs a streamed Exec to its end.
func streamAll(t *testing.T, sb sandbox, body map[string]any) (stdout, stderr string, events []sseEvent) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	var out, errOut strings.Builder
	for ev := range execStream(t, ctx, sb, body) {
		events = append(events, ev)
		switch ev.name {
		case "stdout":
			out.WriteString(ev.data["text"].(string))
		case "stderr":
			errOut.WriteString(ev.data["text"].(string))
		}
	}
	return out.String(), errOut.String(), events
}

func TestExecRunsAsUserByDefault(t *testing.T) {
	api := startPlatform(t, platformOptions{})
	sb := createRunning(t, api)

	// alpine has no `user`; the platform added it at start (ADR 0009).
	res, _ := execIn(t, sb, sb.AccessToken, "whoami; pwd; echo $HOME; stat -c %U:%a /home/user")

	if res.Stdout != "user\n/home/user\n/home/user\nuser:750\n" {
		t.Fatalf("got %+v, want user in its own /home/user", res)
	}
}

func TestExecAsRootWhenRequested(t *testing.T) {
	api := startPlatform(t, platformOptions{})
	sb := createRunning(t, api)

	res, _ := execWith(t, sb, map[string]any{"command": "whoami; pwd", "user": "root"})

	if res.Stdout != "root\n/root\n" {
		t.Fatalf("got %+v, want root in /root", res)
	}
}

func TestExecWorkingDirectoryAndEnvironment(t *testing.T) {
	api := startPlatform(t, platformOptions{})
	sb := createRunning(t, api)

	res, code := execWith(t, sb, map[string]any{
		"command": `pwd; echo "$GREETING"`, "cwd": "/tmp", "env": map[string]string{"GREETING": "hi there"},
	})

	if code != http.StatusOK || res.Stdout != "/tmp\nhi there\n" {
		t.Fatalf("HTTP %d, %+v; want /tmp and the variable", code, res)
	}
}

func TestBadExecOptionsAreRejected(t *testing.T) {
	api := startPlatform(t, platformOptions{})
	sb := createRunning(t, api)

	for name, body := range map[string]map[string]any{
		"timeout over an hour": {"command": "true", "timeout_seconds": 3601},
		"negative timeout":     {"command": "true", "timeout_seconds": -1},
		"unknown user":         {"command": "true", "user": "nobody-here"},
		"missing directory":    {"command": "true", "cwd": "/no/such/dir"},
		"bad variable name":    {"command": "true", "env": map[string]string{"A=B": "x"}},
	} {
		if res, code := execWith(t, sb, body); code != http.StatusBadRequest {
			t.Errorf("%s: HTTP %d, %+v; want 400", name, code, res)
		}
	}
}

func TestSandboxDefaultEnvironmentReachesExecsButNotTheContainerConfig(t *testing.T) {
	api := startPlatform(t, platformOptions{})
	var sb sandbox
	code := call(t, "POST", api+"/v1/sandboxes", "", map[string]any{
		"template": testTemplate, "env": map[string]string{"PROJECT": "gpubox", "MODE": "default"},
	}, &sb)
	if code != http.StatusCreated {
		t.Fatalf("create: HTTP %d, %+v", code, sb)
	}
	t.Cleanup(func() { call(t, "DELETE", api+"/v1/sandboxes/"+sb.ID, "", nil, nil) })

	// An Exec's own variable wins over the Sandbox default.
	res, _ := execWith(t, sb, map[string]any{"command": `echo "$PROJECT $MODE"`, "env": map[string]string{"MODE": "exec"}})

	if res.Stdout != "gpubox exec\n" {
		t.Fatalf("got %+v, want \"gpubox exec\"", res)
	}
	// Init delivered them; `docker commit` could never capture them (ADR 0008).
	got := dockerInspectEnv(t, "container", containerOf(t, sb.ID))
	if want := dockerInspectEnv(t, "image", testTemplate); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("container Env = %q, want exactly the image's %q", got, want)
	}
}

func TestEveryExecRunsInItsOwnProcessGroup(t *testing.T) {
	api := startPlatform(t, platformOptions{})
	sb := createRunning(t, api)

	// Field 5 of /proc/PID/stat is the process group: the shell leads its own.
	res, _ := execIn(t, sb, sb.AccessToken, "echo $$; cut -d' ' -f5 /proc/$$/stat")

	lines := strings.Fields(res.Stdout)
	if len(lines) != 2 || lines[0] != lines[1] || lines[1] == "1" {
		t.Fatalf("shell pid and pgid: %q, want equal (its own group, not PID 1's)", res.Stdout)
	}
}

func TestStreamedExecOutputArrivesWhileTheCommandRuns(t *testing.T) {
	api := startPlatform(t, platformOptions{})
	sb := createRunning(t, api)

	stdout, stderr, events := streamAll(t, sb, map[string]any{"command": "echo first; sleep 1; echo second; echo err >&2; exit 7"})

	if stdout != "first\nsecond\n" || stderr != "err\n" {
		t.Fatalf("stdout=%q stderr=%q", stdout, stderr)
	}
	var firstAt time.Time
	for _, ev := range events {
		if ev.name == "stdout" && ev.data["text"] == "first\n" {
			firstAt = ev.at
		}
	}
	last := events[len(events)-1]
	if last.name != "exit" || last.data["exit_code"] != float64(7) || last.data["timed_out"] != false {
		t.Fatalf("last event %+v, want exit with code 7", last)
	}
	if firstAt.IsZero() || last.at.Sub(firstAt) < 800*time.Millisecond {
		t.Fatalf("\"first\" arrived %s before the end, want about 1 s earlier", last.at.Sub(firstAt))
	}
}

func TestStreamedExecHasNoOutputCap(t *testing.T) {
	api := startPlatform(t, platformOptions{})
	sb := createRunning(t, api)

	// 3 MiB, three times what a buffered Exec keeps.
	stdout, _, events := streamAll(t, sb, map[string]any{"command": "head -c 3145728 /dev/zero | tr '\\0' a"})

	if len(stdout) != 3<<20 || strings.Trim(stdout, "a") != "" {
		t.Fatalf("got %d bytes, want all %d", len(stdout), 3<<20)
	}
	if last := events[len(events)-1]; last.name != "exit" || last.data["exit_code"] != float64(0) {
		t.Fatalf("last event %+v, want exit 0", last)
	}
}

func TestStreamedMultibyteTextSurvivesChunking(t *testing.T) {
	api := startPlatform(t, platformOptions{})
	sb := createRunning(t, api)

	// ~380 KB of two-byte characters: output is read in chunks, and some
	// chunk boundaries fall inside a character.
	stdout, _, _ := streamAll(t, sb, map[string]any{"command": "yes ééééééééé | head -n 20000"})

	if want := strings.Repeat("ééééééééé\n", 20000); stdout != want {
		t.Fatalf("got %d bytes (%d replacement characters), want %d intact bytes",
			len(stdout), strings.Count(stdout, "�"), len(want))
	}
}

// assertNoProcess waits up to 5 s for nothing in the Sandbox to match
// pattern, an extended regex. Write it so it doesn't match itself, as in
// "sleep 30[3]": the shell running pgrep has the pattern in its command line.
func assertNoProcess(t *testing.T, sb sandbox, pattern string) {
	t.Helper()
	var res execResult
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		res, _ = execWith(t, sb, map[string]any{"command": "pgrep -f '" + pattern + "' || true", "user": "root"})
		if strings.TrimSpace(res.Stdout) == "" {
			return
		}
	}
	t.Fatalf("processes matching %q are still running: %q", pattern, res.Stdout)
}

func TestExecTimeoutKillsTheProcessGroupAndIsMarked(t *testing.T) {
	api := startPlatform(t, platformOptions{})
	sb := createRunning(t, api)

	began := time.Now()
	res, code := execWith(t, sb, map[string]any{"command": "sleep 301 & sleep 302", "timeout_seconds": 1})

	if code != http.StatusOK || !res.TimedOut || res.ExitCode != 137 {
		t.Fatalf("HTTP %d, %+v; want timed_out and exit code 137", code, res)
	}
	if took := time.Since(began); took > 10*time.Second {
		t.Errorf("took %s, want about the 1 s timeout", took)
	}
	assertNoProcess(t, sb, "sleep 30[12]")
}

func TestStreamedExecTimeoutIsMarkedInTheLastEvent(t *testing.T) {
	api := startPlatform(t, platformOptions{})
	sb := createRunning(t, api)

	_, _, events := streamAll(t, sb, map[string]any{"command": "echo started; sleep 303", "timeout_seconds": 1})

	if last := events[len(events)-1]; last.name != "exit" || last.data["timed_out"] != true || last.data["exit_code"] != float64(137) {
		t.Fatalf("last event %+v, want exit with timed_out", last)
	}
	assertNoProcess(t, sb, "sleep 30[3]")
}

func TestClientDisconnectEndsTheExecWithNoOrphans(t *testing.T) {
	api := startPlatform(t, platformOptions{})
	sb := createRunning(t, api)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	events := execStream(t, ctx, sb, map[string]any{"command": "sleep 304 & sleep 305 & echo running; wait"})
	for ev := range events {
		if ev.name == "stdout" {
			break
		}
	}
	// The client goes away mid-Exec.
	cancel()

	assertNoProcess(t, sb, "sleep 30[45]")
}
