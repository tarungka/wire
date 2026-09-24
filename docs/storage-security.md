# Storage security

Wire does not encrypt database files, checkpoint archives or backups itself.
TLS protects network connections; enabling it does not encrypt stored state.
WIP-17's at-rest strategy is encryption provided by the host or storage platform,
with keys managed outside Wire. This document describes deployment requirements,
not a claim that Wire verifies the host's encryption configuration.

## Storage inventory

Protect every location below, including temporary copies and old generations.
Encrypting only the coordinator's volume leaves worker records exposed.

| Data | Location and contents |
| --- | --- |
| Coordinator metadata | `node.data_dir`: Pebble files, WAL, job graphs, assignments, checkpoint manifests and recovery metadata. |
| Worker operator state | The state backend's configured root; default scoped worker and window backends can create directories under the process temporary directory. State can contain complete user records. |
| Checkpoint metadata | `worker.checkpoint_replica.store_root`: persisted task checkpoint state and metadata. |
| Checkpoint artifacts | `worker.checkpoint_replica.artifact_root`: state snapshots referenced by checkpoints. |
| Checkpoint transfer staging | `worker.checkpoint_replica.staging_root`: temporary upload, fetch and import archives. |
| Authentication material | `auth.file`, TLS private-key files and client password/API-key files. API keys in the authentication file are plaintext even though the running server retains their hashes. |
| Backups and diagnostics | Copied volumes, retained checkpoint artifacts, logs, core dumps and swap may retain data after a job or checkpoint is deleted. |

The default state paths use Go's process temporary directory. Configure `TMPDIR`
on Unix deployments before starting the process to a private directory on
protected storage, and verify all explicit backend and staging roots separately.
Custom source/sink factories can create their own files; their storage is also
part of the deployment's security boundary.

## Deployment contract

Provision encrypted volumes before creating Wire's directories. Run Wire under
a dedicated service identity, restrict directory and credential-file access to
that identity, and keep encryption keys outside those volumes. Ensure the service
cannot start against an unmounted fallback directory when the encrypted volume
is unavailable. The service manager or storage platform must enforce that mount
and key dependency; Wire does not detect an unencrypted filesystem.

All coordinator instances that share metadata must use the same protected
storage and its normal ownership/election rules. Storage encryption does not
replace fencing, leader election, checkpoint validation or backup consistency.
A running process with access to unlocked volumes can read plaintext; this
strategy protects stored media, not a compromised Wire process or host.

Do not place literal connector credentials into persisted job configurations.
Coordinator-side environment-reference resolution and API redaction remain
unfinished in this branch. Storage encryption is not evidence that those
application-level secret-handling requirements are satisfied.

## Backup, restore and rotation

Use a consistent storage snapshot or stop the relevant writers before copying
live databases. Preserve checkpoint metadata together with every referenced
artifact: independently copying or pruning artifact directories can make a
backup unrestorable. Encrypt backup destinations and copies made during restore,
restrict access independently from the production service, and retain recovery
keys for as long as any backup requires them. Wire does not currently provide
an S3 checkpoint backend or configure S3/KMS policies; external backup tooling
must configure its own encryption and access policies.

Rotate storage keys through the platform's supported procedure. Re-encrypt or
retire older backups according to the same policy; changing a live-volume key
does not necessarily change the key protecting an existing backup. Certificate
rotation affects TLS connections only. Deleting a Wire checkpoint or job is not
secure erasure of media, snapshots or backups.

## Deployment acceptance checks

Before admitting production jobs, record the following evidence in the deployment
runbook:

1. Map each location above, including process temporary storage and any custom
   connector paths, to its actual mounted volume and encryption/key policy.
2. Verify with the storage platform that those volumes and backup destinations
   are encrypted. Confirm a missing mount or unavailable key prevents service
   startup rather than redirecting writes onto the root filesystem.
3. Run a representative stateful job through a completed checkpoint. Inspect
   its state and staging locations to confirm they reside on the intended
   mounts, and verify unrelated service identities cannot read them.
4. Restore a consistent backup into an isolated deployment using the documented
   key-recovery procedure; verify state and checkpoint recovery. Keep restore
   staging on encrypted storage too.
5. Repeat the restore check after a key rotation and verify the backup retention
   policy still permits recovery of every retained backup.

These are operator acceptance checks. Unit tests for Wire cannot prove the
storage platform's encryption, key custody or backup recovery procedures.
