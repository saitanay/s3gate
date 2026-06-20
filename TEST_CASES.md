# S3Gate Test Cases

Endpoint: `https://s3.x2u.in`  
Credentials: `minioadmin` / `minioadmin`

## Setup

```bash
# Configure AWS CLI
aws configure set aws_access_key_id minioadmin
aws configure set aws_secret_access_key minioadmin
aws configure set default.region us-east-1

export S3="--endpoint-url https://s3.x2u.in"
```

---

## 1. Bucket Operations

### 1.1 Create Bucket

```bash
aws s3 mb s3://test-bucket $S3
# Expected: make_bucket: test-bucket
```

### 1.2 List Buckets

```bash
aws s3 ls $S3
# Expected: test-bucket appears in list
```

### 1.3 Create Multiple Buckets

```bash
aws s3 mb s3://bucket-a $S3
aws s3 mb s3://bucket-b $S3
aws s3 mb s3://bucket-c $S3
aws s3 ls $S3
# Expected: all 3 buckets listed
```

### 1.4 Delete Empty Bucket

```bash
aws s3 rb s3://bucket-c $S3
aws s3 ls $S3
# Expected: bucket-c gone
```

### 1.5 Delete Non-Empty Bucket (should fail)

```bash
echo "data" | aws s3 cp - s3://bucket-b/file.txt $S3
aws s3 rb s3://bucket-b $S3
# Expected: error — bucket not empty
```

### 1.6 Force Delete Non-Empty Bucket

```bash
aws s3 rb s3://bucket-b --force $S3
# Expected: removes all objects then deletes bucket
```

---

## 2. File Upload & Download

### 2.1 Upload Small File

```bash
echo "hello s3gate" > /tmp/test-small.txt
aws s3 cp /tmp/test-small.txt s3://test-bucket/small.txt $S3
# Expected: upload successful
```

### 2.2 Download File

```bash
aws s3 cp s3://test-bucket/small.txt /tmp/downloaded.txt $S3
diff /tmp/test-small.txt /tmp/downloaded.txt
# Expected: no diff — files identical
```

### 2.3 Upload Large File (10MB)

```bash
dd if=/dev/urandom of=/tmp/test-large.bin bs=1M count=10 2>/dev/null
aws s3 cp /tmp/test-large.bin s3://test-bucket/large.bin $S3
# Expected: upload completes (multipart may kick in)
```

### 2.4 Download Large File & Verify

```bash
aws s3 cp s3://test-bucket/large.bin /tmp/downloaded-large.bin $S3
md5 /tmp/test-large.bin /tmp/downloaded-large.bin
# Expected: checksums match
```

### 2.5 Upload with Content-Type

```bash
echo '{"key":"value"}' > /tmp/test.json
aws s3 cp /tmp/test.json s3://test-bucket/data.json --content-type application/json $S3
aws s3api head-object --bucket test-bucket --key data.json $S3
# Expected: ContentType shows application/json
```

---

## 3. List & Metadata

### 3.1 List Objects in Bucket

```bash
aws s3 ls s3://test-bucket/ $S3
# Expected: lists small.txt, large.bin, data.json
```

### 3.2 List with Prefix

```bash
aws s3 ls s3://test-bucket/s $S3
# Expected: only small.txt
```

### 3.3 Head Object (metadata)

```bash
aws s3api head-object --bucket test-bucket --key small.txt $S3
# Expected: returns ContentLength, LastModified, ETag
```

---

## 4. Folder / Prefix Operations

### 4.1 Create Nested Structure

```bash
echo "a" | aws s3 cp - s3://test-bucket/folder1/file-a.txt $S3
echo "b" | aws s3 cp - s3://test-bucket/folder1/file-b.txt $S3
echo "c" | aws s3 cp - s3://test-bucket/folder1/sub/file-c.txt $S3
echo "d" | aws s3 cp - s3://test-bucket/folder2/file-d.txt $S3
```

### 4.2 List Top-Level Prefixes

```bash
aws s3 ls s3://test-bucket/ $S3
# Expected: shows folder1/ and folder2/ as prefixes + root files
```

### 4.3 List Nested Prefix

```bash
aws s3 ls s3://test-bucket/folder1/ $S3
# Expected: file-a.txt, file-b.txt, sub/
```

### 4.4 Recursive List

```bash
aws s3 ls s3://test-bucket/folder1/ --recursive $S3
# Expected: all 3 files under folder1/ including sub/file-c.txt
```

### 4.5 Delete Entire Prefix (folder)

```bash
aws s3 rm s3://test-bucket/folder1/ --recursive $S3
aws s3 ls s3://test-bucket/folder1/ $S3
# Expected: nothing listed — folder1 gone
```

---

## 5. Copy & Move

### 5.1 Copy Object Within Bucket

```bash
aws s3 cp s3://test-bucket/small.txt s3://test-bucket/copy-of-small.txt $S3
aws s3 ls s3://test-bucket/copy-of-small.txt $S3
# Expected: copy exists
```

### 5.2 Move (Rename) Object

```bash
aws s3 mv s3://test-bucket/copy-of-small.txt s3://test-bucket/renamed.txt $S3
aws s3 ls s3://test-bucket/renamed.txt $S3
# Expected: renamed.txt exists, copy-of-small.txt gone
```

### 5.3 Copy Between Buckets

```bash
aws s3 mb s3://dest-bucket $S3
aws s3 cp s3://test-bucket/small.txt s3://dest-bucket/small.txt $S3
aws s3 ls s3://dest-bucket/ $S3
# Expected: small.txt in dest-bucket
```

---

## 6. Deletion

### 6.1 Delete Single File

```bash
aws s3 rm s3://test-bucket/renamed.txt $S3
aws s3 ls s3://test-bucket/renamed.txt $S3
# Expected: file gone
```

### 6.2 Bulk Delete

```bash
for i in $(seq 1 10); do
  echo "file $i" | aws s3 cp - s3://test-bucket/bulk/file-$i.txt $S3
done
aws s3 rm s3://test-bucket/bulk/ --recursive $S3
aws s3 ls s3://test-bucket/bulk/ $S3
# Expected: all 10 files deleted
```

### 6.3 Delete Non-Existent Object (idempotent)

```bash
aws s3 rm s3://test-bucket/does-not-exist.txt $S3
# Expected: no error (S3 delete is idempotent)
```

---

## 7. Concurrency

### 7.1 Parallel Uploads

```bash
for i in $(seq 1 20); do
  echo "concurrent $i" | aws s3 cp - s3://test-bucket/concurrent/file-$i.txt $S3 &
done
wait
aws s3 ls s3://test-bucket/concurrent/ $S3 | wc -l
# Expected: 20 files uploaded
```

### 7.2 Parallel Downloads

```bash
mkdir -p /tmp/s3gate-concurrent
for i in $(seq 1 20); do
  aws s3 cp s3://test-bucket/concurrent/file-$i.txt /tmp/s3gate-concurrent/file-$i.txt $S3 &
done
wait
ls /tmp/s3gate-concurrent/ | wc -l
# Expected: 20 files downloaded
```

### 7.3 Concurrent Read + Write

```bash
# Writer
for i in $(seq 1 10); do
  echo "write $i" | aws s3 cp - s3://test-bucket/rw/data.txt $S3
  sleep 0.2
done &

# Reader
for i in $(seq 1 10); do
  aws s3 cp s3://test-bucket/rw/data.txt /dev/null $S3 2>/dev/null
  sleep 0.2
done &

wait
# Expected: no crashes or 500 errors
```

### 7.4 Sync Directory (parallel transfers)

```bash
mkdir -p /tmp/s3gate-sync
for i in $(seq 1 50); do echo "file $i" > /tmp/s3gate-sync/f$i.txt; done
aws s3 sync /tmp/s3gate-sync/ s3://test-bucket/synced/ $S3
aws s3 ls s3://test-bucket/synced/ $S3 | wc -l
# Expected: 50 files synced
```

---

## 8. Overwrite & Versioning

### 8.1 Overwrite Existing File

```bash
echo "version 1" | aws s3 cp - s3://test-bucket/versioned.txt $S3
echo "version 2" | aws s3 cp - s3://test-bucket/versioned.txt $S3
aws s3 cp s3://test-bucket/versioned.txt - $S3
# Expected: outputs "version 2"
```

### 8.2 Zero-Byte File

```bash
aws s3 cp /dev/null s3://test-bucket/empty.txt $S3
aws s3api head-object --bucket test-bucket --key empty.txt $S3
# Expected: ContentLength = 0
```

---

## 9. Presigned URLs (if supported)

### 9.1 Generate Presigned URL

```bash
aws s3 presign s3://test-bucket/small.txt --expires-in 60 $S3
# Expected: returns a signed URL (may or may not work depending on rclone support)
```

### 9.2 Fetch via Presigned URL

```bash
URL=$(aws s3 presign s3://test-bucket/small.txt --expires-in 60 $S3)
curl -s "$URL"
# Expected: file content or 403 (rclone may not support presigned URLs)
```

---

## 10. Error Cases

### 10.1 Access Non-Existent Bucket

```bash
aws s3 ls s3://nonexistent-bucket/ $S3
# Expected: error — NoSuchBucket or similar
```

### 10.2 Download Non-Existent Key

```bash
aws s3 cp s3://test-bucket/no-such-file.txt /tmp/nope.txt $S3
# Expected: error — 404 / NoSuchKey
```

### 10.3 Invalid Credentials

```bash
AWS_ACCESS_KEY_ID=wrong AWS_SECRET_ACCESS_KEY=wrong \
  aws s3 ls --endpoint-url https://s3.x2u.in
# Expected: error — 403 AccessDenied
```

### 10.4 Upload to Non-Existent Bucket

```bash
echo "test" | aws s3 cp - s3://ghost-bucket/file.txt $S3
# Expected: error — NoSuchBucket
```

---

## 11. Special Characters & Edge Cases

### 11.1 Key with Spaces

```bash
echo "spaces" | aws s3 cp - "s3://test-bucket/path with spaces/my file.txt" $S3
aws s3 cp "s3://test-bucket/path with spaces/my file.txt" - $S3
# Expected: round-trips correctly
```

### 11.2 Key with Unicode

```bash
echo "unicode" | aws s3 cp - "s3://test-bucket/日本語/テスト.txt" $S3
aws s3 ls "s3://test-bucket/日本語/" $S3
# Expected: lists テスト.txt
```

### 11.3 Long Key Name (200+ chars)

```bash
LONG=$(python3 -c "print('a'*250)")
echo "long" | aws s3 cp - "s3://test-bucket/$LONG.txt" $S3
# Expected: success or graceful error (depends on SFTP backend limits)
```

### 11.4 Key with Special Chars

```bash
echo "special" | aws s3 cp - "s3://test-bucket/special!@#\$%&()chars.txt" $S3
aws s3 cp "s3://test-bucket/special!@#\$%&()chars.txt" - $S3
# Expected: round-trips or known limitation
```

---

## Cleanup

```bash
aws s3 rb s3://test-bucket --force $S3
aws s3 rb s3://dest-bucket --force $S3
aws s3 rb s3://bucket-a --force $S3
```

---

## Notes

- **Presigned URLs**: rclone `serve s3` may not support them — test and document behavior
- **Multipart uploads**: large file test (2.3) exercises this path
- **Rate limiting**: SFTP backend (Hetzner) may throttle — concurrent tests reveal limits
- **Eventual consistency**: SFTP is strongly consistent unlike real S3, so no read-after-write delays expected
- **No versioning**: rclone S3 gateway does not support S3 object versioning
