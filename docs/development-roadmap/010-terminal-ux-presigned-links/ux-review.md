---
target: screen-by-screen terminal UI review
total_score: 19
max_score: 40
na_heuristics: ""
p0_count: 0
p1_count: 5
target_identity: "file:/home/dcorbell/src/orca/workspaces/s3-bucket-manager/feat-presigned-url/internal/tui"
timestamp: 2026-10-07T14-21-27Z
slug: internal-tui
---
# Terminal UI Review

Method: dual-agent (A: /root/tui_design_review, gpt-6-sol high; B: /root/tui_evidence_review, gpt-6.1-sol high).

Review date: 2026-10-07. Target: internal/tui. Scope: existing terminal screens and interactions, with a proposed temporary download-link flow. This is a product review, not implementation acceptance or a branch code review.

## Assessment

Keep the keyboard workflow, clear selection, breadcrumbs, and transfer progress. Rework the application around browsing files and sharing them. Public access and account administration currently dominate ordinary file work.

Design specificity is moderate. Paths, access, and transfers fit this product. Dense shortcut bars and the dashboard label weaken the task hierarchy. Cognitive load is high: the browser footer exposes about 13 actions, and Enter changes meaning between screens.

## Five Priority Issues

1. **P1: Make file navigation complete and predictable.** A nonempty folder list prevents the initial root-file listing. Enter toggles folder access while Right opens it. Open buckets into a combined files-and-folders list. Make Enter open folders, Left/Escape return one level, and preserve position. Put permissions in a separate view. Sources: internal/tui/buckets.go:319,746,1966.
2. **P1: Make displayed state trustworthy.** Late results can display another bucket's folders or an old folder's files. Failed user permission changes leave the requested permission visible. Bucket access checks treat failures as public. Bind results to the correct location, preserve confirmed permissions on failure, and show unknown access when checks fail. Sources: internal/tui/buckets.go:319,333; internal/tui/users.go:557; internal/aws/s3.go:54.
3. **P1: Make consequential actions explicit.** Downloads use os.Create without checking existing files. Enter cycles permissions immediately. Users' r key creates a key, while r refreshes buckets. RotateAccessKey creates an additional key and never retires the old one. Add overwrite decisions, explicit permission editing, and a key-management flow with a clear completion point. Sources: internal/tui/buckets.go:2113; internal/tui/users.go:250,557,603; internal/aws/iam.go:445.
4. **P1: Fit the terminal.** At 80x24, synthetic browser output measured 174x38, bucket detail 114x50, and users 64x55. Long footers, repeated metadata, and lists without scrolling hide controls. Keep a fixed header/footer, scroll the content area, reduce columns on narrow terminals, and use terminal-column-aware Unicode clipping. Sources: internal/tui/buckets.go:1379,1495,1851; internal/tui/users.go:498; internal/tui/styles.go:104.
5. **P1: Make failure and recovery clear.** Errors persist after successful recovery. Failure messages can render in success colors. Navigation can discard background results. Use action-specific status, retry while preserving input, and consistent cancellation. Sources: internal/tui/app.go:101,110,138; internal/tui/buckets.go:451,1515.

## Screen-by-Screen Recommendations

| Screen or feature | Current issue | Recommendation |
|---|---|---|
| Startup and navigation | Active account/profile is absent. Help promises b opens buckets, but users do not handle it. | Persistent account/profile context. Make b/u navigation reliable. |
| Bucket list | Minimum 96-column table. Statistics show the same placeholder for zero, unknown, and unavailable. | Add filtering, compact columns, text access labels, and dated/unavailable statistics. |
| Create bucket | Region is implicit. Empty submission is silent. | Show the selected region, validate the name, retain input after failure. |
| Bucket detail | Metadata, public settings, users, and folders compete on one long screen. Root files can be omitted. | Default to Files, with separate Access and Details views. |
| File browser | Enter changes access. No filter or full-name panel. Refresh resets position and selection. | Enter opens folders. Add filtering, full selected path, and predictable refresh. |
| Create folder | Both Add prefix and New folder create folder markers through different flows. | One New folder action. Show destination and enter the created folder. |
| Bulk selection | Checkboxes work, but selected scope is not prominent. | Show selected count and available bulk actions. Distinguish focused row from selected set. |
| Delete file/folder/bucket | Typed confirmations protect data, but bucket deletion has multiple stages and single-folder/bucket operations lack consistent cancellation. | One clear target-and-scope review. Strong confirmation for recursive deletion. Report partial completion after cancellation. |
| Download | Saves to current directory and can overwrite existing output. Failure can leave a partial file. | Show destination. Offer rename, overwrite, or cancel. Write to a temporary output and finalize on success. |
| Local upload picker | Hidden files are omitted. Picker repeats bucket metadata and overflows height. Selection starts upload immediately. | Dedicated local picker with destination shown, path entry, hidden-file toggle, and replacement handling. |
| URL upload | Optional S3 Key label is technical. Empty submit has no feedback. Failure closes the form. Cancellation differs from local transfers. | Label destination filename/path, show inline validation, preserve inputs for retry, and standardize Escape cancellation. |
| Public access and copying | Copy URL copies unsigned links for private files. PUBLIC/PRIVATE can overstate what checks establish. | Make Temporary link the file-sharing action. Put permanent public settings in Access. Show Unknown when access cannot be checked. |
| Bucket user access | Enter immediately cycles privileges. Pickers lack scrolling and filtering. | Explicit permission picker with current/requested rights and Apply. Scroll and filter large lists. |
| User list | No scrolling or filtering. r creates a key without confirmation. | Show Managed users scope. Add search/paging and separate key actions from refresh. |
| Create user | Three steps have no final review. Escape abandons rather than returning one step. | Name, bucket, permission, then review. Back preserves choices. Explain permission levels in plain words. |
| User detail | Failed permission edits can look applied. | Show confirmed state until success and a clear failure/retry path. |
| Key management | Rotate creates another key and leaves old keys active. | First label honestly as Create new key. Define create, save, replace in dependent systems, and retire old key steps. |
| Credentials | Secret is visible immediately. Enter/Escape/q can dismiss it unsaved. Saving overwrites a fixed path. | Explicit reveal/copy/save, visible save destination, overwrite handling, and an unsaved-exit decision. |
| Help and feedback | Help disagrees with actual shortcuts, omits actions, and is not contextual. | Short relevant footer plus complete contextual help. Clear success, warning, failure, and cancellation messages. |

## Presigned Download-Link Proposal

1. Select a file and choose Share, suggested shortcut s.
2. Choose one hour, 24 hours, seven days, or Custom. Default to one hour.
3. Show the file and requested expiration, including local date, time, and timezone.
4. Generate and copy. Report clipboard success only when copying succeeds.
5. Keep the result available for Copy again or Show link when clipboard access fails.

Use the product label Temporary download link, with Presigned URL in help. Apply the first version to a single file. Signing a folder does not grant access to all files inside it.

The duration runs from generation, not from first use. Editing the duration requires generating a new link. The link can be used more than once before expiry. Anyone holding the link can use the permissions it grants. Keep these facts in concise contextual help.

AWS documents a seven-day maximum for SDK-created links. Temporary credentials can expire sooner. Show any known credential limit before generation, and do not promise access beyond it. Policies can also restrict use. Source: https://docs.aws.amazon.com/AmazonS3/latest/userguide/using-presigned-url.html

Presigning does not remove existing public grants. Moving existing public folders to private access requires a separate explicit change. Creating a link should not silently change bucket policies.

## Heuristic Scores

These are provisional editorial scores, informed by source and synthetic model fixtures. They are not usability-study results.

| Heuristic | Score out of 4 |
|---|---:|
| Visibility of system status | 2 |
| Match to users' expectations | 2 |
| User control | 2 |
| Consistency | 2 |
| Error prevention | 1 |
| Recognition over recall | 2 |
| Efficiency | 3 |
| Minimalism | 2 |
| Error recovery | 1 |
| Help | 2 |

Total: 19/40. All ten heuristics apply.

## User Perspectives and Smaller Improvements

- Frequent operator: missing filtering, lost position, and short-terminal overflow slow repeated work.
- New user: Enter, public/private badges, and Copy URL create misleading expectations.
- Accessibility-dependent terminal user: hidden controls, emoji width, byte-based clipping, and assumed dark backgrounds need verification.

Keep text labels alongside access icons. Preserve the current palette while testing dark and light terminal backgrounds. Improve filenames and paths before adding cosmetic effects.

## Evidence

Assessment A read all production terminal sources and README without seeing detector findings. Assessment B ran detector, existing tests, and synthetic rendering/routing fixtures independently.

The detector returned [] with zero findings. Its HTML/CSS focus makes that weak evidence for Go terminal quality. No browser, overlay, or live server applied.

Passed: go test ./internal/tui ./internal/aws ./internal/httpcopy ./internal/httpresolve ./internal/progress.

Four temporary evidence tests passed using go test ./internal/tui -run TestAuditB -v. They demonstrated existing behavior, not fixes. Temporary test source was removed. Sample renders remain in tmp/tui-ux-audit-renders.txt.

No live AWS calls, clipboard execution, real overwrite, interactive terminal, screenshots, or screen-reader checks were performed. No application code was changed.

## Product Decision for Next Step

Recommended scope: a focused overhaul of file navigation, trustworthy state, safer actions, and terminal sizing, then temporary links. Alternative scopes are full screen overhaul or sharing only. Choose scope before implementation and claim a phase for substantial work.
