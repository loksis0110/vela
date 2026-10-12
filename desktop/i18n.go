package main

import (
	"sync/atomic"

	"github.com/velaapp/vela/core"
)

// Messages of the Windows app are written in Russian and translated with
// tr(). The window has its own dictionary (ui/i18n.js); the core translates
// its messages after core.SetLanguage.

var uiLanguage atomic.Value // "ru" or "en"

func language() string {
	if v, ok := uiLanguage.Load().(string); ok {
		return v
	}
	return "ru"
}

// resolveLanguage turns the setting ("" = like Windows) into "ru" or "en".
func resolveLanguage(pref string) string {
	switch pref {
	case "ru", "en":
		return pref
	}
	return systemLanguage()
}

func setLanguage(lang string) {
	if lang != "ru" {
		lang = "en"
	}
	uiLanguage.Store(lang)
	core.SetLanguage(lang)
}

// tr translates a Russian message into the current language.
func tr(ru string) string {
	if language() == "ru" {
		return ru
	}
	if en, ok := english[ru]; ok {
		return en
	}
	return ru
}

var english = map[string]string{
	". Не удалось: ":                                           ". Failed: ",
	"Kill switch не включился: ":                               "The kill switch could not be turned on: ",
	"Kill switch работает только в Windows":                    "The kill switch works only on Windows",
	"Kill switch выключен: не удалось найти адаптер Vela. ":    "The kill switch is off: the Vela adapter was not found. ",
	"В подписке нет серверов. Обновите её или выберите другую": "The subscription has no servers. Update it or choose another one",
	"TUN (весь компьютер)":                                     "TUN (whole computer)",
	"Vela обновлена до версии %s":                              "Vela has been updated to version %s",
	"Vela: не подключено":                                      "Vela: not connected",
	"Vela: отключение…":                                        "Vela: disconnecting…",
	"Vela: подключение…":                                       "Vela: connecting…",
	"Vela: подключено":                                         "Vela: connected",
	"«%s» обновлена":                                           "\"%s\" updated",
	"«%s» перестал отвечать. Vela переключилась на автовыбор":  "\"%s\" stopped responding. Vela switched to automatic selection",
	"«%s»: серверов %d":                                        "\"%s\": %d servers",
	"Автовыбор":                                                "Automatic",
	"В подписке «%s» израсходовано %d%% трафика":               "%[2]d%% of the traffic of \"%[1]s\" is used",
	"Восстановлено. ":                                          "Restored. ",
	"Все подписки обновлены":                                   "All subscriptions updated",
	"Всё через VPN":                                            "Everything through VPN",
	"Выбранный сервер не отвечает: %s. Выберите другой сервер или «Автовыбор»": "The selected server does not respond: %s. Choose another server or \"Automatic\"",
	"Выйти": "Quit",
	"Горячая клавиша Vela не работает":                               "The Vela hotkey does not work",
	"Для режима «Весь компьютер» нужны права администратора":         "The \"Whole computer\" mode needs administrator rights",
	"Доступна версия %s":                                             "Version %s is available",
	"Интернет заблокирован: VPN не подключён":                        "Internet blocked: VPN is not connected",
	"Маршрутизация":                                                  "Routing",
	"Мои серверы":                                                    "My servers",
	"Название сохранено":                                             "Name saved",
	"Настройки восстановлены":                                        "Settings restored",
	"Настройки восстановлены. Подписки не загрузились: ":             "Settings restored. Subscriptions could not be downloaded: ",
	"Настройки сброшены":                                             "Settings reset",
	"Не удалось закрыть соединение":                                  "Could not close the connection",
	"Не удалось запустить Vela: порт 47390 занят другой программой.": "Could not start Vela: port 47390 is used by another program.",
	"Не удалось подключиться: ":                                      "Could not connect: ",
	"Не удалось получить список соединений":                          "Could not get the list of connections",
	"Не удалось проверить обновления":                                "Could not check for updates",
	"Не удалось скачать обновление. Попробуйте позже":                "Could not download the update. Try again later",
	"Нет прав администратора для создания сетевого адаптера":         "No administrator rights to create the network adapter",
	"Нет серверов":                                                   "No servers",
	"Нет подключения к интернету":                                    "No internet connection",
	"Ни один сервер пока не ответил. Проверьте подписку или интернет-соединение": "No server has answered yet. Check the subscription or the internet connection",
	"Новая версия не найдена. Проверьте обновления ещё раз":                      "No new version found. Check for updates again",
	"Обновление повреждено: контрольная сумма не совпала":                        "The update is damaged: the checksum does not match",
	"Обновление повреждено: размер не совпал":                                    "The update is damaged: the size does not match",
	"Обновлено %d из %d подписок":                                                "Updated %d of %d subscriptions",
	"Отключить":    "Disconnect",
	"Открыть Vela": "Open Vela",
	"Подключить":   "Connect",
	"Подписка «%s» закончилась. Продлите её у провайдера":                         "The \"%s\" subscription has expired. Renew it with your provider",
	"Подписка «%s» закончится меньше чем через сутки":                             "The \"%s\" subscription expires in less than a day",
	"Подписка «%s» закончится через %d дня":                                       "The \"%s\" subscription expires in %d days",
	"Подписка обновлена":                                                          "Subscription updated",
	"Порт локального прокси занят другой программой. Поменяйте порт в настройках": "The local proxy port is used by another program. Change the port in the settings",
	"Режим":                  "Mode",
	"Сервер удалён":          "Server deleted",
	"Сервер":                 "Server",
	"Системный прокси":       "System proxy",
	"Только заблокированное": "Only blocked sites",
	"У вас последняя версия": "You have the latest version",
	"Удалено":                "Deleted",
	"Умная":                  "Smart",
	"вставьте ссылку на подписку или сервер":                     "paste a subscription or server link",
	"добавьте к клавише Ctrl, Alt или Win":                       "add Ctrl, Alt or Win to the key",
	"запуск с правами администратора отменён":                    "starting with administrator rights was cancelled",
	"запустите Vela через sudo, чтобы перехватывать весь трафик": "start Vela with sudo to capture all traffic",
	"используйте букву, цифру или F1–F24":                        "use a letter, a digit or F1–F24",
	"не найден сетевой адаптер Vela":                             "the Vela network adapter was not found",
	"не нашли поддерживаемых ссылок":                             "no supported links found",
	"не удалось заменить файл Vela: %v":                          "could not replace the Vela file: %v",
	"не удалось запустить новую версию: %v":                      "could not start the new version: %v",
	"не удалось назначить %s: %v":                                "could not assign %s: %v",
	"не удалось обновить подписки, проверьте интернет":           "could not update the subscriptions, check the internet connection",
	"не удалось прочитать картинку":                              "could not read the picture",
	"неизвестная клавиша %s":                                     "unknown key %s",
	"неизвестный язык":                                           "unknown language",
	"нет доступа к папке Vela: %v":                               "no access to the Vela folder: %v",
	"перестал отвечать во время работы":                          "stopped responding while in use",
	"подписка не найдена":                                        "subscription not found",
	"подписок пока нет":                                          "there are no subscriptions yet",
	"пропущено ссылок: %d":                                       "links skipped: %d",
	"профиль не найден":                                          "profile not found",
	"сервер не найден":                                           "server not found",
	"серверов добавлено: %d":                                     "servers added: %d",
	"слишком много данных для QR-кода":                           "too much data for a QR code",
	"сначала добавьте сервер или подписку":                       "add a server or a subscription first",
	"сочетание %s уже занято другой программой":                  "%s is already used by another program",
	"ссылка устарела, откройте её ещё раз":                       "the link has expired, open it again",
	"тест скорости уже идёт":                                     "the speed test is already running",
	"удалять можно только серверы, добавленные вручную":          "only servers added manually can be deleted",
	"эти серверы уже есть в списке":                              "these servers are already in the list",
}
