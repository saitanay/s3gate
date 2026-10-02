# BucketCheap Bug Fixes — Oct 2026

End-to-end testing revealed these issues. All fixed in one push.

## Critical — S3 API Broken

| # | Bug | Root Cause | Fix | Status |
|---|-----|-----------|-----|--------|
| 1 | ListBuckets leaks ALL tenants' buckets | Proxy forwarded rclone response unfiltered | Intercept ListBuckets, query rclone backend, filter by userId prefix, strip prefix from names | ✅ |
| 2 | CreateBucket via S3 API → InternalError | Proxy forwarded PUT to rclone serve s3 which can't handle `--` bucket names | Intercept CreateBucket, use `rclone mkdir` directly + record in DB | ✅ |
| 3 | Buckets created via Web UI don't exist on SFTP | `rclone mkdir` ran in fire-and-forget goroutine, failed silently | Made mkdir synchronous, return error to user if it fails, rollback DB record | ✅ |
| 4 | Upload/List/Download all fail with NoSuchBucket | Consequence of #2 and #3 | Fixed by #2 and #3 | ✅ |

## Critical — Security

| # | Bug | Root Cause | Fix | Status |
|---|-----|-----------|-----|--------|
| 5 | No S3 signature verification | AuthenticateS3Request extracted access key but never validated HMAC-SHA256 | Implemented full AWS SigV4 signature verification | ✅ |

## Medium — UX

| # | Bug | Root Cause | Fix | Status |
|---|-----|-----------|-----|--------|
| 6 | Hardcoded phone 9999999999 | Cashfree requires phone, we don't collect one | Intentional — not a bug | N/A |

## Additional improvements

- Added DeleteBucket via S3 API (intercept DELETE /bucket, rclone rmdir, BucketNotEmpty check)
- Added `db.DeleteBucket()` for cleanup
