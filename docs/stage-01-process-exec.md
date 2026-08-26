# Stage 1 - Process Execution and Re-exec

Stage 1 establishes the execution model that later container setup will build on.

At this stage, `minictr` can run commands such as:

```bash
./minictr run /bin/sh
```

There is still no isolation yet. The goal here is to understand process creation, re-exec, and `exec` before introducing namespaces, mounts, or cgroups.

## Process Architecture

Running:

```bash
./minictr run /bin/sh
```

creates this sequence:

```text
Process A

./minictr run /bin/sh
PID 5000

        |
        | exec.Command(...)
        | creates another process
        v

Process B

/proc/self/exe init /bin/sh
PID 5001

        |
        | unix.Exec(...)
        v

/bin/sh
PID 5001
```

The important observation is:

```text
minictr init     PID 5001
      |
      | unix.Exec()
      v
/bin/sh          PID 5001
```

The PID does not change.

`unix.Exec()` replaces the program running inside the process instead of creating another process.

## Why Re-execute `minictr`?

Linux already provides the underlying process creation and execution mechanisms.

Normally, a program could simply execute:

```go
exec.Command("/bin/sh")
```

and Linux would create the child process and execute `/bin/sh`.

This runtime intentionally introduces an intermediate process:

```text
parent
   |
   v
minictr init
   |
   v
/bin/sh
```

The reason is that later we need to execute our own setup code after the child process has been created but before the application starts.

Eventually, `minictr init` will perform work such as:

```text
minictr init

    |
    +-- configure hostname
    |
    +-- configure namespaces
    |
    +-- configure mounts
    |
    +-- pivot_root
    |
    +-- mount /proc
    |
    +-- configure container environment
    |
    v

exec("/bin/sh")
```

Once all initialization is complete, `unix.Exec()` replaces `minictr init` with the requested container process.

## What Stage 1 Teaches

### `exec.Command`

Go's `exec.Command(...)` is used to prepare the creation of another process.

For this project, the parent executes another copy of the current binary:

```go
exec.Command(
    "/proc/self/exe",
    "init",
    "/bin/sh",
)
```

`/proc/self/exe` points to the executable currently running.

This means the program can launch another copy of itself without knowing where its binary was installed.

### `run` and `init`

The same binary has two modes.

```text
minictr run ...
```

means:

> Act as the parent runtime or supervisor.

```text
minictr init ...
```

means:

> Act as the child bootstrap process.

The `main()` function dispatches between them:

```text
main
 |
 +-- run  -> run()
 |
 +-- init -> containerInit()
```

### `unix.Exec`

`unix.Exec()` does not create a new process.

It replaces the currently running program.

Before:

```text
PID 5001
minictr init /bin/sh
```

After:

```text
PID 5001
/bin/sh
```

This mechanism is important because the child can first execute container setup code and only then transform itself into the user's requested program.

### Standard Input and Output

The child currently inherits:

```text
stdin
stdout
stderr
```

from the parent.

That allows commands such as:

```bash
./minictr run /bin/sh
```

to behave interactively.

The shell reads from the same terminal and writes back to it.

## Current Limitations

Stage 1 is not a container runtime yet.

There is currently no isolation.

The child still shares the host's:

- hostname
- process namespace
- mount namespace
- filesystem
- network
- users
- cgroups

At this point, `minictr` is a small process launcher with the architecture needed for later container setup.

## Next Step

Stage 2 introduces the first real isolation primitive: the UTS namespace.

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

Conceptually:

```text
HOST
hostname = my-host

       |
       | create child with
       | new UTS namespace
       v

CHILD
hostname = minictr
```