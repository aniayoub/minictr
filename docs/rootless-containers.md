# Rootless Container Implementation

## Objective

Originally minictr required root privileges to run because creating some of the namespaces required privileges, as well as some configurations like writing to the rootfs and creating cgroups.

The goal is to be able to run a program inside minictr with an unprivileged host user.

## Skipping CGroup

For a first version, we adjust the code in a way that the creation of a CGroup is only made if the user specifies a CGroup limiter. We'll handle the CGroup later since it needs special treatment because it is handled by the host and is not a resource governed by the container's user namespace.

## First execution without changing anything

### Operation Description

Before changing anything, executing minictr with:

`./minictr run ./rootfs -- /bin/sleep 30`

results in an error:

`start child process: fork/exec /proc/self/exe: operation not permitted`

### Error Explanation

The issue is not that `fork/exec` itself requires root privileges. The issue happens while creating the child process because we are asking the kernel to create the UTS, PID, mount, and IPC namespaces.

Creating these namespaces requires `CAP_SYS_ADMIN` in the user namespace that governs them. Since minictr is now running as an unprivileged host user, it does not have this capability in the initial user namespace.

If all the clone flags associated with these isolations are removed, the error above does not occur.

### Solution: Add a user namespace

To solve this issue, we need the child container to have `CAP_SYS_ADMIN` without giving it `CAP_SYS_ADMIN` over the host.

The solution is to create a new user namespace. A process created in a new user namespace gets capabilities inside that namespace. These capabilities are scoped to the user namespace and to resources governed by namespaces owned by it.

To do that, we add the flag `CLONE_NEWUSER` to the clone flags.

After execution, the issue with starting the child is resolved, but a new issue occurs:

`set hostname: operation not permitted`

We resolve this in the next section.

### User/Group ID mappings

#### Problem Description

As mentioned in the previous section, the hostname cannot be set because the operation is not permitted.

When the user namespace is initially created, the child gets capabilities inside the new user namespace. However, minictr re-executes itself using `execve` to run `minictr init`.

During `execve`, Linux recalculates the process capabilities. Unless the process is UID 0 inside the user namespace, or the executable grants capabilities in another way, those capabilities are lost.

The execution sequence is roughly:

```text
clone(CLONE_NEWUSER | other namespace flags)

        ↓

CAP_SYS_ADMIN inside the new user namespace = yes

        ↓

execve("/proc/self/exe")

        ↓

capabilities recalculated

        ↓

not UID 0 inside the user namespace

        ↓

CAP_SYS_ADMIN lost

        ↓

sethostname()

        ↓

EPERM
```

One more note is that the issue is not restricted to setting the hostname, but to the other operations that need capabilities that were lost during the recalculation.

#### Solution: Adjust User/Group Mapping

We need the invoking host user to appear as UID 0 and GID 0 inside the new user namespace.

To do that, we introduce user/group mappings when setting up the process attributes.

The mapping is:

```text
container UID 0 -> invoking host UID
container GID 0 -> invoking host GID
```

This looks like:

```go
cmd.SysProcAttr = &syscall.SysProcAttr{
    Cloneflags: syscall.CLONE_NEWUTS |
        syscall.CLONE_NEWPID |
        syscall.CLONE_NEWNS |
        syscall.CLONE_NEWIPC |
        syscall.CLONE_NEWUSER,

    UidMappings: []syscall.SysProcIDMap{
        {
            ContainerID: 0,
            HostID:      os.Geteuid(),
            Size:        1,
        },
    },

    GidMappings: []syscall.SysProcIDMap{
        {
            ContainerID: 0,
            HostID:      os.Getegid(),
            Size:        1,
        },
    },

    GidMappingsEnableSetgroups: false,
}
```

`GidMappingsEnableSetgroups` is disabled because an unprivileged process must deny `setgroups` before installing this kind of GID mapping.

After trying to run minictr, the hostname permission issue is resolved. However, a new issue occurs:

`create old-root directory: mkdir /home/bee/Projects/learning/minictr/rootfs/.pivot_root: permission denied`

We resolve this in the next section.

## RootFS Permissions

### Problem Description

When the rootfs was initially created, it was owned by host root.

Even though the container process is now UID 0 inside its user namespace, that UID maps to the unprivileged user on the host. It does not become host UID 0.

So when running minictr with a rootless user, writing to a rootfs owned by host root is not allowed.

### Solution: Change the ownership

This issue with the provided filesystem should not be solved by minictr since it is a resource not managed by it.

So the solution will simply be to change the ownership of the rootfs to the user executing minictr, so that minictr can create the directories it needs during the container setup.

After adjusting the rootfs ownership, the next issue that occurs is:

`mount proc: operation not permitted`

which we resolve in the next section.

## Proc Too Revealing

### Diagnosis

To diagnose the issue instead of guessing at which permission check was failing, I ran:

`sudo journalctl -k -f`

and then ran minictr.

I got the kernel error:

`kernel: VFS: Mount too revealing`

### Solution: Adjust Execution Sequence

After some research, it turned out that the issue is with the sequence between `pivot_root` and mounting the new proc filesystem.

Originally, minictr performed `pivot_root`, detached the old root, and then tried to mount the new proc filesystem.

After the old root is detached, the inherited `/proc` mount is no longer present in the container's mount namespace. Since we are mounting proc from an unprivileged user namespace, the kernel performs an additional safety check to make sure that creating a fresh proc mount does not reveal something that was hidden from the inherited proc view.

At that point, the inherited proc mount is already gone, so the kernel cannot verify that the new proc mount is not more revealing and rejects it with:

`VFS: Mount too revealing`

Making the mount propagation private earlier does not remove the inherited `/proc` mount. It only prevents future mount changes from propagating between the host and the container mount namespaces.

The solution is to mount the new proc filesystem under the future rootfs **before** executing `pivot_root`, while the inherited proc filesystem is still present.

The sequence becomes roughly:

```text
create namespaces

        ↓

make mount propagation private

        ↓

mount new proc at rootfs/proc

        ↓

pivot_root(rootfs)

        ↓

detach old root and inherited proc

        ↓

new proc remains mounted at /proc
```

Since the process is already inside the new PID namespace when the new proc filesystem is mounted, the new proc filesystem represents the processes visible from that PID namespace.

After switching the sequence, the proc mount succeeds and the rootless container runs successfully.
