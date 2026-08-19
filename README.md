# OffLag

[![CI](https://github.com/lisovcoff/offlag/actions/workflows/ci.yml/badge.svg)](https://github.com/lisovcoff/offlag/actions/workflows/ci.yml)

Cross-platform VPN service with a Flutter client and Go backend. The system handles passwordless authentication, subscriptions and payments, VPN node selection, 3x-ui panel synchronization, and Windows Xray runtime management.

## Highlights

- one-time email code login with short-lived JWT access tokens and revocable refresh sessions;
- balances, promo codes, tariffs, premium activation, and YooKassa payment flows;
- normalized VPN node catalog with availability, load, and measured TCP latency;
- multi-panel 3x-ui synchronization through isolated Python adapters;
- Windows client integration for Xray TUN configuration and launch;
- separate administrative API routes and administrative web interface.

## Stack

- **Backend:** Go 1.23, Fiber, JWT, SQLite
- **Integrations:** Python 3.12, py3xui, requests
- **Client:** Flutter, Dart, Dio, secure storage
- **VPN runtime:** Xray, Wintun
- **Payments and messaging:** YooKassa, SMTP email
- **Infrastructure:** Docker Compose, GitHub Actions
- **Testing:** Go test, Go vet, Python compile checks, Flutter Analyze, Flutter Test

## Architecture

```text
Flutter client (Android / Windows)
               |
               | HTTPS + JWT
               v
        Go Fiber backend
          /      |       \
     SQLite    YooKassa   SMTP
          \
       Python 3x-ui adapters
               |
          VPN panels
```

The Go backend is the source of truth for users, sessions, payments, subscriptions, announcements, and VPN panel configuration. Python helpers isolate 3x-ui client logic from the API layer. The Flutter client communicates only with the normalized backend API.

Additional notes: [architecture](docs/architecture.md), [security](docs/security.md).

## Run locally

```bash
cp .env.example .env
docker compose up --build
```

PowerShell:

```powershell
Copy-Item .env.example .env
docker compose up --build
```

The API listens on `http://localhost:8080`.

Health check:

```bash
curl http://localhost:8080/health
```

## Run the Flutter client

```bash
cd client
flutter pub get
flutter run --dart-define=API_BASE_URL=http://127.0.0.1:8080
```

For Android Emulator:

```bash
flutter run --dart-define=API_BASE_URL=http://10.0.2.2:8080
```

Windows distribution builds require audited Xray and Wintun runtime files. See `client/third_party/xray/windows-amd64/README.md`.

## Development and tests

Backend:

```bash
cd backend
go test ./...
go vet ./...
python -m compileall -q get_uuid_3xui.py stats_3xui.py sync_3xui.py
```

Client:

```bash
cd client
flutter pub get
flutter analyze
flutter test
```

## Repository layout

```text
backend/                    Go API, SQLite schema, tests, and 3x-ui helpers
client/                     Flutter application for Android and Windows
docs/                       Architecture and security notes
.github/workflows/ci.yml    Backend and Flutter checks
compose.yaml                Local backend container
.env.example                Safe configuration template
```

## Notes

- This public repository excludes production databases, credentials, signing keys, logs, customer data, and VPN panel secrets.
- External panel APIs and VPN runtime behavior require ongoing compatibility testing.
- Distribution builds require separately managed third-party runtime binaries.
