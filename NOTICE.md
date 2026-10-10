# Сторонние компоненты

| Компонент | Лицензия | Где используется |
|---|---|---|
| [sing-box](https://github.com/SagerNet/sing-box) | GPL-3.0 с дополнительным условием о названии | ядро на всех платформах |
| [Xray-core](https://github.com/XTLS/Xray-core) | MPL-2.0 | встроенный движок для серверов, которые не поддерживает sing-box (XHTTP, mKCP, шифрование VLESS, конфигурации Xray JSON), на всех платформах (`xrayout/`) |
| [sing-box-for-android](https://github.com/SagerNet/sing-box-for-android), [sing-box-for-apple](https://github.com/SagerNet/sing-box-for-apple) | GPL-3.0 | части кода `DefaultNetworkMonitor.kt`, `VelaVpnService.kt`, `PlatformInterface.swift` адаптированы оттуда |
| [sing-geoip](https://github.com/SagerNet/sing-geoip), [sing-geosite](https://github.com/SagerNet/sing-geosite) | GPL-3.0; данные: [v2fly domain-list-community](https://github.com/v2fly/domain-list-community) (MIT) и GeoIP-база, указанная в репозитории sing-geoip | `core/rules/geoip-ru.srs`, `geosite-category-ru.srs`, `geosite-category-ads-all.srs` для умного режима и блокировки рекламы |
| [runetfreedom/russia-v2ray-rules-dat](https://github.com/runetfreedom/russia-v2ray-rules-dat) | GPL-3.0; данные: re:filter (MIT), antifilter community, [v2fly domain-list-community](https://github.com/v2fly/domain-list-community) (MIT) | `core/rules/geosite-ru-blocked.srs`, `geoip-ru-blocked.srs` и `ru-inside.srs` (собран из категорий v2fly: сервисы ИИ, Spotify, Netflix, YouTube, Discord, Telegram, X, Meta и др.) для режима «Только заблокированное» (подробнее ниже) |
| [WireGuard for Windows](https://git.zx2c4.com/wireguard-windows) (`tunnel/firewall`) | MIT | код фильтров WFP для kill switch адаптирован в `desktop/wfp_windows.go` (уведомление о лицензии сохранено в файле) |
| [fyne.io/systray](https://github.com/fyne-io/systray) | Apache-2.0 | значок в трее Windows |
| [gomobile (SagerNet)](https://github.com/sagernet/gomobile) | BSD-3-Clause | сборка ядра для Android и iOS |
| Jetpack Compose, AndroidX | Apache-2.0 | интерфейс Android |

## Списки для режима «Только заблокированное»

Списки обновляет `scripts/update-rules.sh` перед каждой сборкой. Лицензии проверены 10 октября 2026 года по клонам репозиториев.

- **runetfreedom/russia-v2ray-rules-dat** — GPL-3.0 (файл `LICENSE`). Файлы в нём собирают репозитории [russia-blocked-geosite](https://github.com/runetfreedom/russia-blocked-geosite) и [russia-blocked-geoip](https://github.com/runetfreedom/russia-blocked-geoip), оба тоже GPL-3.0. Исходные данные по их описанию: [antifilter.download](https://antifilter.download/) и community.antifilter.download, [re:filter](https://github.com/1andrevich/Re-filter-lists) (MIT), [v2fly domain-list-community](https://github.com/v2fly/domain-list-community) (MIT). Vela распространяется под GPL-3.0, условия совместимы.
- **ru-inside.srs** собирает `tools/rules` из файлов того же репозитория runetfreedom (категории v2fly domain-list-community, MIT, и списки IP Telegram, Facebook, X, Netflix). Список категорий — в `scripts/update-rules.sh`.

## Перед публикацией

- Сверьте условия лицензии GeoIP-базы в репозитории sing-geoip: у некоторых баз есть требование указать источник.
- MPL-2.0 (Xray-core) требует открывать изменения в файлах Xray-core. Vela подключает Xray-core как модуль Go без изменений, исходный код Vela открыт под GPL-3.0.
