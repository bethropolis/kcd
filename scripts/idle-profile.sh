#!/bin/sh
set -eu

usage() {
	printf 'Usage: %s PID [seconds] [output-directory]\n' "$0" >&2
	printf 'Each run must last at least 600 seconds (10 minutes).\n' >&2
}

if [ "$#" -lt 1 ] || [ "$#" -gt 3 ]; then
	usage
	exit 2
fi

pid=$1
seconds=${2:-600}
stamp=$(date -u +%Y%m%dT%H%M%SZ)
out_dir=${3:-"$PWD/kcd-idle-profile-$stamp"}

case "$pid" in *[!0-9]*|'') usage; exit 2 ;; esac
case "$seconds" in *[!0-9]*|'') usage; exit 2 ;; esac
if [ "$seconds" -lt 600 ]; then usage; exit 2; fi
if ! kill -0 "$pid" 2>/dev/null; then
	printf 'Process %s is not running.\n' "$pid" >&2
	exit 1
fi

mkdir -p "$out_dir"
printf 'Profiling PID %s for %s seconds. Results: %s\n' "$pid" "$seconds" "$out_dir"
ps -p "$pid" -o pid=,comm=,args= >"$out_dir/process.txt"

if command -v pidstat >/dev/null 2>&1; then
	interval=60
	count=$(( (seconds + interval - 1) / interval ))
	pidstat -h -u -w -p "$pid" "$interval" "$count" >"$out_dir/pidstat.txt" &
	pidstat_pid=$!
else
	printf 'pidstat not installed; CPU and context-switch samples omitted.\n' >"$out_dir/pidstat.txt"
	pidstat_pid=
fi

started=$(date +%s)
if command -v perf >/dev/null 2>&1; then
	perf trace -s -p "$pid" -- sleep "$seconds" >"$out_dir/perf-trace.txt" 2>&1 || true
elif command -v strace >/dev/null 2>&1; then
	strace -f -c -p "$pid" -o "$out_dir/strace.txt" 2>&1 &
	trace_pid=$!
	sleep "$seconds"
	kill -INT "$trace_pid" 2>/dev/null || true
	wait "$trace_pid" || true
else
	printf 'Neither perf nor strace is installed; syscall counts omitted.\n' >"$out_dir/syscalls.txt"
	sleep "$seconds"
fi

elapsed=$(( $(date +%s) - started ))
remaining=$(( seconds - elapsed ))
if [ "$remaining" -gt 0 ]; then
	sleep "$remaining"
fi

if [ -n "${pidstat_pid:-}" ]; then
	wait "$pidstat_pid" || true
fi
