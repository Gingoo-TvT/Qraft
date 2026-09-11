# Security

Qraft is designed for a local computer or a trusted private workspace. It executes generated programs in a separate Linux sandbox; the sandbox container requires elevated container privileges and must stay on its internal network.

The current desktop release does not provide a complete account/login or multi-tenant permission system. Everyone connected to the same workspace shares its question bank and model settings. Do not expose an unauthenticated development-mode service directly to the Internet. HTTP listeners default to loopback.

API keys entered in the service settings are encrypted at rest with the instance's settings-encryption key. Back up that key with the database; do not commit either. Models receive the content needed for the requested generation or embedding operation. Qraft does not automatically synchronize banks between independent instances.

To report a vulnerability, use [GitHub private vulnerability reporting](https://github.com/Gingoo-TvT/Qraft/security/advisories/new). Include the affected version, a minimal reproduction using synthetic data, and the impact. Do not post keys, user data, or exploitable production URLs in a public issue.
