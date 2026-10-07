# Terminal Screen Captures

These text captures use deterministic sample data and remove terminal color codes.
No live cloud service, clipboard, credential, or signed download URL was used.
The screen fixtures check every mode at 80x24, 120x40, and 60x15.
Color contrast and real terminal behavior still require operator inspection.

## Files with a long Unicode path (80x24)

```text
s3m  Account: Unknown  Profile: default  Region: Unknown
example-bucket  [Files]  Access  Details
────────────────────────────────────────────────────────────────────────────────
s3://example-bucket/資料/long-folder/long-folder/long-folder/
50 entries  /: Filter  Selected: 0
  NAME                                                           SIZE
  [ ] sample-38-資料.txt                                         1.0 KB
  [ ] sample-39-資料.txt                                         1.0 KB
  [ ] sample-40-資料.txt                                         1.0 KB
  [ ] sample-41-資料.txt                                         1.0 KB
  [ ] sample-42-資料.txt                                         1.0 KB
  [ ] sample-43-資料.txt                                         1.0 KB
  [ ] sample-44-資料.txt                                         1.0 KB
  [ ] sample-45-資料.txt                                         1.0 KB
  [ ] sample-46-資料.txt                                         1.0 KB
  [ ] sample-47-資料.txt                                         1.0 KB
  [ ] sample-48-資料.txt                                         1.0 KB
> [ ] sample-49-資料.txt                                         1.0 KB
Selected: 資料/long-folder/long-folder/long-folder/sample-49-資料.txt
Enter: Open  /: Filter  Space: Select  m: Actions  Tab: Access
```

## Managed users after paging (60x15)

```text
s3m  Account: Unknown  Profile: default  Region: Unknown
 Users
Managed users
50 entries  /: filter
  USERNAME                           KEYS  CREATED
  sample-48-資料                     1       2026-01-02
> sample-49-資料                     1       2026-01-02
Showing 49-50 of 50

enter: access K: keys c: new d: delete r: refresh /: filter
```

## Permission review (60x15)

```text
s3m  Account: Unknown  Profile: default  Region: Unknown
 Users > sample-user > Review
Review permission change
User: sample-user
Bucket: example-bucket
Current: Read
Requested: Read and upload

enter: Apply  esc: cancel
```

## Key management (120x40)

```text
s3m  Account: Unknown  Profile: default  Region: Unknown
 Users > sample-user > Keys
Access keys: sample-user
> EXAMPLE_KEY_ID  Inactive  2026-01-02

c: new key  x: deactivate  a: reactivate  d: delete inactive  r: refresh  esc: back
```

## Masked credentials (80x24)

```text
s3m  Account: Unknown  Profile: default  Region: Unknown
 Users > Credentials
New credentials
Username: sample-user
Access key ID: EXAMPLE_KEY_ID
Secret: ********************************
Save this secret now. It cannot be retrieved later.
Save destination: /example/credentials/sample-user-EXAMPLE_KEY_ID-credentials.js
on

v: reveal  c: copy  s: save  a: captured  enter: Done
```

## New user review (80x24)

```text
s3m  Account: Unknown  Profile: default  Region: Unknown
 Users > New user > Review
Review new user
Username: sample-user
Bucket: example-bucket
Access: Read and upload

enter: Create user  esc: back
```

## Temporary-link duration selection (80x24)

```text
s3m  Account: Unknown  Profile: default  Region: Unknown
Temporary download link
────────────────────────────────────────────────────────────────────────────────
File: s3://example-bucket/資料/sample.txt

Choose how long the link should work:
> 1 hour
  24 hours
  7 days
  Custom
Up/Down: Choose  Enter: Review  Esc: Cancel
```

## Compact URL upload (60x15)

```text
s3m  Account: Unknown  Profile: default  Region: Unknown
 Upload from URL
Destination: s3://example-bucket/資料/long-folder/long-…
URL:
> Paste HTTPS or WeTransfer URL…
Destination filename or path (optional):
> Key (blank = current prefix + filename)
enter: review destination  tab: next field  esc: cancel
```
