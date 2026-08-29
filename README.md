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

The current codebase can launch a process inside a minimal container-like environment built from Linux namespaces and a prepared root filesystem.

Current behavior:

```bash
go build -o minictr ./cmd/minictr
sudo ./minictr run ./rootfs --hostname demo -- /bin/echo Hi
```

What is implemented now:

- parent process re-execs the current binary in `init` mode
- child runs in new UTS, PID, and mount namespaces
- container hostname is configurable with `--hostname`
- the selected root filesystem is bind-mounted and activated with `pivot_root`
- `/proc` is mounted inside the container rootfs
- `init` replaces itself with the requested workload using `unix.Exec()`

What this demonstrates:

- practical use of Linux namespace flags from Go
- understanding of the difference between process creation and process replacement
- direct control over mount propagation and root filesystem transitions
- hands-on knowledge of how container bootstrap code prepares an isolated runtime environment

Current constraints:

- requires Linux and root privileges
- expects a usable root filesystem that already contains the requested command
- no cgroup-based resource limits yet
- no user namespace isolation yet
- no network namespace isolation yet
- no OCI bundle or image support yet

## Architecture

The current execution model is:

```text
minictr run <rootfs> [runtime-options] -- <command> [args...]
                       |
                       v
           /proc/self/exe init <rootfs> [runtime-options] -- <command> [args...]
                       |
                       +-- set hostname
                       +-- make mounts private
                       +-- pivot_root into rootfs
                       +-- mount /proc
                       |
                       v
                 target process
```

The important design choice is that `init` gets a chance to perform setup before the target command starts. That setup now includes namespace-backed isolation and filesystem activation, and it is the place where later features such as cgroups or additional namespaces can be added.

This mirrors a real container-runtime concern: the bootstrap process has to mutate kernel-visible process state before handing control to the workload.

## Current CLI

The CLI now expects a root filesystem followed by runtime flags and a `--` separator before the container command:

```bash
sudo ./minictr run ./rootfs --hostname minictr -- /bin/sh
```

The same parser is used by both `run` and `init`, which keeps the bootstrap path aligned across the parent and child processes.

That design keeps the runtime honest: the child path does not rely on hidden global state and instead reconstructs container state from explicit arguments.

## Next Stage

The next useful milestones are resource control and broader isolation beyond the current bootstrap path.

Practical targets from here:

- cgroups v2 for memory and PID limits
- bind mounts for controlled filesystem exposure
- user namespaces for safer unprivileged execution paths
- network namespaces for interface isolation

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
Stage 6  Bind mounts                        PARTIAL
Stage 7  cgroups v2                         NEXT
Stage 8  Signals and lifecycle management
Stage 9  User namespaces
Stage 10 Network namespaces
Stage 11 OCI runtime bundle support
Stage 12 OCI image support
Stage 13 Security hardening
```

The initial MVP will stop well before full feature parity with existing runtimes. The priority is understanding each primitive deeply rather than reproducing Docker.

## Learning Notes

Implementation notes for the current bootstrap path are available in [docs/runtime-flow-reexec-container-init.md](docs/runtime-flow-reexec-container-init.md).