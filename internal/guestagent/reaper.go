package guestagent

import (
	"os"
	"os/exec"
	"os/signal"
	"sync"
	"syscall"
)

// The reaper owns every wait in this process.
//
// Why: as PID 1, the Guest Agent inherits every orphaned process in the
// Sandbox, and an exited child stays a zombie until its parent waits for it.
// So PID 1 must keep calling wait4(-1). But wait4(-1) collects *any* child,
// including the ones os/exec would wait for, and a status can be collected
// only once. So nothing here calls exec.Cmd.Wait: processes are started
// through start(), and the reaper hands each exit status to whoever started
// that pid. Exits of unknown pids (orphans) are simply discarded.
//
// The reaper is process-wide because wait4(-1) is.
var reaper = struct {
	once sync.Once
	// mu is held around Start+register and around each reaping pass, so a
	// child can't be reaped before its waiter is registered.
	mu      sync.Mutex
	waiters map[int]chan syscall.WaitStatus
}{waiters: map[int]chan syscall.WaitStatus{}}

func startReaper() {
	reaper.once.Do(func() {
		sigchld := make(chan os.Signal, 1)
		signal.Notify(sigchld, syscall.SIGCHLD)
		go func() {
			for range sigchld {
				reapAll()
			}
		}()
	})
}

func reapAll() {
	reaper.mu.Lock()
	defer reaper.mu.Unlock()
	for {
		var ws syscall.WaitStatus
		pid, err := syscall.Wait4(-1, &ws, syscall.WNOHANG, nil)
		if err == syscall.EINTR {
			continue
		}
		if pid <= 0 || err != nil {
			return
		}
		if ch, ok := reaper.waiters[pid]; ok {
			delete(reaper.waiters, pid)
			ch <- ws
		}
	}
}

// start starts cmd and returns a channel that receives its exit status.
// cmd's Stdin, Stdout and Stderr must be nil or *os.File, because os/exec
// only joins its copying goroutines in Wait, which is never called.
func start(cmd *exec.Cmd) (<-chan syscall.WaitStatus, error) {
	startReaper()
	reaper.mu.Lock()
	defer reaper.mu.Unlock()
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	ch := make(chan syscall.WaitStatus, 1)
	reaper.waiters[cmd.Process.Pid] = ch
	return ch, nil
}

// exitCode turns a wait status into a shell-style exit code: the exit status,
// or 128 plus the signal number if a signal ended the process.
func exitCode(ws syscall.WaitStatus) int {
	if ws.Signaled() {
		return 128 + int(ws.Signal())
	}
	return ws.ExitStatus()
}
