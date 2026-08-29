# Runtime Flow - Re-exec to Container Init

This document started as the Stage 1 process-exec note, but it now reflects the current runtime path implemented in the repository.

The core idea is still the same: the parent re-execs the current binary, and the child performs setup before replacing itself with the requested workload.

Today, that setup already includes namespace creation, hostname configuration, mount propagation changes, `pivot_root`, and mounting `/proc`.

## Why This Matters

This project is useful as Linux systems practice because it works directly with the kernel-facing mechanisms behind containers instead of abstracting them away. The current code exercises:

- process bootstrap with re-exec
- namespace creation via `clone` flags
- mount namespace behavior and propagation control
- root filesystem switching with `pivot_root`
- procfs setup inside an isolated filesystem view
- process replacement through `exec`

## Process Architecture

Running:

```bash
sudo ./minictr run ./rootfs --hostname demo -- /bin/sh
```

creates this sequence:

```text
Process A

./minictr run ./rootfs --hostname demo -- /bin/sh
host PID 5000

        |
        | exec.Command("/proc/self/exe", ...)
        v

Process B

/proc/self/exe init ./rootfs --hostname demo -- /bin/sh
host PID 5001

        |
        | sethostname()
        | make mounts private
        | pivot_root()
        | mount /proc
        | unix.Exec(...)
        v

/bin/sh
host PID 5001
container PID 1
```

From the host's point of view, the child still has a normal host PID such as 5001. Inside the container PID namespace, the workload becomes PID 1.

## Current CLI Shape

The current command format is:

```bash
minictr run <rootfs> [runtime-options] -- <command> [command-args...]
```

Example:

```bash
sudo ./minictr run ./rootfs --hostname minictr -- /bin/echo Hi
```

The `--` separator is required so runtime flags can be distinguished from the workload command and its arguments.

The same parser is reused when `init` runs, so the child receives the same rootfs, runtime flags, and workload arguments.

## What `run` Does

The parent process uses Go's `exec.Command(...)` to launch another copy of the current binary through `/proc/self/exe`.

It configures the child with these namespace flags:

- `CLONE_NEWUTS`
- `CLONE_NEWPID`
- `CLONE_NEWNS`

This gives the child:

- an isolated hostname view
- an isolated PID namespace
- an isolated mount namespace

Standard input, output, and error are inherited from the parent so interactive commands still work.

## What `init` Does

Inside the child process, `init` parses the rootfs path, runtime flags, and command, then performs container setup in this order:

1. set the container hostname
2. mark mounts as private with `MS_PRIVATE | MS_REC`
3. bind-mount the rootfs onto itself so it becomes a mount point
4. call `pivot_root`
5. change directory to `/`
6. unmount and remove the old root
7. mount `proc` at `/proc`
8. replace the init process with the requested workload using `unix.Exec()`

That ordering matters because `pivot_root` requires the new root to already be a mount point, and `/proc` should be mounted only after the new root is active.

## Why Re-exec Still Matters

The Stage 1 idea remains the foundation of the current implementation.

We still need an intermediate process:

```text
parent
   |
   v
minictr init
   |
   v
workload
```

Without that bootstrap process, there would be nowhere to run setup code between child creation and starting the target command.

This is one of the central container-runtime patterns: create a controlled process boundary first, mutate its execution environment, then hand over control to the application.

## What Is Implemented Now

The current code already provides:

- re-exec based parent/child architecture
- UTS namespace creation
- PID namespace creation
- mount namespace creation
- configurable hostname
- root filesystem activation through `pivot_root`
- `/proc` mounted inside the new root filesystem
- final workload replacement through `unix.Exec()`

## Current Limitations

This is still a learning runtime, not a production container engine.

Current limitations include:

- root privileges are required
- the rootfs must already contain the command being executed
- no cgroup limits yet
- no user namespaces yet
- no network namespaces yet
- no OCI bundle or image workflow yet

## Key Observation About `unix.Exec`

`unix.Exec()` does not create a new process.

Before:

```text
container PID 1
minictr init /bin/sh
```

After:

```text
container PID 1
/bin/sh
```

The process changes identity, but not its PID within that namespace.

That distinction is fundamental to Linux process control: the kernel swaps the running program image for the new executable while keeping the process slot alive.

## Next Useful Milestones

The current code has already moved past the original Stage 1 milestone. The next meaningful additions are:

- cgroups v2 for memory and process limits
- bind mounts for exposing selected host paths
- user namespaces for safer isolation
- network namespaces for connectivity control