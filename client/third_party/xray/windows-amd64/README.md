# Windows Xray runtime

The Windows client expects these runtime files in this directory before a release build:

- `xray.exe`
- `wintun.dll`
- `geoip.dat`
- `geosite.dat`

They are third-party release artifacts and are intentionally not committed to this source repository. Obtain them from the official Xray-core and Wintun distributions, verify their checksums, and review their licenses before packaging.
