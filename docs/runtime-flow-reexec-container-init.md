# Runtime Flow - Re-exec to Container Init

This document started as the Stage 1 process-exec note, but it now reflects the current runtime path implemented in the repository.

The core idea is still the same: the parent re-execs the current binary, and the child performs setup before starting the requested workload.

Today, the overall runtime path includes config parsing, cgroup setup, namespace creation, hostname configuration, mount propagation changes, `pivot_root`, mounting `/proc`, and launching the workload as a child of `init`.

## Why This Matters

This project is useful as Linux systems practice because it works directly with the kernel-facing mechanisms behind containers instead of abstracting them away. The current code exercises:

- process bootstrap with re-exec
- cgroup v2 setup and process membership management
- Unix signal forwarding from the runtime to the child process
- namespace creation via `clone` flags
- IPC namespace isolation in addition to UTS, PID, and mount isolation
- mount namespace behavior and propagation control
- bind mount preparation and host-to-container path mapping
- root filesystem switching with `pivot_root`
- procfs setup inside an isolated filesystem view
- PID 1-style workload supervision with explicit child reaping

## Process Architecture

Running:

```bash
sudo ./minictr run ./rootfs --hostname demo --bind "$PWD":/workspace -- /bin/sh
```

creates this sequence:

```text
Process A

./minictr run ./rootfs --hostname demo --bind "$PWD":/workspace -- /bin/sh
host PID 5000

        |
        | config.Parse(...)
        | create cgroup
        | write pids.max / memory.max / cpu.max
        | exec.Command("/proc/self/exe", ...)
        | add child to cgroup
        | forward SIGINT/SIGTERM/SIGHUP/SIGQUIT
        v

Process B

/proc/self/exe init ./rootfs --hostname demo --bind "$PWD":/workspace -- /bin/sh
host PID 5001

        |
        | sethostname()
        | make mounts private
        | mountBinds()
        | pivot_root()
        | mount /proc
        | exec.Command(...)
        | forward SIGINT/SIGTERM/SIGHUP/SIGQUIT
        | wait4() and reap child exits
        | exit with workload status
        v

/bin/sh
host PID 5002
container PID 2
```

From the host's point of view, both the container init process and the workload have normal host PIDs such as 5001 and 5002. Inside the container PID namespace, `init` becomes PID 1 and the workload runs as its child, typically PID 2.

## Current CLI Shape

The current command format is:

```bash
minictr run <rootfs> [runtime-options] -- <command> [command-args...]
```

Example:

```bash
sudo ./minictr run ./rootfs --hostname minictr --bind /home/bee/data:/data -- /bin/echo Hi
```

The `--` separator is required so runtime flags can be distinguished from the workload command and its arguments.

The same parser is reused when `init` runs, so the child receives the same rootfs, runtime flags, and workload arguments.

Supported runtime flags currently include:

- `--hostname <name>`
- `--bind <source>:<target>`
- `--pids <count>`
- `--memory <bytes|K|M|G>`
- `--cpu <quota>`

`--bind` may be provided multiple times.

The bind-mount parser rejects invalid values early. The flag must contain a colon, both source and target must be non-empty, and the target must later resolve to an absolute container path.

The memory flag accepts raw bytes or `K`, `M`, and `G` suffixes. The CPU flag is converted into the runtime's internal cgroup time unit before being written to `cpu.max`.

Validation is performed before runtime startup. Negative values for `--pids`, `--memory`, and `--cpu` are rejected, and oversized memory values are rejected during parsing.

## What `run` Does

The parent process uses Go's `exec.Command(...)` to launch another copy of the current binary through `/proc/self/exe`.

Before dispatching into `run` or `init`, `main()` parses the full runtime config once from the arguments after `<rootfs>`. That gives both code paths a shared view of hostname, bind mounts, and resource limits.

When running in parent mode, the runtime also creates a cgroup under `/sys/fs/cgroup/minictr-<pid>` and applies any configured limits before starting the child.

It configures the child with these namespace flags:

- `CLONE_NEWUTS`
- `CLONE_NEWPID`
- `CLONE_NEWNS`
- `CLONE_NEWIPC`

This gives the child:

- an isolated hostname view
- an isolated PID namespace
- an isolated mount namespace
- an isolated IPC namespace

Standard input, output, and error are inherited from the parent so interactive commands still work.

After `Start()`, the parent adds the child PID to the cgroup, forwards `SIGINT`, `SIGTERM`, `SIGHUP`, and `SIGQUIT` to the child, waits for `init` to exit, and removes the cgroup on cleanup.

In the current implementation, the child receiving those signals is the `init` process inside the new PID namespace. That `init` process then forwards the same signal set to the workload subprocess it created, reaps child exits with `wait4()`, and exits with the workload's resulting status code.

If cgroup membership fails after the child has been started, the runtime kills the child process and waits for it before returning the error.

## What `init` Does

Inside the child process, `init` receives the already-parsed config and performs container setup in this order:

1. set the container hostname
2. mark mounts as private with `MS_PRIVATE | MS_REC`
3. resolve each bind source to an absolute host path
4. clean each bind target and verify it is absolute inside the container
5. create the bind target directory under the selected rootfs
6. bind-mount each host path into the rootfs with `MS_BIND | MS_REC`
7. bind-mount the rootfs onto itself so it becomes a mount point
8. call `pivot_root`
9. change directory to `/`
10. unmount and remove the old root
11. mount `proc` at `/proc`
12. start the requested workload with `exec.Command(...)`
13. forward `SIGINT`, `SIGTERM`, `SIGHUP`, and `SIGQUIT` to that workload
14. call `wait4()` in a loop to reap child exits while supervising
15. exit with the main workload's exit code or signal-derived status

That ordering matters because mount propagation is made private before additional bind mounts are added, the bind targets must exist inside the future root filesystem, `pivot_root` requires the new root to already be a mount point, and `/proc` should be mounted only after the new root is active.

For a bind such as `/home/bee/data:/data`, the runtime maps the container target `/data` to a host path under the rootfs, such as `./rootfs/data`, creates that directory if needed, and mounts the host source there before switching roots.

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
- IPC namespace creation
- mount namespace creation
- configurable hostname
- repeated bind mounts with `--bind source:target`
- cgroup v2 resource limits for PID count, memory, and CPU quota
- forwarding of common Linux termination signals across both runtime hops
- reaping of child exits while `init` supervises the workload
- root filesystem activation through `pivot_root`
- `/proc` mounted inside the new root filesystem
- final workload launch as a subprocess of `init`

## Current Limitations

This is still a learning runtime, not a production container engine.

Current limitations include:

- root privileges are required
- the rootfs must already contain the command being executed
- cgroup v2 must be available and writable under `/sys/fs/cgroup`
- no user namespaces yet
- no network namespaces yet
- no OCI bundle or image workflow yet

## Current Workload Process Model

The current implementation does create a new process for the workload.

Before the workload starts:

```text
container PID 1
minictr init /bin/sh
```

After `init` launches the workload:

```text
container PID 1
minictr init ./rootfs -- /bin/sh

container PID 2
/bin/sh
```

This means the bootstrap process remains the namespace's PID 1 while the requested command runs underneath it.

That distinction matters because PID 1 has special process-lifecycle semantics on Linux. The current code now forwards common termination signals, uses `wait4()` to reap child exits while waiting for the main workload, and propagates the main workload's exit status back through `init`.

For this MVP, that behavior is enough to consider the signals and lifecycle milestone complete: the runtime can supervise a direct workload, preserve its exit status, and avoid leaving exited children unreaped while the main process is still running.

The remaining gap is broader subtree supervision. The code logs and reaps other exited children it observes while waiting for the main workload, but it does not yet implement fuller init-style lifecycle management beyond that loop.

## Next Useful Milestones

The current code has already moved past the original Stage 1 milestone. The next meaningful additions are:

- user namespaces for safer isolation
- network namespaces for connectivity control
- more complete signal forwarding and lifecycle management across child processes