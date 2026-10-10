# Google Play listing: English (en-US)

Copy each block as a whole. Google limits: app name 30 characters, short description 80, full description 4000, release notes 500. The lengths below are counted.

Written to Play rules: no anonymity promises, no "bypass censorship" wording or names of blocked services, no other apps' brands, no ad blocking (the `play` build doesn't have it). VpnService use is stated in the text, as the policy requires.

## App name (17 characters)

```
Vela — VPN Client
```

Alternatives: `Vela: VPN Client` (16), `Vela VPN Client` (15).

## Short description (76 characters)

```
Client for your own VPN servers: VLESS, Trojan, Hysteria2, subscriptions, QR
```

## Full description (about 2800 characters)

```
Vela is a client for VPN servers you add yourself. Vela does not provide servers: you need a subscription link from your provider or your own server.

CONNECTING
• One tap to connect. Automatic choice of the fastest server.
• Switch servers without reconnecting: open connections stay alive.
• If a server stops responding, Vela moves to a working one.
• Latency check with a real request through each server. For a server that fails, Vela shows why.

PROTOCOLS AND FORMATS
• VLESS (Reality, Vision, XHTTP, VLESS encryption), VMess, Trojan, Shadowsocks, Hysteria2, TUIC, mKCP.
• Subscriptions from popular panels (Marzban, 3x-ui, Remnawave and others): link lists, YAML and JSON.
• Add from your provider's website: tap the connect button in your account, Vela shows what will be added and asks you to confirm.
• Copied a subscription link? Vela offers to add it. The clipboard is checked on the device only and never sent anywhere.
• QR codes from the camera, a photo or a screenshot.

ROUTING
• All traffic through the VPN.
• Smart mode: Russian sites and services go direct, everything else through the VPN.
• Only sites from a built-in list go through the VPN, everything else direct.
• Your own "always direct" and "always via VPN" domain lists.
• Choose which apps use the VPN or bypass it.

SUBSCRIPTIONS
• Remaining traffic and expiry date on the home screen.
• Reminders 3 days and 1 day before the subscription ends and at 90% traffic used.
• Announcements from your provider.
• Auto-update at the interval your provider sets, or your own.

STATISTICS AND TOOLS
• Traffic usage by day and month, stored on your phone only.
• Active connections: which site goes through which server, with an option to close a connection.
• Speed graph, "My IP", speed test through the selected server, log.

CONVENIENCE
• Home screen widget and Quick Settings tile.
• Always-on VPN.
• Back up all settings as one text or a QR code.
• Light and dark themes, English and Russian.

PRIVACY
• No sign-up, no ads, no analytics.
• Subscriptions and settings stay on your device and are excluded from cloud backups.
• For providers that limit the number of devices, Vela can send a random installation ID, the phone model and the Android version when it updates a subscription. You can turn this off in Settings.

HOW VELA USES VPNSERVICE
Vela uses the Android VpnService to create an encrypted tunnel from your phone to the server you added. This is the app's core function. Vela does not collect data through the VPN tunnel, does not show ads and does not redirect other apps' traffic for monetization.

Please note: Vela encrypts traffic between your device and your server, but it does not make you anonymous. The server can see your connections, so choose a provider you trust.

Open source (GPL-3.0): https://github.com/loksis0110/vela
```

Keep the last line only once the repository is public (see section 9 of [GUIDE.md](GUIDE.md)); otherwise replace it with wherever the source code is published.

## What's new in 1.4 (372 characters)

```
• New protocols: XHTTP, mKCP and VLESS encryption
• YAML and JSON subscriptions
• Add a subscription from your provider's website or the clipboard
• New routing mode: only sites from a built-in list go through the VPN
• Provider announcements, reminders before a subscription or traffic runs out
• Traffic statistics by day and month, active connections
• English language
```

## Screenshots

Phone, portrait 1080×1920 (9:16), PNG without transparency, app language English. Use test servers only, no real links or IPs. The first 2–3 show up in search.

1. Home screen, "Connected", selected server, traffic and expiry visible.
2. Server list with latency, flags and the "Xray" badge on one server.
3. "Add subscription …?" confirmation after a link from a provider's site.
4. Routing mode choice: the three options with their explanations.
5. Statistics: bars for 31 days and monthly totals.
6. Active connections.
7. Subscription with a provider announcement and remaining traffic.
8. Dark theme or the home screen widget.

Captions, if you frame the screenshots: "Connect in one tap", "Your servers, your subscriptions", "Three routing modes", "Traffic statistics".

Feature graphic 1024×500: Vela logo and "VPN client for your own servers" on the app's colors.
