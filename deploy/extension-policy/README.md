# Enterprise deployment of the Continuum extension (FR-X06)

Replace `EXTENSION_ID` with the store id (Chrome Web Store / Edge Add-ons) or with the id of a self-hosted CRX.

| File | Browser · OS | Install |
|---|---|---|
| `chrome-linux.json` | Chrome / Chromium on Linux | copy to `/etc/opt/chrome/policies/managed/continuum.json` (Chromium: `/etc/chromium/policies/managed/`) |
| `chrome-windows.reg` | Chrome on Windows | `reg import chrome-windows.reg` or the same values via GPO (ADMX: `ExtensionInstallForcelist`) |
| `edge-windows.reg` | Edge on Windows | `reg import edge-windows.reg` / Intune |
| `continuum-extension.mobileconfig` | Chrome + Edge on macOS | deploy with your MDM (Jamf, Intune, Kandji) |
| `updates.xml` | self-hosted CRX (outside the stores) | serve over HTTPS and use its URL instead of the store update URL |

`ExtensionInstallForcelist` installs the extension for every user and prevents removal; `ExtensionSettings`
pins it to the toolbar and keeps the native-messaging permission (T3, optional) available. Private tester
builds are published to the Chrome Web Store *trusted testers* channel by `.github/workflows/release.yml`
(`store_channel: trustedTesters`).
