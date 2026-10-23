# Windows project discovery fix

## Result and scope

The affected desktop now runs the candidate fix. Plain `amonhen ls`, a fresh
PowerShell child, a normal host process and the still-running MXC agent all
see gadget-dev, polyfable-dev and novelbinder-dev. The host migrated the three
project records once; all three original credentials remain retrievable without
re-entry under approved execution. Neither original AppData registry was edited
or deleted during this investigation.

This is evidence for the tested Windows environment, not a release claim for
every platform and sandbox. Runtime macOS/Linux testing, a dedicated live BWS
integration, sign-out/sign-in and reboot acceptance remain outstanding. Direct
credential-store calls in MXC are denied; credential operations require approved
host execution there. No broker or automatic fallback was introduced.

## Cause established

The same logical path, `C:\Users\hfalc\AppData\Roaming\amonhen\projects.json`,
opened different backing files:

| Observation | SHA256 | File ID |
| --- | --- | --- |
| Normal host | `63EC58DB6FF8DA0AD2F7452F95D126969D0C05EEBA8E02866B69B30488C9D619` | `0x0000000000000000001f00000006281e` |
| Packaged desktop child | `62B0B4ED4365EAA4B5E85EDDC148532A6B38074CE77C40333DACC6300C5AFCA3` | `0x0000000000000000026b000000030993` |

`GetFinalPathNameByHandleW` resolved the agent's opened file to
`C:\Users\hfalc\AppData\Local\Packages\OpenAI.Codex_2p2nqsd0c76g0\LocalCache\Roaming\amonhen\projects.json`.
The explicit package-cache path had the same hash and file ID as the agent's
logical AppData path. Approved execution inherited this view too.

This identifies MSIX AppData virtualization as the cause of the divergent
registry views. It does not establish when the private copy was first created.
Earlier registry edits compromised that historical evidence. MXC log selection
alone was not used to infer causation. A fresh shell and app restart need not
remove the persistent package-private file.

Microsoft documents that virtualized apps prefer an existing per-package
AppData file over its real counterpart:
[Understanding how packaged desktop apps run on Windows](https://learn.microsoft.com/en-us/windows/msix/desktop/desktop-to-uwp-behind-the-scenes).
Package identity APIs returned no package for the child despite its redirected
file handle, so checking package identity alone would have missed this case.

## Implementation

- Windows metadata now lives at `%USERPROFILE%\.amonhen\projects.json`.
  macOS/Linux retain their existing user configuration path.
- `storage migrate` validates and imports the host legacy metadata once. It
  refuses an existing destination or a redirected source and leaves the old
  file and OS credentials unchanged. `storage init` creates a new empty store
  and refuses to discard an existing legacy registry.
- Every lookup opens the selected authoritative file afresh. Missing, invalid,
  inaccessible or redirected stores are explicit errors. There is no fallback
  to AppData, a cached list, credential enumeration or a second maintained index.
- An explicit `AMONHEN_CONFIG_DIR` selects an absolute shared directory. All
  clients must select the same store. Windows verifies opened file handles and
  rejects redirected/junction paths; use a direct path outside AppData.
- Writers use an OS interprocess lock around the complete mutation. Contention
  produces a retryable error. Metadata commits use a flushed temporary file and
  same-directory replacement. Windows briefly retries sharing/access conflicts,
  retaining the complete previous file if commit fails.
- `project rename` transfers the existing credential before publishing the new
  name, then removes the old entry. Rollback/cleanup failures are explicit.
  Credential operations and metadata are not a crash-atomic distributed
  transaction; interruption can leave unused credential entries.
- The Windows installer verifies its destination and temporary file handles,
  refusing a private redirected executable copy. BWS validation output is
  suppressed so arbitrary child diagnostics cannot disclose credentials.

The OS credential service remains `amonhen/bws-machine-account`, keyed by the
original project names. Migration neither enumerates nor rewrites tokens.

## Actual boundary checks, 23 October 2026

Environment: Windows build 26200 (UBR not collected), desktop package
`OpenAI.Codex 26.1016.6573.0`, Codex CLI `0.163.0-alpha.11`, Go
`1.26.4 windows/amd64`, original source base
`4cdc4e4b04fd52e1e45dec17123ce9e680e2b542`. Dependency versions remain pinned
in go.mod/go.sum, including go-keyring 0.2.6 and x/sys 0.30.0.

1. The original workspace fixture matched between the user's PowerShell and
   agent, narrowing the discrepancy to AppData.
2. A dedicated AppData fixture reproduced the old CLI failure: the real host
   added fixture-4 while the packaged agent continued to see three records.
3. A shared fixture exercised host add, rename and delete, then agent add,
   rename and delete. Each next lookup on the other side matched the complete
   list and registry hash. Both clients stayed running throughout.
4. The host seeded a random synthetic credential in an isolated service.
   Approved agent execution retrieved it and injected it into a child which
   reported only success. The host similarly retrieved an approved-agent-seeded
   synthetic credential. MXC denied direct access explicitly. Probe output was
   checked against the synthetic token before being emitted; synthetic entries
   were removed afterward.
5. All three existing production credentials were retrieved into a trusted
   child that checked only for a nonempty value. No token values were printed;
   no production BWS requests were made.
6. The adjacent Amon Lhaw Go loader was exercised using the installed CLI,
   a unique synthetic Amon Hen credential and a controlled BWS executable. It
   resolved by project name, loaded a synthetic application value and did not
   install the machine token into the application environment. This was not a
   live BWS integration and required no changes in the sibling repository.
7. Fresh legacy unelevated and elevated sandboxes listed all three projects
   using the identical build in the workspace. Separate fixtures then passed
   all six bidirectional add/rename/delete checks between MXC and each legacy
   implementation, comparing the next lookup's complete list and registry hash.
   That elevated sandbox could not launch the executable from its AppData
   installation path (CreateProcessAsUserW error 5); an ordinary cmd.exe startup
   succeeded. Installed-path execution in that mode remains unsupported without
   the necessary executable access in its sandbox configuration. No ACLs or
   saved sandbox settings were changed to conceal that limitation.

The normal host context was verified by matching the exact host AppData hash
and file ID previously supplied by the user. A first `Shell.Application`
launch inherited the package view and was explicitly rejected as host evidence.
A temporary fixture-only launcher using the documented desktop-shell parent
process method obtained the real host view. It is not part of Amon Hen or its
runtime authentication path:
[Microsoft's desktop-parent process example](https://devblogs.microsoft.com/oldnewthing/20190425-00/?p=102443/).

## Evidence and regression tests

Ignored local artifacts are retained in `dist/`:

- `boundary-c4af5731-r2/`: paired legacy reproduction and six mutation reports;
  `stage` is `complete`. `boundary-c4af5731/INVALID.txt` records the rejected
  first launch rather than treating it as a successful host test.
- `host-context.json`: real host hash/file identity proof.
- `install-result.json`: matching candidate/installed hashes, version and host
  project list. `amonhen-before-storage-fix.exe` preserves the previous binary.
- `lhaw-integration/`, its synthetic fixture and controlled BWS child: the
  separate Go library integration harness. These are local diagnostics.
- `freshness-elevated/` and `freshness-unelevated/`: twelve paired legacy/MXC
  mutation reports per implementation (six writes and six next-lookups).

The full repository gate passed: formatting, Go tests, vet, pinned actionlint,
and all five release cross-builds. Tests cover explicit missing-store failure,
invalid metadata, no legacy fallback, migration without token re-entry,
immediate lookup freshness, eight concurrent writer processes, writer locking,
reader/commit overlap, credential-preserving rename and installer replacement.

The Windows redirected-registry regression is run against the actual dedicated
MSIX fixture, not a mocked filesystem:

```powershell
$env:AMONHEN_REDIRECTED_FIXTURE = "$env:APPDATA\amonhen-investigation-c4af5731\amonhen\projects.json"
go test ./cmd/amonhen -run TestMSIXRedirectedRegistryRejected -count=1 -v
```

Without that explicitly supplied dedicated fixture the environment-specific
test skips. Standard tests remain portable. The affected MXC environment's
private temporary directory denied rename operations; tests/gate used a
workspace `dist` scratch directory via TEMP/TMP. The store never falls back to
truncating writes when a filesystem denies atomic replacement.

`scripts/sandbox-fixture.ps1` supports Legacy and Shared fixtures.
`scripts/boundary-host.ps1` and `scripts/boundary-agent.ps1` coordinate paired
observations and retain JSON evidence. They use a fixed run ID; archive the
previous evidence and select a new ID before repeating a completed run.

## Release limitations

The repaired Windows discovery path is verified on this affected desktop.
Do not claim universal platform, sandbox or credential-access support from
cross-build success. Before a broad release, complete dedicated live BWS
authentication, macOS Keychain/Linux runtime checks, fresh full agent sessions,
application restart, sign-out/sign-in and reboot acceptance, and unauthorized
identity tests. Amon Lhaw's explicit missing-project
environment-selector fallback also remains existing library behavior; unavailable
Amon Hen stores fail explicitly rather than returning a missing project.

Older Amon Hen executables keep reading the legacy file. Upgrade every client;
do not synchronize the two registries manually. Strong isolation against an
untrusted process running as the same OS user still requires a separate security
boundary; moving metadata does not provide one.
