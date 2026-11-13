# 0001. Object storage: Garage instead of MinIO

**Status:** accepted (phase 0)

**Context.** The specification names MinIO for files and recordings. During phase 0 the official MinIO images were no longer downloadable (Docker Hub repositories gone, quay.io refused anonymous pulls), so a MinIO deployment could not be reproduced.

**Decision.** Use Garage (`dxflrs/garage`), an open-source S3-compatible store, behind a small image of ours that initialises the layout, the application key and the buckets on first start. All code speaks the S3 API (minio-go client library), so the store can be replaced without touching the services.

**Consequences.** Single node today (replication factor 1): production needs a replicated layout or regular backups of the data volume (see deployment.md). The Garage container runs as root inside its container (hardening item). If the bank prefers MinIO, only `ops/garage` and the compose service change.
