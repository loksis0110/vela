# Сторонние компоненты

| Компонент | Лицензия | Где используется |
|---|---|---|
| [sing-box](https://github.com/SagerNet/sing-box) | GPL-3.0 с дополнительным условием о названии | ядро на всех платформах |
| [sing-box-for-android](https://github.com/SagerNet/sing-box-for-android), [sing-box-for-apple](https://github.com/SagerNet/sing-box-for-apple) | GPL-3.0 | части кода `DefaultNetworkMonitor.kt`, `VelaVpnService.kt`, `PlatformInterface.swift` адаптированы оттуда |
| [sing-geoip](https://github.com/SagerNet/sing-geoip), [sing-geosite](https://github.com/SagerNet/sing-geosite) | GPL-3.0; данные: [v2fly domain-list-community](https://github.com/v2fly/domain-list-community) (MIT) и GeoIP-база, указанная в репозитории sing-geoip | файлы `core/rules/*.srs` для умного режима и блокировки рекламы |
| [fyne.io/systray](https://github.com/fyne-io/systray) | Apache-2.0 | значок в трее Windows |
| [gomobile (SagerNet)](https://github.com/sagernet/gomobile) | BSD-3-Clause | сборка ядра для Android и iOS |
| Jetpack Compose, AndroidX | Apache-2.0 | интерфейс Android |

Перед публикацией сверьте условия лицензий GeoIP-базы в репозитории sing-geoip: у некоторых баз есть требование указать источник.
