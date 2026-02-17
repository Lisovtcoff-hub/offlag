# OffLag

OffLag is a Flutter client app for the OffLag VPN service. It includes user onboarding,
server selection, and billing flows, and builds Xray/Sing-box configs for the tunnel.

## Features
- Email-based onboarding and profile management
- Server list with latency/ping indicators
- Balance, promo codes, and top-up flows
- Desktop window sizing and theming

## Tech Stack
- Flutter (Dart)
- dio, flutter_secure_storage, shared_preferences
- Xray and Sing-box config builders

## Getting Started
```bash
flutter pub get
flutter run
```

## Windows VPN (developer)
1. Ensure runtime files exist in `third_party/xray/windows-amd64`:
   `xray.exe`, `wintun.dll`, `geoip.dat`, `geosite.dat`.
2. Run app on Windows with admin rights (route/netsh commands require elevation).
3. Turn VPN ON in UI:
   app copies runtime files to `%APPDATA%\\offlag\\xray-runtime`,
   renders `client-tun.jsonc` from `assets/vpn/client-tun.template.jsonc`,
   starts `xray.exe`, and applies routing commands.
4. Inspect logs in `%APPDATA%\\offlag\\logs\\windows-vpn.log`.
5. Validate:
   `2ip.io` / `ifconfig.me` should go through proxy,
   `.ru` and `geoip:private` should go via `direct-lan` (`sendThrough=<LAN_IP>`),
   `udp:443` should be blocked.

## Notes
- Desktop window size is fixed to 460x800.
- App name and bundle IDs are set to OffLag.
