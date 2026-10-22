# Amon Hen

A Go CLI that gives project commands Bitwarden Secrets Manager values by
reference. Project files contain `bws://` IDs; the BWS access token is requested
with terminal echo disabled and stored using the operating system credential
store.

## Current commands

```text
amonhen token set
amonhen project add demo OPENAI_API_KEY=bws://<secret-uuid>
amonhen project list
amonhen install
amonhen run demo -- npm run dev
amonhen version
```

`amonhen version`, `amonhen -V`, and `amonhen --version` print the embedded
release version.

Install the official Bitwarden Secrets Manager CLI (`bws`) and create a
read-only machine account scoped to a dedicated development project. Then save
its access token with `amonhen token set`. Get a secret's UUID from Bitwarden
Secrets Manager and add it to a named Amon Hen project. Amon Hen resolves each
reference immediately before starting the command. It does not offer a command
that prints a resolved value, and it masks literal secret values in child
stdout and stderr.

Project metadata is stored in the user's config directory with restrictive
file permissions. Secret values are not written to the config file. The BWS
CLI receives its access token through `BWS_ACCESS_TOKEN`, never as an argument.
The launched command does not inherit that token.

## Security model and limits

This version helps prevent accidental exposure through chat, command arguments,
project files, and ordinary command output. Using a dedicated BWS machine
account with read-only access to a non-production project also limits the
damage if that account is compromised.

**This version is not a security boundary against an agent running as the same
OS user.** Such an agent can potentially retrieve the token from that user's
credential store, inspect Amon Hen or BWS processes, change the command it asks
Amon Hen to run, or make the launched command transmit the secret. Output
masking only catches literal values in stdout/stderr; it cannot stop encoding,
network exfiltration, or other side channels. Credential stores protect data at
rest and from other OS identities; they do not protect one process from another
process with the same user's permissions.

To claim resistance to a malicious same-user agent, Amon Hen needs a separate
broker identity and a locked-down workload boundary. The broker must own the
credential store and accept only narrow, authenticated requests. The secret-
using workload must be fixed or approved, run under a separate restricted
identity/container/VM, and have tightly limited filesystem, debugging, and
network access. The agent must not be able to control or inspect that workload.
Even then, any workload allowed to use a credential can misuse its authority;
permissions and network destinations must limit that capability. This CLI is a
first reference-based workflow, not that broker/sandbox implementation.

## Development

Requires Go 1.23 or later. Build with:

```text
go build ./cmd/amonhen
```

## Install on Windows

From PowerShell in the repository directory, run:

```powershell
go run ./cmd/amonhen install
```

From an extracted Windows release package, run `.\amonhen.exe install`.
The executable installs itself under `%LOCALAPPDATA%\Programs\AmonHen\bin` and
adds that directory to the current user's PATH. It does not require
administrator access. Open a new terminal to use `amonhen`.

To remove the executable and its PATH entry while keeping the user's settings
and credentials, run:

```powershell
.\uninstall.ps1
```

## Install on Linux or macOS

From a terminal in the repository directory, run:

```sh
go run ./cmd/amonhen install
```

From an extracted Linux/macOS release package, run `./amonhen install`. The
executable installs itself under `~/.local/bin` and adds that directory to the
detected shell's startup file when needed. Open a new terminal or reload the
startup file to use `amonhen`.

To remove the executable and the installer's PATH entry while keeping the
user's project configuration and credentials, run:

```sh
sh ./uninstall.sh
```

