# Data safety: answers for Play Console

Play Console → **Policy → App content → Data safety**. Answers in English (as the form shows them), comments in Russian.

> **Комментарий.** Google считает «сбором» (collection) любую передачу данных с устройства, в том числе третьей стороне, а не только разработчику. Разработчик Vela не получает ничего. Но при обновлении подписки Vela по умолчанию отправляет провайдеру случайный номер установки (заголовок `x-hwid`), а также ОС, её версию и модель телефона (`x-device-os`, `x-ver-os`, `x-device-model`): так провайдеры с панелью Remnawave считают устройства. Поэтому честный ответ: **собирается один тип данных, «Device or other identifiers», необязательно**. Если в сборке `play` HWID когда-нибудь уберут совсем, ответ меняется на «No data collected» (вариант Б в конце).
>
> Ответы должны совпадать с `PRIVACY.md`. Изменилось поведение приложения: обновите и форму, и политику.

## Overview

| Question | Answer |
|---|---|
| Does your app collect or share any of the required user data types? | **Yes** |
| Is all of the user data collected by your app encrypted in transit? | **No** for now; **Yes** once HWID headers go over HTTPS only (see the note) |
| Which of the following methods of account creation does your app support? | **My app does not allow users to create an account** |
| Do you provide a way for users to request that their data is deleted? | **No** (see comment) |

> **Шифрование при передаче.** Подписки почти всегда скачиваются по HTTPS. Но ядро сейчас отправляет заголовки HWID и для ссылок `http://`, если пользователь добавит такую. Чтобы ответ «Yes» был верен без оговорок, в ядре стоит отправлять `x-hwid` и `x-device-*` только по HTTPS. Пока этого нет, строго честный ответ: **No**. В карточке приложения тогда просто не будет пункта «Данные шифруются при передаче».
>
> **Удаление данных.** Номер установки хранится у провайдера подписки, а не у разработчика: удалить его может только провайдер. На устройстве пользователь удаляет всё сам (удалить подписку или приложение). Поэтому «No». Отдельный раздел «Data deletion» с URL для удаления аккаунта не нужен: аккаунтов в Vela нет.

## Data types

Only one data type is collected. Everything else: **not collected, not shared**.

### Device or other identifiers: collected

| Question | Answer |
|---|---|
| Collected | **Yes** |
| Shared | **No** |
| Is this data processed ephemerally? | **No** |
| Is this data required for your app, or can users choose whether it's collected? | **Users can choose whether this data is collected** |
| Why is this user data collected? | **App functionality** |

> **Почему так.**
> - Что именно: случайный номер, который Vela создаёт при первом запуске (не IMEI, не Android ID, не рекламный ID), плюс «Android», версия Android и модель телефона.
> - Куда: только на адрес подписки, который добавил сам пользователь, при её обновлении. Разработчику ничего не уходит.
> - Зачем: провайдер ограничивает число устройств на одну подписку (App functionality).
> - Shared = No: передача идёт по действию пользователя (он сам добавил подписку своего провайдера и оставил включённым «Отправлять HWID»). Google не считает это «sharing», если пользователь разумно ожидает такую передачу.
> - Optional: Настройки → «Отправлять HWID». Выключил: заголовки не отправляются.
> - Модель телефона и версия ОС в списке типов данных Google отдельно не значатся, их относим к тому же пункту.

### Not collected (all other types)

| Data type | Answer | Why (для себя) |
|---|---|---|
| Location (approximate, precise) | Not collected | Нет разрешения на геолокацию. «Мой IP» запрашивает ipwho.is по нажатию пользователя, результат только показывается на экране |
| Personal info (name, email, user IDs, address, phone, …) | Not collected | Нет регистрации и аккаунтов |
| Financial info | Not collected | Нет покупок |
| Health and fitness | Not collected | — |
| Messages | Not collected | — |
| Photos and videos | Not collected | QR-код из фото распознаётся на устройстве, фото никуда не отправляется |
| Audio files | Not collected | — |
| Files and docs | Not collected | Резервная копия сохраняется или отправляется только туда, куда пользователь сам выберет |
| Calendar, Contacts | Not collected | — |
| App activity (interactions, search history, installed apps, other actions) | Not collected | Список приложений для раздельного туннелирования читается на устройстве. Статистика трафика и журнал хранятся только на устройстве. Буфер обмена проверяется на устройстве |
| Web browsing history | Not collected | Трафик идёт зашифрованным на сервер пользователя, Vela не записывает и не отправляет адреса сайтов. Список активных соединений берётся из локального ядра и только показывается |
| App info and performance (crash logs, diagnostics, other) | Not collected | Нет отчётов о сбоях и аналитики. Журнал уходит, только если пользователь сам им поделится |

> **Трафик VPN.** Сам трафик пользователя не «собирается»: Vela передаёт его зашифрованным на сервер, который выбрал пользователь, и не имеет к нему доступа как разработчик. Ответственность за обработку на сервере несёт его владелец (это сказано в `PRIVACY.md`).
>
> **IP-адрес.** Сервер подписки, VPN-сервер, `www.gstatic.com` (проверка задержки), `ipwho.is` и Cloudflare (инструменты «Мой IP» и тест скорости) видят IP-адрес, как при любом сетевом запросе. Vela его не собирает и никуда не пересылает, поэтому отдельно не декларируется.

## Security practices

| Question | Answer |
|---|---|
| Data is encrypted in transit | Same as above: No for now, Yes after the HTTPS-only change |
| You provide a way for users to request that their data is deleted | No |
| Committed to follow the Play Families Policy | Not applicable (target audience 18+) |
| Independent security review | No |

## Вариант Б: если в сборке `play` нет HWID

Если HWID в сборке `play` полностью отключён (не только выключен по умолчанию, а не отправляется вообще), ответ на первый вопрос: **No, my app does not collect or share any of the required user data types**, и остальные разделы формы пропускаются. Пока переключатель «Отправлять HWID» есть в приложении, используйте основной вариант выше.
