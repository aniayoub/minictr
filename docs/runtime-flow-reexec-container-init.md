# Runtime Flow - Re-exec to Container Init

This document started as the Stage 1 process-exec note, but it now reflects the current runtime path implemented in the repository.

The core idea is still the same: the parent re-execs the current binary, and the child performs setup before starting the requested workload.

Today, the overall runtime path includes config parsing, a privileged-only optional cgroup setup path, user-namespace-backed namespace creation, a parent/child startup handshake, hostname configuration, mount propagation changes, bind mounts, `pivot_root`, mounting a fresh `/proc` in the new root before the old root is detached, and launching the workload as a child of `init`.

## Why This Matters

This project is useful as Linux systems practice because it works directly with the kernel-facing mechanisms behind containers instead of abstracting them away. The current code exercises:

- process bootstrap with re-exec
- optional cgroup v2 setup and process membership management in the privileged path
- parent/child startup synchronization through an inherited pipe
- Unix signal forwarding from the runtime to the child process
- namespace creation via `clone` flags, including a user namespace for rootless execution
- user/group ID mapping so container UID 0 and GID 0 map back to the invoking host user and group
- IPC namespace isolation in addition to UTS, PID, and mount isolation
- mount namespace behavior and propagation control
- bind mount preparation and host-to-container path mapping
- root filesystem switching with `pivot_root`
- procfs setup inside an isolated filesystem view
- PID 1-style workload supervision with explicit child reaping

## Process Architecture

Running:

```bash
./minictr run ./rootfs --hostname demo --bind "$PWD":/workspace -- /bin/sh
```

creates this sequence:

```text
Process A

./minictr run ./rootfs --hostname demo --bind "$PWD":/workspace -- /bin/sh
host PID 5000

        |
        | config.Parse(...)
        | create cgroup if limits were requested
        | write pids.max / memory.max / cpu.max when configured
        | exec.Command("/proc/self/exe", ...)
        | add child to cgroup if present
        | write token to fd 3 sync pipe
        | forward SIGINT/SIGTERM/SIGHUP/SIGQUIT
        v

Process B

/proc/self/exe init ./rootfs --hostname demo --bind "$PWD":/workspace -- /bin/sh
host PID 5001

        |
        | read one-byte token from fd 3
        | sethostname()
        | make mounts private
        | mountBinds()
        | pivot_root()
        | mount /proc in the new root
        | detach and remove old root
        | os.StartProcess(...)
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
./minictr run ./rootfs --hostname minictr --bind /home/bee/data:/data -- /bin/echo Hi
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

When any of `--pids`, `--memory`, or `--cpu` are set, the parent creates a cgroup before the child is released from its startup wait. Without those flags, the runtime skips cgroup creation entirely. That cgroup-backed path is not part of the current rootless support.

The bind-mount parser rejects invalid values early. The flag must contain a colon, and both source and target must be non-empty. Absolute target-path validation happens later inside `init`, immediately before bind setup.

The memory flag accepts raw bytes or `K`, `M`, and `G` suffixes. The CPU flag is converted into the runtime's internal cgroup time unit before being written to `cpu.max`.

Validation is performed before runtime startup. Negative values for `--pids`, `--memory`, and `--cpu` are rejected, and oversized memory values are rejected during parsing.

## What `run` Does

The parent process uses Go's `exec.Command(...)` to launch another copy of the current binary through `/proc/self/exe`.

Before dispatching into `run` or `init`, `main()` parses the full runtime config once from the arguments after `<rootfs>`. That gives both code paths a shared view of hostname, bind mounts, and resource limits.

When running in parent mode, the runtime creates a cgroup under `/sys/fs/cgroup/minictr-<pid>` only when at least one resource-limit flag is set, and applies the requested limits before starting the child. In practice, that remains a privileged-only branch of the startup flow.

The parent also creates a pipe and passes its read end to the child as file descriptor 3. That lets `init` block immediately after startup until the parent has finished the parent-side startup work, including cgroup placement when one is being used in the privileged path.

It configures the child with these namespace flags:

- `CLONE_NEWUSER`
- `CLONE_NEWUTS`
- `CLONE_NEWPID`
- `CLONE_NEWNS`
- `CLONE_NEWIPC`

This gives the child:

- a user namespace where container UID 0 and GID 0 map to the invoking host user and group
- an isolated hostname view
- an isolated PID namespace
- an isolated mount namespace
- an isolated IPC namespace

Standard input, output, and error are inherited from the parent so interactive commands still work.

After `Start()`, the parent adds the child PID to the cgroup when one exists, writes a one-byte continue token into the sync pipe, forwards `SIGINT`, `SIGTERM`, `SIGHUP`, and `SIGQUIT` to the child, waits for `init` to exit, and removes the cgroup on cleanup.

In the current implementation, the child receiving those signals is the `init` process inside the new PID namespace. That `init` process then forwards the same signal set to the workload subprocess it created, reaps child exits with `wait4()`, and exits with the workload's resulting status code.

If cgroup membership fails after the child has been started, the runtime kills the child process and waits for it before returning the error.

The user-namespace mapping is what keeps the rootless flow working across the re-exec. Inside the container, `id -u` and `id -g` report 0, while from the host's point of view the `init` process still runs under the invoking user's real UID and GID.

## What `init` Does

Inside the child process, `init` receives the already-parsed config and performs container setup in this order:

1. block on file descriptor 3 until the parent signals that parent-side startup work is complete
2. set the container hostname
3. mark mounts as private with `MS_PRIVATE | MS_REC`
4. resolve each bind source to an absolute host path
5. clean each bind target and verify it is absolute inside the container
6. create the bind target directory under the selected rootfs
7. bind-mount each host path into the rootfs with `MS_BIND | MS_REC`
8. bind-mount the rootfs onto itself so it becomes a mount point
9. call `pivot_root`
10. change directory to `/`
11. mount a fresh `proc` filesystem at `/proc` while the old root is still available at `/.pivot_root`
12. unmount `/.pivot_root` with `MNT_DETACH`
13. remove the old-root directory
14. start the requested workload with `os.StartProcess(...)`
15. forward `SIGINT`, `SIGTERM`, `SIGHUP`, and `SIGQUIT` to that workload through its `os.Process` handle
16. call `wait4()` in a loop to reap child exits while supervising
17. exit with the main workload's exit code or signal-derived status

That ordering matters because the child must not begin container setup before the parent has attached it to the cgroup when one exists, mount propagation is made private before additional bind mounts are added, absolute bind-target validation happens in the same slice that performs the mounts, and bind targets must exist inside the future root filesystem before the root switch. `pivot_root` still requires the new root to already be a mount point. After the root switch and `chdir("/")`, the runtime mounts a fresh procfs at the new `/proc` while the old root is still present at `/.pivot_root`; only after that succeeds does it detach and remove the old root.

For a bind such as `/home/bee/data:/data`, the runtime maps the container target `/data` to a host path under the rootfs, such as `./rootfs/data`, creates that directory if needed, and mounts the host source there before switching roots.

Because the current code always creates the destination with directory semantics, the supported and tested bind-mount path today is a host path mounted onto a directory target inside the container rather than a file-to-file bind.

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
- user namespace creation with rootless UID/GID mapping
- UTS namespace creation
- PID namespace creation
- IPC namespace creation
- mount namespace creation
- configurable hostname
- repeated bind mounts with `--bind source:target`
- cgroup v2 resource limits for PID count, memory, and CPU quota when those flags are requested in the privileged path
- forwarding of common Linux termination signals across both runtime hops
- startup synchronization so parent-side setup is finished before container setup proceeds
- reaping of child exits while `init` supervises the workload
- root filesystem activation through `pivot_root`
- `/proc` mounted inside the new root filesystem
- final workload launch as a subprocess of `init`

## Current Limitations

This is still a learning runtime, not a production container engine.

Current limitations include:

- the selected rootfs must be writable by the invoking host user for rootless setup to succeed
- the rootfs must already contain the command being executed
- rootless execution does not currently support cgroup-backed resource limits
- cgroup-backed resource limits still require writable cgroup v2 access under `/sys/fs/cgroup`, which keeps them in the privileged path for now
- bind targets are created as directories, so file-target binds are not supported yet
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

- network namespaces for connectivity control
- rootless-compatible cgroup handling
- more complete signal forwarding and lifecycle management across child processes