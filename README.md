# Mini Container Runtime

`minictr` is a learning-oriented container runtime written in Go.

The project is not intended to replace Docker, containerd, or runc. The goal is to understand the Linux primitives that make containers possible by implementing them incrementally and to build working intuition for Linux process, namespace, and filesystem behavior.

## Focus

This repository is for learning how containers are built from lower-level Linux features such as:

- processes and `exec`
- namespaces
- `/proc`
- `chroot` and `pivot_root`
- bind mounts
- cgroups v2
- process lifecycle and signal handling

Each stage introduces one primitive at a time.

The current implementation is intentionally close to the kernel-facing mechanics rather than hiding them behind higher-level orchestration. The code exercises the exact interfaces container runtimes rely on: re-exec process bootstrapping, namespace creation, mount propagation changes, root filesystem switching, and `/proc` setup.

## Current Status

The current codebase can launch a process inside a minimal container-like environment built from Linux namespaces and a prepared root filesystem. It also contains a separate cgroup v2 resource-control path that still belongs to the privileged execution flow.

Current behavior:

```bash
go build -o minictr ./cmd/minictr
./minictr run ./rootfs --hostname demo -- /bin/echo Hi
```

Example with a bind mount:

```bash
./minictr run ./rootfs \
      --hostname demo \
      --bind "$PWD":/workspace \
      -- /bin/sh
```

Example with resource limits:

```bash
sudo ./minictr run ./rootfs \
      --hostname demo \
      --pids 64 \
      --memory 256M \
      --cpu 0.5 \
      -- /bin/sh
```

What is implemented now:

- parent process re-execs the current binary in `init` mode
- child runs in new user, UTS, PID, mount, and IPC namespaces
- container UID 0 and GID 0 are mapped to the invoking host user and group
- container hostname is configurable with `--hostname`
- host paths can be bind-mounted into directory targets inside the container with repeated `--bind source:target` flags
- cgroups v2 limits can be applied for PID count, memory, and CPU quota in the privileged execution path
- `init` waits for an explicit parent sync signal before beginning container setup, so parent-side startup coordination completes first
- common Linux termination signals are forwarded from the parent runtime to `init`, and from `init` to the workload process
- the selected root filesystem is bind-mounted and activated with `pivot_root`
- after `pivot_root` switches into the new root, a fresh `/proc` is mounted before the old root is detached
- `init` supervises the requested workload as a child process, reaps exited children while waiting, and returns the workload exit code

What this demonstrates:

- practical use of Linux namespace flags from Go
- practical use of IPC namespace isolation alongside process and mount isolation
- direct cgroup v2 manipulation through `pids.max`, `memory.max`, and `cpu.max` in the privileged path
- basic Unix signal handling and forwarding across both runtime hops
- understanding of how a container bootstrap process can act as PID 1 and supervise a workload
- direct control over mount propagation and root filesystem transitions
- explicit host-to-container filesystem mapping through bind mounts onto directory targets
- hands-on knowledge of how container bootstrap code prepares an isolated runtime environment

Current constraints:

- requires Linux
- rootless runs expect the selected rootfs to be writable by the invoking host user because setup creates bind targets and `.pivot_root`
- expects a usable root filesystem that already contains the requested command
- rootless execution does not currently support cgroup-backed resource limits
- resource-limit flags still depend on cgroup v2 being available and writable under `/sys/fs/cgroup`, which keeps that path in the privileged flow for now
- current bind-mount setup creates targets as directories, so file-target bind mounts are not supported yet
- no network namespace isolation yet
- no OCI bundle or image support yet

## Architecture

The current execution model is:

```text
minictr run <rootfs> [runtime-options] -- <command> [args...]
                       |
                       +-- parse runtime config once in main
                       +-- create cgroup and apply resource limits when requested
                       +-- start child with sync pipe on fd 3
                       +-- join child to cgroup if one was created
                       +-- signal child to continue setup
                       +-- forward signals while child runs
                       v
           /proc/self/exe init <rootfs> [runtime-options] -- <command> [args...]
                       |
                       +-- wait for parent sync token on fd 3
                       +-- set hostname
                       +-- make mounts private
                       +-- create bind-mount targets under rootfs
                       +-- bind mount host paths into rootfs
                       +-- pivot_root into rootfs
                       +-- mount a fresh /proc in the new root
                       +-- detach and remove the old root
                       +-- start workload as a child process
                       +-- forward signals to workload
                       +-- reap child exits and return workload status
                       |
                       v
                 target process
```

The important design choice is that `run` prepares shared runtime state such as config and optional cgroup state before the child starts, while `init` gets a chance to perform container-specific setup before the target command starts. That setup includes namespace-backed isolation and filesystem activation, and it is the place where later features such as additional namespaces can be added.

The user-namespace path is now part of that bootstrap. `run` starts the child with `CLONE_NEWUSER` and maps container UID 0 and GID 0 to the invoking host user and group, so the child keeps namespace-scoped capabilities across the re-exec while still appearing as the unprivileged caller from the host's point of view.

This mirrors a real container-runtime concern: the bootstrap process has to mutate kernel-visible process state before handing control to the workload.

## Current CLI

The CLI now expects a root filesystem followed by runtime flags and a `--` separator before the container command:

```bash
./minictr run ./rootfs --hostname minictr -- /bin/sh
```

Supported runtime flags currently include:

- `--hostname <name>` to set the container hostname
- `--bind <source>:<target>` to bind-mount a host path into an absolute directory path inside the container
- `--pids <count>` to set `pids.max`
- `--memory <bytes|K|M|G>` to set `memory.max`
- `--cpu <cpus>` to set `cpu.max` relative to the runtime time unit

`--bind` may be provided more than once.

Example:

```bash
./minictr run ./rootfs \
      --hostname minictr \
      --bind /home/bee/data:/data \
      --bind /home/bee/src:/workspace \
      -- /bin/sh
```

If you request `--pids`, `--memory`, or `--cpu`, the runtime switches into its cgroup-backed resource-limit path and creates a cgroup before releasing `init` to continue setup. That path is not part of the current rootless support and still depends on privileged host cgroup v2 access.

The config parser runs once in `main()` before dispatching to `run` or `init`, so both code paths operate on the same parsed runtime configuration.

That design keeps the runtime honest: the child path does not rely on hidden global state, and the parent can apply resource controls before the child starts running the workload.

Startup is now explicitly synchronized across the parent/child boundary. The parent passes a pipe to `init` on file descriptor 3, waits until the child has been started, performs any requested cgroup placement, and only then writes a one-byte token that allows `init` to continue with hostname, mount, and workload setup.

When no resource-limit flags are set, no cgroup is created and the same sync pipe is used only to preserve the startup ordering between `run` and `init`.

Inside the PID namespace, `init` becomes PID 1 and the requested workload runs as its child. That is a deliberate reflection of the current implementation: `container.Init()` performs setup, starts the workload with `os.StartProcess(...)`, forwards common termination signals to its `os.Process` handle, reaps exited child processes while supervising, and exits with the workload's final status.

Bind mount parsing is split across two stages. During flag parsing, `--bind` values must be in `source:target` format and neither side may be empty. During `init`, each target is cleaned and must be an absolute container directory path such as `/data` before the mount is attempted.

When resource-limit flags are present, limits are applied through a dedicated cgroup created under `/sys/fs/cgroup`, the child PID is added after `Start()`, and the cgroup is removed after the workload exits. In practice, that remains a privileged execution path rather than part of the current rootless mode.

Runtime flag validation is also enforced before startup for numeric and structural checks: `--bind` must be valid `source:target`, and `--pids`, `--memory`, and `--cpu` must not be negative. The memory parser accepts `K`, `M`, and `G` suffixes and rejects oversized values. Absolute bind-target validation happens later in `init`, just before mount setup.

The current bind-mount implementation always creates the destination with `mkdir -p` semantics under the selected rootfs before calling `mount(MS_BIND|MS_REC)`, so the documented and tested path today is directory-oriented bind mounting.

## Next Stage

The next useful milestones are broader isolation and runtime hardening beyond the current bootstrap path.

Practical targets from here:

- network namespaces for interface isolation
- rootless-compatible cgroup handling
- broader process-tree supervision and init-style lifecycle hardening

## Long-Term Goal

Eventually, the runtime should support a command resembling:

```bash
./minictr run \
      ./rootfs \
      --hostname minictr \
      --memory 128M \
      --pids 64 \
      -- \
      /bin/sh
```

That process should eventually run with its own hostname, PID namespace, mount namespace, IPC namespace, filesystem root, `/proc`, and resource limits.

## Roadmap

```text
Stage 0  Environment inspection             DONE
Stage 1  Process execution / re-exec        DONE
Stage 2  UTS namespace                      DONE
Stage 3  PID namespace                      DONE
Stage 4  IPC namespace                      DONE
Stage 5  Mount namespace + /proc            DONE
Stage 6  pivot_root                         DONE
Stage 7  Bind mounts                        DONE
Stage 8  cgroups v2                         DONE
Stage 9  Signals and lifecycle management   DONE
Stage 10 User namespaces                    DONE
Stage 11 Network namespaces
Stage 12 OCI runtime bundle support
Stage 13 OCI image support
Stage 14 Security hardening
```

For this MVP, Stage 9 means the runtime forwards common termination signals across both runtime hops, `init` supervises the workload as PID 1, exited children are reaped while waiting for the main workload, and the workload's final exit status is propagated back out of the container path.

More complete process-tree supervision is still a useful future improvement, but it is treated here as runtime hardening rather than a blocker for the learning milestone.

The initial MVP will stop well before full feature parity with existing runtimes. The priority is understanding each primitive deeply rather than reproducing Docker.

## Learning Notes

Implementation notes for the current bootstrap path are available in [docs/runtime-flow-reexec-container-init.md](docs/runtime-flow-reexec-container-init.md).