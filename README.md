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

The current codebase can launch a process inside a minimal container-like environment built from Linux namespaces, a prepared root filesystem, and cgroups v2 resource controls.

Current behavior:

```bash
go build -o minictr ./cmd/minictr
sudo ./minictr run ./rootfs --hostname demo -- /bin/echo Hi
```

Example with a bind mount:

```bash
sudo ./minictr run ./rootfs \
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
- child runs in new UTS, PID, and mount namespaces
- container hostname is configurable with `--hostname`
- host paths can be bind-mounted into the container with repeated `--bind source:target` flags
- cgroups v2 limits can be applied for PID count, memory, and CPU quota
- common Linux termination signals are forwarded from the parent runtime to the child process
- the selected root filesystem is bind-mounted and activated with `pivot_root`
- `/proc` is mounted inside the container rootfs
- `init` replaces itself with the requested workload using `unix.Exec()`

What this demonstrates:

- practical use of Linux namespace flags from Go
- direct cgroup v2 manipulation through `pids.max`, `memory.max`, and `cpu.max`
- basic Unix signal handling and forwarding in a parent/child runtime model
- understanding of the difference between process creation and process replacement
- direct control over mount propagation and root filesystem transitions
- explicit host-to-container filesystem mapping through bind mounts
- hands-on knowledge of how container bootstrap code prepares an isolated runtime environment

Current constraints:

- requires Linux and root privileges
- expects a usable root filesystem that already contains the requested command
- expects cgroup v2 to be available and writable under `/sys/fs/cgroup`
- no user namespace isolation yet
- no network namespace isolation yet
- no OCI bundle or image support yet

## Architecture

The current execution model is:

```text
minictr run <rootfs> [runtime-options] -- <command> [args...]
                       |
                       +-- parse runtime config once in main
                       +-- create cgroup and apply resource limits
                       +-- start child, join it to cgroup, and forward signals
                       v
           /proc/self/exe init <rootfs> [runtime-options] -- <command> [args...]
                       |
                       +-- set hostname
                       +-- make mounts private
                       +-- create bind-mount targets under rootfs
                       +-- bind mount host paths into rootfs
                       +-- pivot_root into rootfs
                       +-- mount /proc
                       |
                       v
                 target process
```

The important design choice is that `run` prepares shared runtime state such as config and cgroups before the child starts, while `init` gets a chance to perform container-specific setup before the target command starts. That setup includes namespace-backed isolation and filesystem activation, and it is the place where later features such as additional namespaces can be added.

This mirrors a real container-runtime concern: the bootstrap process has to mutate kernel-visible process state before handing control to the workload.

## Current CLI

The CLI now expects a root filesystem followed by runtime flags and a `--` separator before the container command:

```bash
sudo ./minictr run ./rootfs --hostname minictr -- /bin/sh
```

Supported runtime flags currently include:

- `--hostname <name>` to set the container hostname
- `--bind <source>:<target>` to bind-mount a host path into an absolute path inside the container
- `--pids <count>` to set `pids.max`
- `--memory <bytes|K|M|G>` to set `memory.max`
- `--cpu <cpus>` to set `cpu.max` relative to the runtime time unit

`--bind` may be provided more than once.

Example:

```bash
sudo ./minictr run ./rootfs \
      --hostname minictr \
      --bind /home/bee/data:/data \
      --bind /home/bee/src:/workspace \
      --pids 64 \
      --memory 512M \
      --cpu 0.5 \
      -- /bin/sh
```

The config parser runs once in `main()` before dispatching to `run` or `init`, so both code paths operate on the same parsed runtime configuration.

That design keeps the runtime honest: the child path does not rely on hidden global state, and the parent can apply resource controls before the child starts running the workload.

Bind mount validation is strict: the flag value must be in `source:target` format, neither side may be empty, and the target must be an absolute container path such as `/data`.

Resource limits are applied through a dedicated cgroup created under `/sys/fs/cgroup`, the child PID is added after `Start()`, and the cgroup is removed after the workload exits.

Runtime flag validation is also enforced before startup: `--bind` must be valid `source:target`, bind targets must be absolute container paths, and `--pids`, `--memory`, and `--cpu` must not be negative. The memory parser accepts `K`, `M`, and `G` suffixes and rejects oversized values.

## Next Stage

The next useful milestones are broader isolation and runtime hardening beyond the current bootstrap path.

Practical targets from here:

- user namespaces for safer unprivileged execution paths
- network namespaces for interface isolation
- more complete signal and lifecycle handling across containerized process trees

## Long-Term Goal

Eventually, the runtime should support a command resembling:

```bash
sudo ./minictr run \
      ./rootfs \
      --hostname minictr \
      --memory 128M \
      --pids 64 \
      -- \
      /bin/sh
```

That process should eventually run with its own hostname, PID namespace, mount namespace, filesystem root, `/proc`, and resource limits.

## Roadmap

```text
Stage 0  Environment inspection             DONE
Stage 1  Process execution / re-exec        DONE
Stage 2  UTS namespace                      DONE
Stage 3  PID namespace                      DONE
Stage 4  Mount namespace + /proc            DONE
Stage 5  pivot_root                         DONE
Stage 6  Bind mounts                        DONE
Stage 7  cgroups v2                         DONE
Stage 8  Signals and lifecycle management   PARTIAL
Stage 9  User namespaces
Stage 10 Network namespaces
Stage 11 OCI runtime bundle support
Stage 12 OCI image support
Stage 13 Security hardening
```

The initial MVP will stop well before full feature parity with existing runtimes. The priority is understanding each primitive deeply rather than reproducing Docker.

## Learning Notes

Implementation notes for the current bootstrap path are available in [docs/runtime-flow-reexec-container-init.md](docs/runtime-flow-reexec-container-init.md).