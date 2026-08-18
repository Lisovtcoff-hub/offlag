# Architecture

OffLag is split into two applications:

- `client/` — Flutter application for Android and Windows;
- `backend/` — Go Fiber API with SQLite persistence and Python adapters for 3x-ui panels.

```text
Flutter client
     |
     | HTTPS + JWT
     v
Go Fiber API
     |
     +-- SQLite: users, sessions, payments, subscriptions, announcements
     +-- SMTP: authentication and account emails
     +-- YooKassa: payment creation and webhooks
     +-- Python helpers: 3x-ui synchronization and panel statistics
```

The client receives normalized VPN node data from `/vpn/nodes`. Provider-specific panel communication stays behind the backend's Python helper boundary.
