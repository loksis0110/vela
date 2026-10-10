# Политика конфиденциальности Vela

_Действует с 10 октября 2026 года._ [English version below](#vela-privacy-policy).

Vela — клиент для подключения к VPN-серверам, которые пользователь добавляет сам. Мы не предоставляем серверы и не видим трафик. Разработчику Vela не отправляет никаких данных.

## Какие данные обрабатываются

- **Ссылки на серверы и подписки, настройки.** Хранятся только на устройстве пользователя в закрытом хранилище приложения. На Android и iOS исключены из резервных копий.
- **Сетевой трафик.** Шифруется и передаётся напрямую на сервер, выбранный пользователем. Vela не записывает, не анализирует и не передаёт сведения о посещаемых сайтах. Для части серверов (XHTTP, mKCP, шифрование VLESS) работает встроенный движок Xray-core. Он тоже работает внутри приложения и соединяется только с сервером пользователя.
- **Запросы к подписке.** Приложение скачивает подписку по ссылке пользователя. Сервер подписки видит IP-адрес устройства и заголовок `User-Agent` с названием приложения.
- **HWID и сведения об устройстве.** Если включено «Отправлять HWID» (по умолчанию включено), Vela добавляет к запросу подписки:
  - `x-hwid`: случайный номер, который Vela создаёт при первом запуске. Он не связан с IMEI, серийным номером, Android ID или рекламным идентификатором;
  - `x-device-os`, `x-ver-os`, `x-device-model`: название и версию системы и модель устройства (на Windows вместо модели передаётся «PC»).

  Зачем: многие провайдеры (например, на панели Remnawave) ограничивают число устройств на одну подписку и по этим данным показывают список устройств в личном кабинете. Данные уходят только на сервер подписки, которую добавил пользователь. Как отключить: **Настройки → «Отправлять HWID»**. Некоторые провайдеры без HWID не выдают подписку.
- **Объявления провайдера.** Сервер подписки может передать текст объявления (заголовок `announce` или строка `#announce:` в подписке). Vela только показывает его на устройстве.
- **Напоминания.** Уведомления об окончании подписки и расходе трафика Vela формирует на устройстве по данным, которые прислал провайдер.
- **Статистика трафика.** Vela считает, сколько данных прошло через VPN за каждый день (только объём, без адресов сайтов), и хранит это в файле на устройстве. Список активных соединений берётся из ядра на устройстве, только показывается и не сохраняется.
- **Буфер обмена.** Когда приложение открывается, Vela проверяет, нет ли в буфере обмена ссылки на подписку или сервер, и предлагает её добавить. Проверка выполняется на устройстве, текст никуда не отправляется. Vela запоминает только хеш последнего предложенного текста, чтобы не спрашивать повторно. iOS может спросить разрешение на вставку. На Windows проверку можно отключить в настройках.
- **Ссылки из приложений и сайтов.** Ссылки вида `vela://` (и схемы других клиентов, например `happ://`, `v2rayng://`, `sub://`) Vela разбирает на устройстве и перед добавлением показывает, что именно будет добавлено. Подписка скачивается только после подтверждения.
- **Проверка задержки и инструменты.** Для замера Vela обращается к `www.gstatic.com/generate_204` через выбранные серверы или подключается к самим серверам. По нажатию пользователя «Мой IP» обращается к `ipwho.is` и `www.cloudflare.com`, тест скорости к `speed.cloudflare.com`. Эти сервисы видят IP-адрес, с которого пришёл запрос.
- **Проверка обновлений (Windows).** Vela для Windows при запуске и раз в 6 часов скачивает файл `version.json` из релиза на GitHub (`github.com/loksis0110/vela`), чтобы узнать о новой версии. GitHub видит IP-адрес и `User-Agent`. Новая версия устанавливается только по команде пользователя. Отключить: **Настройки → «Проверять обновления автоматически»**.
- **Списки правил.** Списки для режимов маршрутизации встроены в приложение и применяются на устройстве. Во время работы Vela их не скачивает.

## Чего Vela не делает

- Не собирает аналитику и отчёты о сбоях. Статистика трафика хранится только на устройстве.
- Не содержит рекламы и трекеров.
- Не передаёт и не продаёт данные третьим лицам.
- Не требует регистрации.

## Ответственность владельца сервера

Трафик обрабатывается сервером, который добавил пользователь. Сервер подписки получает данные из запросов к подписке, описанные выше. Как владелец сервера обращается с данными, определяет его собственная политика.

## Удаление данных

Удалите подписку в приложении или само приложение: все данные Vela хранятся только на устройстве. Номер установки, который уже получил провайдер, может удалить только провайдер. После переустановки Vela создаёт новый номер.

## Изменения

Если политика изменится, мы обновим её текст и дату вверху.

## Контакты

Вопросы о конфиденциальности: _укажите email_.

---

# Vela Privacy Policy

_Effective 10 October 2026._

Vela is a client for VPN servers that users add themselves. We do not provide servers and cannot see your traffic. Vela sends no data to its developer.

## What data is processed

- **Server and subscription links, settings.** Stored only on your device, in the app's private storage. On Android and iOS they are excluded from backups.
- **Network traffic.** Encrypted and sent directly to the server you chose. Vela does not record, analyze or share which sites you visit. Some servers (XHTTP, mKCP, VLESS encryption) run on the embedded Xray-core engine. It also runs inside the app and connects only to your server.
- **Subscription requests.** The app downloads a subscription from the link you added. The subscription server sees your device's IP address and a `User-Agent` header with the app name.
- **HWID and device details.** If "Send HWID" is on (it is on by default), Vela adds to subscription requests:
  - `x-hwid`: a random number Vela generates on first launch. It is not derived from the IMEI, serial number, Android ID or advertising ID;
  - `x-device-os`, `x-ver-os`, `x-device-model`: the OS name and version and the device model (Windows sends "PC" instead of a model).

  Why: many providers (for example, on the Remnawave panel) limit the number of devices per subscription and use these details to list your devices in your account. The data goes only to the subscription server you added. To turn it off: **Settings → "Send HWID"**. Some providers refuse subscriptions without an HWID.
- **Provider announcements.** A subscription server may send an announcement (the `announce` header or a `#announce:` line). Vela only displays it on your device.
- **Reminders.** Notifications about subscription expiry and traffic use are created on your device from the data your provider sends.
- **Traffic statistics.** Vela counts how much data went through the VPN each day (volume only, no site addresses) and stores it in a file on your device. The active connections list comes from the core on your device; it is only displayed and never saved.
- **Clipboard.** When the app opens, Vela checks whether the clipboard holds a subscription or server link and offers to add it. The check runs on your device and the text is never sent anywhere. Vela keeps only a hash of the last offered text so it doesn't ask twice. iOS may ask for permission to paste. On Windows the check can be turned off in Settings.
- **Links from apps and websites.** Links such as `vela://` (and other clients' schemes, for example `happ://`, `v2rayng://`, `sub://`) are parsed on your device, and Vela shows what will be added before adding it. The subscription is downloaded only after you confirm.
- **Latency checks and tools.** To measure latency Vela requests `www.gstatic.com/generate_204` through the selected servers or connects to the servers directly. When you tap them, "My IP" queries `ipwho.is` and `www.cloudflare.com`, and the speed test uses `speed.cloudflare.com`. These services see the IP address the request comes from.
- **Update check (Windows).** At start and every 6 hours, Vela for Windows downloads `version.json` from the GitHub release (`github.com/loksis0110/vela`) to learn about new versions. GitHub sees your IP address and `User-Agent`. A new version is installed only when you choose to. To turn it off: **Settings → "Check for updates automatically"**.
- **Rule lists.** The lists used by the routing modes are built into the app and applied on your device. Vela does not download them while running.

## What Vela does not do

- No analytics or crash reports. Traffic statistics stay on your device.
- No ads or trackers.
- No sharing or selling of data to third parties.
- No sign-up.

## The server owner's responsibility

Your traffic is handled by the server you added. The subscription server receives the request data described above. How the server owner treats data is governed by their own policy.

## Deleting data

Delete the subscription in the app, or delete the app: all Vela data is stored only on your device. An installation ID your provider has already received can only be deleted by the provider. After a reinstall Vela generates a new one.

## Changes

If this policy changes, we will update the text and the date at the top.

## Contact

Privacy questions: _add an email_.
