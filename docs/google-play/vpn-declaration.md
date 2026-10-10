# VpnService declaration: answers for Play Console

Play Console → **Policy → App content → VpnService** (появляется после загрузки сборки с `android.net.VpnService`). Ответы на английском, комментарии на русском. Вопросы взяты со страницы справки Google (ссылка в конце); в самой Play Console формулировки могут чуть отличаться.

> **Что важно для проверяющих.** VPN — основная функция Vela. Шифрование до сервера. Никакого вмешательства в трафик ради заработка. В сборке `play` **нет блокировки рекламы**: переключатель скрыт и принудительно выключен, потому что форма прямо предупреждает: «Any manipulation of ads that can impact monetization will result in rejection». Загружайте только `Vela-1.4.0-play.aab`.

## 1. Is providing a VPN the core functionality of your application?

**Yes.**

## 2. Which permitted functionality does your app provide?

> Обычно этот вопрос нужен приложениям, у которых VPN не основная функция. Если Play Console всё равно его покажет, выберите **Network-related tools (including remote access)**. «None of the above» не выбирайте: тогда Google потребует подробно объяснить, почему VpnService нужен, или убрать его.

If an explanation field is shown, use:

```
Vela is a VPN client. Its only purpose is to connect the user's device to VPN servers that the user adds (a subscription link from their own provider or their own server). Vela uses VpnService to create the device-level encrypted tunnel to that server. The app does not provide servers of its own.
```

## 3. Link to a video (90 seconds or less) showing your app being opened and your VPN being used

Ссылка на видео YouTube «По ссылке» (Unlisted) или Google Диск с доступом по ссылке. Сценарий записи: раздел 8 в [GUIDE.md](GUIDE.md). Коротко: запуск Vela с рабочего стола → добавление тестовой подписки → нажатие «Подключить» → системный «Запрос на подключение» → значок ключа и уведомление → «Мой IP» показывает IP сервера → настройка «Отправлять HWID» с пояснением → отключение из уведомления.

## 4(a). What data does your VPN service collect or share?

**No** (the VPN service does not collect or share user data).

> **Почему «No».** Через VpnService Vela только передаёт трафик на сервер пользователя: не читает его содержимое, не пишет адреса сайтов и не отправляет их разработчику. Статистика трафика (сколько байт за день) хранится только на устройстве.
>
> Единственное, что уходит с устройства, это HWID при обновлении подписки. Его отправляет загрузчик подписок, а не VPN-туннель, и он уже заявлен в Data safety (`data-safety.md`, «Device or other identifiers», необязательно). Если проверяющий не согласится и попросит отметить HWID здесь, ответьте **Yes → Device or other identifiers**. Тогда понадобится видео с заметным уведомлением (вопрос 5), а в приложении, вероятно, отдельный экран согласия перед первой отправкой HWID: пояснения под переключателем в настройках для этого может не хватить.

## 5. Link to a video showing the prominent disclosure shown to users in your app

> Нужен, если в 4(a) отмечен сбор данных. При ответе «No» поле может не появиться. Если поле обязательное, дайте ссылку на то же видео: в нём показан переключатель «Отправлять HWID» с полным текстом пояснения: «Случайный номер этой установки, модель телефона и версия Android. Нужен провайдерам с лимитом устройств, личных данных нет».

## 5(a). Does your application redirect or manipulate user traffic from other apps on a device for monetization purposes?

**No.**

> Vela не показывает рекламу, не подменяет и не блокирует рекламу (в сборке `play`), не перенаправляет трафик через другие страны ради заработка, не продаёт трафик и не встраивает партнёрские ссылки.

## Additional explanation (if there is a free-text field)

```
Vela is an open-source VPN client (GPL-3.0). VPN is its core functionality: it connects the device to VPN servers that the user adds from their own provider's subscription or their own server. Vela does not provide servers.

Encryption: Vela encrypts the data from the device to the VPN tunnel endpoint with the protocol of the user's server, such as TLS 1.3 or REALITY (VLESS, Trojan, VMess), QUIC (Hysteria2, TUIC), Shadowsocks AEAD ciphers or VLESS encryption.

No traffic manipulation: Vela does not show, inject, block or modify ads, does not redirect other apps' traffic for monetization and does not sell or share traffic. The Google Play build has no ad blocking.

Data handling: there is no account, analytics, crash reporting or advertising SDK. Servers, subscriptions, settings and traffic statistics (byte counts per day) are stored only on the device and excluded from cloud backups. When the app updates a subscription it sends a User-Agent and, unless the user turns it off in Settings, a random installation ID with the OS name, OS version and device model to the subscription server the user added, so that the provider can enforce its device limit. Nothing is sent to the developer.

VpnService use is described in the store listing.
```

> **Про шифрование, для себя.** Все протоколы, которые выдают панели провайдеров, шифруют трафик. Но ядро принимает и вручную созданные серверы без шифрования (например, VLESS с `security=none` без шифрования VLESS). Чтобы фраза «encrypts the data from the device to the endpoint» была верна без оговорок, в сборке `play` стоит предупреждать о таких серверах или не подключаться к ним.

## Источник

- VpnService policy and declaration form: https://support.google.com/googleplay/android-developer/answer/12564964
- Policy text (Permissions and APIs that Access Sensitive Information, VPN Service): https://support.google.com/googleplay/android-developer/answer/16558241#vpn
