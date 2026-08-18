# Security notes

- Secrets are loaded from environment variables and must never be committed.
- Access tokens are short-lived; refresh tokens are stored as SHA-256 hashes and can be revoked per device.
- Authentication and payment endpoints use rate limiting.
- Administrative API routes require both a valid session and an email listed in `ADMIN_EMAILS`.
- The administrative web interface uses a separate password and expiring in-memory sessions.
- YooKassa webhooks support Basic Authentication.
- SQLite uses foreign keys, a busy timeout, and WAL mode.
- Release signing files, production databases, VPN credentials, logs, and third-party runtime binaries are excluded from the public repository.

For production, use HTTPS, a long random `JWT_SECRET`, strong administrative credentials, restricted CORS origins, protected backups, and a reverse proxy with request limits.
