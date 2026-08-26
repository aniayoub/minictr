# Mini Container Runtime

`minictr` is a learning-oriented container runtime written in Go.

The project is not intended to replace Docker, containerd, or runc. The goal is to understand the Linux primitives that make containers possible by implementing them incrementally.

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

## Current Status

Stage 1 is complete.

Current behavior:

```bash
go build -o minictr ./cmd/minictr
./minictr run /bin/sh
```

What this stage provides:

- parent process starts a child copy of the current binary
- child runs in `init` mode
- `init` replaces itself with the requested program using `unix.Exec()`

What it does not provide yet:

- hostname isolation
- PID isolation
- mount isolation
- filesystem isolation
- cgroup-based resource limits

At this point, `minictr` is a small re-exec based process launcher that establishes the runtime architecture for later container features.

## Architecture

The current execution model is:

```text
minictr run
            |
            v
minictr init
            |
            v
target process
```

The important design choice is that `init` gets a chance to perform setup before the target command starts. In later stages, that setup will include namespaces, mounts, filesystem changes, and cgroup configuration.

## Next Stage

Stage 2 introduces the first real isolation primitive: a UTS namespace.

Target behavior:

```bash
$ hostname
my-host

$ sudo ./minictr run /bin/sh

# hostname
minictr

# exit

$ hostname
my-host
```

The child process should see its own hostname while the host remains unchanged.

## Long-Term Goal

Eventually, the runtime should support a command resembling:

```bash
sudo ./minictr run \
      --rootfs ./rootfs \
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
Stage 2  UTS namespace                      NEXT
Stage 3  PID namespace
Stage 4  Mount namespace + /proc
Stage 5  chroot
Stage 6  pivot_root
Stage 7  Bind mounts
Stage 8  cgroups v2
Stage 9  Signals and lifecycle management
Stage 10 User namespaces
Stage 11 Network namespaces
Stage 12 OCI runtime bundle support
Stage 13 OCI image support
Stage 14 Security hardening
```

The initial MVP will stop well before full feature parity with existing runtimes. The priority is understanding each primitive deeply rather than reproducing Docker.

## Learning Notes

Detailed Stage 1 notes are available in [docs/stage-01-process-exec.md](docs/stage-01-process-exec.md).