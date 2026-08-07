# Security policy

Please report suspected vulnerabilities privately through [GitHub's private vulnerability reporting](../../security/advisories/new). Do not open a public issue for a security-sensitive report.

Include affected versions, reproduction steps, impact, and any suggested mitigation. We will acknowledge reports and coordinate disclosure through GitHub.

Uncanny Lab does not distribute model checkpoints or converted model bundles. Operators obtain and store them separately under the applicable upstream terms.

Checkpoint networking is disabled by default. `UNCANNY_ENABLE_CHECKPOINT_DOWNLOADS=true` enables only the fixed Bundle B catalog, with HTTPS host restrictions and exact byte and SHA-256 verification. It does not accept arbitrary URLs or bake source caches into the image.
