# Performance measurements

Record measurements on the same host, with the same Go build and network
conditions before and after an idle-path change. Each scenario runs for at
least ten minutes. This document contains no baseline values until they have
been collected on a live daemon.

## Scenarios

| ID | Daemon state | Measurements to record |
|---|---|---|
| A | No paired devices; daemon idle | CPU, context switches, syscalls, wakeups/min |
| B | All paired devices connected; nothing playing | CPU, context switches, timer-related syscalls, wakeups/min |
| C | One paired device offline | Retry cadence, CPU, context switches, wakeups/min |
| D | Paired device unseen for more than 24 hours | CPU, context switches, timer-related syscalls, wakeups/min |
| E | Desktop music playing | D-Bus calls/min, CPU, context switches |
| F | Unfiltered `kcd watch` running | Same measurements as B |

| Scenario | Before | After | Date, host, build, notes |
|---|---|---|---|
| A | Pending live measurement | Pending live measurement | |
| B | Pending live measurement | Pending live measurement | |
| C | Pending live measurement | Pending live measurement | |
| D | Pending live measurement | Pending live measurement | |
| E | Pending live measurement | Pending live measurement | |
| F | Pending live measurement | Pending live measurement | |

## Capture

Find the daemon PID, then run the profiler once per scenario:

```sh
pid=$(pgrep -x kcd)
scripts/idle-profile.sh "$pid" 600 /tmp/kcd-profile-scenario-a
```

The script records process identity and, when installed, `pidstat` CPU/context
switch samples and `perf trace` syscall summaries. It falls back to `strace`;
if neither syscall tracer exists, it records that omission. Run `sudo powertop`
separately when system wakeup counts are needed. For goroutine state, capture a
Go pprof goroutine dump if the daemon is running with an explicitly configured
pprof endpoint; kcd does not expose pprof by default.

Do not compare a missing tool's output as zero. Record the tool and any
permission limits alongside the measurements. Keep raw captures outside the
repository and copy only summarized values and relevant conditions here.
