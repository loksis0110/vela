package core

import (
	"fmt"
	"strings"
	"sync/atomic"
)

var language atomic.Value // "ru" (default) or "en"

// SetLanguage switches messages produced by the core ("ru" or "en"; other
// values fall back to English when they are not Russian).
func SetLanguage(lang string) {
	lang = strings.ToLower(strings.TrimSpace(lang))
	if strings.HasPrefix(lang, "ru") || lang == "" {
		language.Store("ru")
		return
	}
	language.Store("en")
}

// Language returns the current message language.
func Language() string {
	if v, ok := language.Load().(string); ok {
		return v
	}
	return "ru"
}

// T translates a Russian message into the current language.
func T(ru string) string {
	if Language() == "ru" {
		return ru
	}
	if en, ok := english[ru]; ok {
		return en
	}
	return ru
}

// errorf is fmt.Errorf with a translated format string.
func errorf(format string, args ...any) error {
	return fmt.Errorf(T(format), args...)
}

var english = map[string]string{
	"%s: в конфигурации Xray нет адреса сервера": "%s: the Xray config has no server address",
	"%s: в конфигурации Xray нет сервера":        "%s: the Xray config has no server",
	"OK %d мс": "OK %d ms",
	"Reality: сервер не принял ключ (pbk/sid) или SNI":   "Reality: the server did not accept the key (pbk/sid) or SNI",
	"Vela: проверка серверов %s\nработают: %d из %d\n\n": "Vela: server check %s\nworking: %d of %d\n\n",
	"flow %s не поддерживается":                          "flow %s is not supported",
	"security=%s не поддерживается":                      "security=%s is not supported",
	"ОШИБКА: ": "ERROR: ",
	"в vmess нет адреса, порта или id":       "the vmess link has no address, port or id",
	"в подписке нет поддерживаемых серверов": "the subscription has no supported servers",
	"в ссылке Reality нет pbk":               "the Reality link has no pbk",
	"в ссылке нет подписки":                  "the link has no subscription",
	"в ссылке нет серверов":                  "the link has no servers",
	"зашифрованные ссылки Happ открываются только в Happ. Попросите у провайдера обычную ссылку на подписку": "encrypted Happ links open only in Happ. Ask your provider for a regular subscription link",
	"на картинке не найден QR-код":    "no QR code found in the picture",
	"не проверен":                     "not checked",
	"не ссылка: %s":                   "not a link: %s",
	"не удалось декодировать vmess":   "could not decode the vmess link",
	"не удалось измерить скорость":    "could not measure the speed",
	"не удалось найти адрес сервера":  "could not resolve the server address",
	"не удалось прочитать ссылку":     "could not read the link",
	"неверная ss-ссылка":              "invalid ss link",
	"неверный vmess JSON":             "invalid vmess JSON",
	"неверный адрес":                  "invalid address",
	"неверный параметр extra у XHTTP": "invalid XHTTP extra parameter",
	"неверный порт":                   "invalid port",
	"нет UUID":                        "no UUID",
	"нет адреса сервера":              "no server address",
	"нет метода или пароля":           "no method or password",
	"нет ответа: ":                    "no response: ",
	"нет пароля":                      "no password",
	"нет серверов для подключения":    "no servers to connect to",
	"отпечаток сертификата сервера не совпал со ссылкой": "the server certificate fingerprint does not match the link",
	"ошибка соединения":                             "connection error",
	"повреждённая запись сервера":                   "damaged server entry",
	"подписка не найдена (404). Проверьте ссылку":   "subscription not found (404). Check the link",
	"порт сервера закрыт":                           "the server port is closed",
	"протокол %s не поддерживается":                 "protocol %s is not supported",
	"резервная копия повреждена":                    "the backup is damaged",
	"сервер %v: цепочки (detour) не поддерживаются": "server %v: chains (detour) are not supported",
	"слишком много перенаправлений":                 "too many redirects",
	"сервер не ответил вовремя":                     "the server did not answer in time",
	"сервер не отвечает: ":                          "the server does not answer: ",
	"сервер не удалось разобрать":                   "the server could not be parsed",
	"сервер недоступен из этой сети":                "the server is unreachable from this network",
	"сервер отклонил ключ доступа (UUID/пароль)":    "the server rejected the access key (UUID/password)",
	"сервер подписки ответил %d":                    "the subscription server answered %d",
	"сервер подписки отказал (%d). Возможно, нужен HWID или превышен лимит устройств": "the subscription server refused (%d). It may need a HWID or the device limit is reached",
	"сервер разорвал соединение":                   "the server closed the connection",
	"сертификат сервера не прошёл проверку":        "the server certificate failed verification",
	"тип %s не поддерживается":                     "type %s is not supported",
	"транспорт %s не поддерживается":               "transport %s is not supported",
	"транспорт XHTTP пока не поддерживается ядром": "the XHTTP transport is not supported by the core yet",
	"транспорт mKCP не поддерживается":             "the mKCP transport is not supported",
	"это не картинка":                              "this is not a picture",
	"это не ссылка на подписку":                    "this is not a subscription link",
	"это не ссылка":                                "this is not a link",

	"Xray не принимает настройки сервера: %v":         "Xray does not accept the server settings: %v",
	"sing-box не принимает настройки сервера: %v":     "sing-box does not accept the server settings: %v",
	"картинка слишком большая":                        "the picture is too large",
	"не удалось загрузить подписку: %s (%v)":          "could not download the subscription: %s (%v)",
	"неверная ссылка":                                 "invalid link",
	"нет серверов с шифрованием трафика":              "no servers that encrypt traffic",
	"плагин Shadowsocks %s не поддерживается":         "the Shadowsocks plugin %s is not supported",
	"подписка слишком большая":                        "the subscription is too large",
	"операция отменена":                               "the operation was cancelled",
	"сервер не принял TLS-соединение (проверьте SNI)": "the server refused the TLS connection (check the SNI)",
	"сервер ответил ошибкой":                          "the server answered with an error",
	"слишком много данных для QR-кода":                "too much data for a QR code",
	"шифрование %s не поддерживается":                 "the %s cipher is not supported",
	"obfs %s не поддерживается":                       "obfs %s is not supported",
}
