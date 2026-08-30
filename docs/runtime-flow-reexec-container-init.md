# Runtime Flow - Re-exec to Container Init

This document started as the Stage 1 process-exec note, but it now reflects the current runtime path implemented in the repository.

The core idea is still the same: the parent re-execs the current binary, and the child performs setup before replacing itself with the requested workload.

Today, the overall runtime path includes config parsing, cgroup setup, namespace creation, hostname configuration, mount propagation changes, `pivot_root`, and mounting `/proc`.

## Why This Matters

This project is useful as Linux systems practice because it works directly with the kernel-facing mechanisms behind containers instead of abstracting them away. The current code exercises:

- process bootstrap with re-exec
- cgroup v2 setup and process membership management
- namespace creation via `clone` flags
- mount namespace behavior and propagation control
- bind mount preparation and host-to-container path mapping
- root filesystem switching with `pivot_root`
- procfs setup inside an isolated filesystem view
- process replacement through `exec`

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

## What `run` Does

The parent process uses Go's `exec.Command(...)` to launch another copy of the current binary through `/proc/self/exe`.

Before dispatching into `run` or `init`, `main()` parses the full runtime config once from the arguments after `<rootfs>`. That gives both code paths a shared view of hostname, bind mounts, and resource limits.

When running in parent mode, the runtime also creates a cgroup under `/sys/fs/cgroup/minictr-<pid>` and applies any configured limits before starting the child.

It configures the child with these namespace flags:

- `CLONE_NEWUTS`
- `CLONE_NEWPID`
- `CLONE_NEWNS`

This gives the child:

- an isolated hostname view
- an isolated PID namespace
- an isolated mount namespace

Standard input, output, and error are inherited from the parent so interactive commands still work.

After `Start()`, the parent adds the child PID to the cgroup, waits for the workload to exit, and removes the cgroup on cleanup.

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
12. replace the init process with the requested workload using `unix.Exec()`

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
- mount namespace creation
- configurable hostname
- repeated bind mounts with `--bind source:target`
- cgroup v2 resource limits for PID count, memory, and CPU quota
- root filesystem activation through `pivot_root`
- `/proc` mounted inside the new root filesystem
- final workload replacement through `unix.Exec()`

## Current Limitations

This is still a learning runtime, not a production container engine.

Current limitations include:

- root privileges are required
- the rootfs must already contain the command being executed
- cgroup v2 must be available and writable under `/sys/fs/cgroup`
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

- user namespaces for safer isolation
- network namespaces for connectivity control
- stronger signal forwarding and lifecycle management