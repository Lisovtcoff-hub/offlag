# OffLag Client

Flutter client for Android and Windows. The application handles email-code authentication, profile and subscription flows, server selection, VPN configuration, and native Windows Xray process management.

## Run

```bash
flutter pub get
flutter run --dart-define=API_BASE_URL=http://127.0.0.1:8080
```

Android Emulator:

```bash
flutter run --dart-define=API_BASE_URL=http://10.0.2.2:8080
```

Windows release builds additionally require the audited runtime files described in `third_party/xray/windows-amd64/README.md`.
