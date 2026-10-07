# Independent Branch Review

**Reviewed SHA:** `85deb404aebdcb84553e07e8d8c87eae63c45b26`
**Base:** `origin/main` `bfd15bcb5b2a5a0f385e7398733acae8f6daff88`
**Coverage:** Full diff `origin/main...HEAD` (53 files, ~7,864 additions). Read end-to-end: `internal/aws/` (presign.go, s3.go, iam.go, client.go, creation.go, metadata.go, transfer_safety.go), `internal/httpcopy/copy.go`, `internal/tui/` (app.go, buckets.go, users.go, share.go, credentials.go, transfer.go, urlupload.go, filepicker.go, list.go, status.go, browser_view.go, layout.go, validation.go, styles.go), `cmd/`, `internal/model/types.go`, all new test files, and the three design documents. Verified multipart conditional-write propagation against the installed AWS SDK manager source. All checks (race tests, build, lint, vulncheck) reported passing; no checks rerun.

## Verified findings

### Medium: cancelled or failed recursive deletes discard the completed-deletion count

**File:line:** `internal/tui/buckets.go:401-411`

**Trigger:** Press Esc or Ctrl+C during a folder delete or multi-selection delete after at least one batch has been removed (or a mid-delete service failure).

**Impact:** The handler clears `m.deleteProgress` (which held "Deleting folder... N objects removed") and shows only "Bulk delete cancelled". The operator cannot tell how many objects were already deleted, so they cannot judge what still needs cleanup. The same gap applies on mid-operation failure: the error text survives, the completed count does not. This contradicts the milestone requirement "Retain cancellation and actual completed counts so operators know what still needs cleanup" (implementation-plan.md:213), and it is inconsistent with the neighboring delete-bucket path at `buckets.go:367-374`, which correctly preserves `m.deleteProgress` in its stopped message ("Bucket deletion stopped. Completed deletions remain. " + progress). The branch rewrote this code path and added cancellation, so this is new-code behavior failing the plan, not inherited behavior.

**Concrete fix:** In the `m.bulkDeleting` branch of the `errMsg` case, capture the progress text before clearing, e.g. `progress := m.deleteProgress` then `m.detailMessage = "Bulk delete cancelled. " + progress` (and append the partial count to failure messages the same way). Optionally return the deleted count inside `DeletePrefix`'s error path in `internal/aws/s3.go` so the message can state an exact number rather than reusing display text.

### Critical / High

No critical or high findings.

## What was verified safe

- Presigned links: one credential snapshot per request, refresh through the configured provider, signing-time and expiry parsed back from the signed URL, expiry-over-credential rejection, explicit consent for shortened sessions, no credentials in `String`/`GoString`, folder keys rejected, no policy mutation on link creation.
- Transfers: destination review required, unknown check result blocks mutation, overwrite needs explicit `o`, temp-file publication with identity/size/mtime recheck, exclusive `os.Link` for new files, cancellable multipart with uncancelled bounded cleanup, `IfMatch`/`IfNoneMatch` confirmed propagating into `CompleteMultipartUpload`.
- Stale results: request counters plus bucket/prefix identity on browse, prefix, bucket-list, user, and metadata loads; share flow gated by request ID; transfer checks gated by ID; permission results gated by request ID and username.
- Policy preservation: raw-JSON statement filtering keeps unrelated statements, validates every statement before mutation, managed grants identified by `s3m-public-<prefix>` SID, empty-policy deletes restore blocks.
- Key lifecycle: two-key limit, deactivate-before-delete, typed key-ID confirmation, re-list recheck before delete, session-key warning, masked secret, `0600`/`O_EXCL` save, preserved secret on clipboard failure.
- CLI and JSON fields preserved; new fields are additive.

## Low notes (5)

1. `buckets.go:564-572`, sender at `1782-1794`, confirm at `1986`: `folderCountedMsg` carries no bucket or request identity, so a slow folder count that resolves after the user exits to the bucket list opens a delete dialog resolved against whatever bucket row the cursor sits on at confirm time (count and public-grant flag still describe the stale folder). Identical defect exists on `main`, so it is not a regression, but this milestone hardened the same class elsewhere; worth fixing by tagging the message with bucket name and a request counter.
2. `users.go:271-308`: handlers for `usersLoadedMsg`, `userAccessLoadedMsg`, `accessUpdatedMsg`, `createBucketPickerLoadedMsg`, `detailBucketPickerLoadedMsg` are dead code; only tests emit those types now. Harmless, but they can mislead future readers about live paths.
3. `browser_view.go:271,279` vs `buckets.go:1090`: the Access tab renders `height-9` rows while cursor clamping uses `browseVisibleRows()` (`height-10` when a status line exists), so the focused row can drift one line outside the visible window.
4. `internal/aws/s3.go:938-947`: when removing the last managed grant, `SetPrefixesPrivate` re-enables all four public access blocks, including blocks that were off for unrelated reasons. Conservative direction, but it can disable unrelated public ACL access.
5. `buckets.go:1238-1249`: bucket-side user removal builds the displayed list locally instead of re-listing (`ListBucketUsers` is called on the edit path at `1198` but not the remove path), so the shown access list can silently omit concurrent changes.
