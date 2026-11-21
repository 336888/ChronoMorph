# Slacker Android (early preview)

This is an installable Android UI preview, **not a functioning VPN client**.

- Android minSdk 28, targetSdk 35, compatible with Android 17 runtime in principle.
- Node link parser and ephemeral node list.
- VPN service manifest declaration; intentionally does not establish a TUN until a native forwarding engine is integrated.
- No credentials are persisted. No network traffic is redirected.
- CI builds a debug APK with the GitHub Actions Android preview workflow.

## Missing before functional release
1. Native Slacker Go core built as a supported Android library and Android-to-Go API.
2. VpnService TUN descriptor bridge, protect() for tunnel sockets, DNS, IPv4/IPv6 forwarding.
3. REALITY/TLS/UDP/chain routing integration and end-to-end tests.
4. Encrypted credential storage, lifecycle/background service, notifications, device validation.

Use GitHub Actions artifacts for the debug APK. Do not represent it as a full VPN release.
