# OffLag

[![CI](https://github.com/lisovcoff/offlag/actions/workflows/ci.yml/badge.svg)](https://github.com/lisovcoff/offlag/actions/workflows/ci.yml)

A cross-platform VPN service composed of a Flutter client and a Go backend. OffLag provides passwordless authentication, subscription and payment flows, VPN node selection, 3x-ui panel synchronization, and native Windows Xray process management.

> This public repository is a portfolio-safe source version. Production databases, credentials, signing keys, logs, customer data, and third-party VPN runtime binaries are not included.

## What the project does

- authenticates users with one-time email codes and short-lived JWT access tokens;
- maintains revocable refresh sessions and per-device logout;
- manages balances, promo codes, premium activation, tariffs, and YooKassa payments;
- returns normalized VPN nodes with panel load and availability statistics;
- synchronizes users and usage data with multiple 3x-ui panels through isolated Python adapters;
- selects VPN nodes using priority, load, and measured TCP latency;
- builds and launches Windows Xray TUN configurations from the Flutter client;
- provides announcements, minimum application version checks, and account email changes;
- includes protected administrative API routes and a separate administrative web interface.

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

The Go application is the source of truth for users, sessions, payments, subscriptions, announcements, and VPN panel configuration. Python helpers isolate the 3x-ui client dependency from the API layer. The Flutter client communicates only with the normalized backend API.

More detail is available in [docs/architecture.md](docs/architecture.md).

## Technology stack

- **Backend:** Go 1.23, Fiber, JWT, SQLite
- **Panel integrations:** Python 3.12, py3xui, requests
- **Client:** Flutter, Dart, Dio, secure storage
- **VPN runtime:** Xray, Wintun, TUN configuration generation
- **Payments and messaging:** YooKassa, SMTP email
- **Infrastructure:** Docker Compose, GitHub Actions
- **Testing:** Go test, Go vet, Python compile checks, Flutter Analyze, Flutter Test

## Quick start with Docker

Create local configuration and start the backend:

```bash
cp .env.example .env
docker compose up --build
```

PowerShell:

```powershell
Copy-Item .env.example .env
docker compose up --build
```

The API listens on `http://localhost:8080`. Health check:

```bash
curl http://localhost:8080/health
```

Replace all placeholder credentials in `.env` before exposing the service outside a local environment.

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

## Configuration

The backend reads configuration from environment variables. The complete template is in `.env.example`.

Important groups:

- `JWT_SECRET`, `ADMIN_EMAILS`, `ADMIN_UI_PASSWORD` — authentication and administration;
- `SMTP_*` — one-time codes and account notifications;
- `YOOKASSA_*` — payment creation and webhook authentication;
- `DB_PATH`, `PORT`, `CORS_ALLOW_ORIGINS` — service runtime;
- `PYTHON_BIN`, `PYTHON_SCRIPT_DIR` — 3x-ui adapter runtime.

The Flutter API address can be changed at build or run time through `--dart-define=API_BASE_URL=...`.

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

GitHub Actions runs both pipelines for every push and pull request.

## Project structure

```text
backend/                    Go API, SQLite schema, tests, and 3x-ui helpers
client/                     Flutter application for Android and Windows
docs/                       Architecture and security notes
.github/workflows/ci.yml    Backend and Flutter checks
compose.yaml                Local backend container
.env.example                Safe configuration template
```

## Security and operational notes

- Access tokens are short-lived and refresh tokens are stored as hashes.
- Login, verification, and payment creation endpoints are rate-limited.
- Administrative API routes require an authenticated administrator email.
- Production secrets, SQLite databases, signing materials, and VPN panel credentials are excluded from Git.
- Use HTTPS, restricted CORS origins, strong random secrets, protected backups, and an edge rate limiter in production.
- External panel APIs and VPN runtime behavior require ongoing compatibility testing.

See [docs/security.md](docs/security.md) for additional details.

## Project status

This repository contains the complete portfolio source architecture derived from the working OffLag application. The older `server/` directory from the supplied archive was not retained: its Go application was an earlier build, while `backend/main.go` contains the newer API required by the current Flutter client. Third-party runtime binaries must be supplied separately for distribution builds.

## Author

Sergey Inozemtsev — Python backend developer

GitHub: https://github.com/lisovcoff
