# Amon Hen

Amon Hen's primary purpose is managing multiple Bitwarden Secrets Manager
(BWS) keys on one machine. Here, a key means a BWS machine-account token. Each
token is stored in the operating system credential store under its Amon Hen
project name. Projects also record their BWS project UUID, so commands can use
the matching credential. The secret values stored inside BWS remain managed by
Bitwarden.

> Amon Hen, known as the Hill of Sight (or Hill of the Eye), featured the Seat of Seeing, a high chair built within a stone-paved court.  It was here that Frodo Baggins, while wearing the One Ring, experienced visions of Mordor and decided to continue his journey alone.

## Install

Install the official Bitwarden Secrets Manager CLI (`bws`) first. Download the
Amon Hen executable for your operating system from the GitHub Releases page.

On Windows, double-click `amonhen.exe` to install it for your user. A message
confirms success or shows an error. You can also run `amonhen.exe install` from
PowerShell.

On macOS or Linux, run `./amonhen install` from a terminal. The installer places
the executable in `~/.local/bin` and adds that directory to the detected shell
startup file if needed. Open a new terminal or reload that file afterward.

To build from source, install Go 1.23 or later and run:

```sh
go build -o amonhen ./cmd/amonhen
```

## Add a project

Create a BWS machine account with access to the BWS project you want to use.
Prefer a read-only machine account scoped only to the required project. Then
register it with Amon Hen:

```text
amonhen project add -N local-dev -U <BWS-project-UUID> -T
```

`-N` is the unique name you choose for the project in Amon Hen. `-U` is the
UUID of the project in Bitwarden Secrets Manager. `-T` tells Amon Hen to prompt
for that project's machine-account token without displaying the characters as
you type. The token itself is not a command-line argument; do not paste it into
the command. Amon Hen stores it in the operating system credential store.

For example, if the BWS project UUID is
`12345678-1234-4123-8123-123456789abc`, enter:

```text
amonhen project add -N local-dev -U 12345678-1234-4123-8123-123456789abc -T
```

Then enter the machine-account token at the hidden prompt. The UUID above is an
example; use the UUID of your own BWS project.

Repeat `project add` with a different project name and UUID to manage another
machine-account token on the same machine. Each project has its own credential
store entry.

## List and delete projects

List configured projects with either command:

```text
amonhen project list
amonhen ls
```

The output contains each project's local name and BWS project UUID. It never
shows the machine-account token.

Look up a project's UUID by its Amon Hen name:

```text
amonhen project uuid -N local-dev
```

This prints the UUID only. To pass the project's machine-account token to a
child process without printing it, use the `amonhen token -N ... -- <command>`
form below.

Delete by the local name or the BWS project UUID:

```text
amonhen project delete -N local-dev
amonhen project delete -U 12345678-1234-4123-8123-123456789abc
```

Deleting a project also removes its saved token from the operating system
credential store.

## Pass a project's token to another process

To run a process with a project's stored machine-account token in its
`BWS_ACCESS_TOKEN` environment variable, use:

```text
amonhen token -N local-dev -- bws project list
```

`-N` selects the Amon Hen project. Amon Hen retrieves that project's token from
the operating system credential store and starts the command after `--`. Amon
Hen does not print the token, but the child process can read it from its
environment and could print or transmit it. Use this only with a process you
trust.

## Run a command

Run the desired command through Amon Hen, naming the registered project:

```text
amonhen run local-dev -- npm run dev
```

Amon Hen starts the BWS CLI with that project's UUID and machine-account token.
BWS supplies the secrets belonging to the project to the requested command as
environment variables. The BWS machine-account token is passed to the BWS
process through `BWS_ACCESS_TOKEN`; it is not included in the command arguments.
Amon Hen does not fetch or store individual secret values.

## Help and version

```text
amonhen --help
amonhen project add --help
amonhen project list --help
amonhen project uuid --help
amonhen project delete --help
amonhen token --help
amonhen run --help
amonhen version
amonhen -V
```

## Uninstall

On Windows, run the included script:

```powershell
.\uninstall.ps1
```

On macOS or Linux, run:

```sh
sh ./uninstall.sh
```

The uninstallers remove Amon Hen and its PATH entry while leaving project
metadata and credentials for you to remove separately.

## Security model and limits

Amon Hen keeps machine-account tokens out of command history and project
metadata, and stores them using the operating system credential store. The
`amonhen run` command uses `bws run` scoped to the selected project, so Amon Hen
itself does not read individual BWS secret values. The separate `amonhen token`
command intentionally passes the selected machine-account token to its child
process through the environment; that process can read it. Configure machine
accounts with the narrowest permissions needed, preferably read-only access
to only the relevant project.

This CLI alone is not a security boundary against a malicious agent running as
the same operating-system user. That agent may be able to invoke Amon Hen, read
the environment of a command it controls, or make that command disclose secrets
it receives. The operating system credential store protects tokens at rest and
between user identities; it does not isolate mutually untrusted processes
running as the same user. Stronger protection requires a separate broker
identity and an isolated, restricted workload that the agent cannot inspect
or control.
